package workspacetool

import (
	"context"
	"fmt"
	"io"
	"os"
	slashpath "path"
	"path/filepath"
	"regexp"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

// maxSearchDepth caps directory recursion. Hitting it is disclosed in the
// search output rather than silently trimming the walk.
const maxSearchDepth = 24

type searchArgs struct {
	Query         string `json:"query"`
	Path          string `json:"path"`
	Mode          string `json:"mode"`
	Glob          string `json:"glob"`
	Context       int    `json:"context"`
	MaxResults    int    `json:"max_results"`
	CaseSensitive *bool  `json:"case_sensitive"`
}

// SearchTool returns a tool that searches files and returns compact matches.
func (w *Workspace) SearchTool() ds4.ToolHandler {
	return newTool(schema("search", "Search files and return compact edit-friendly matches.", searchParams),
		func(ctx context.Context, a searchArgs) (string, error) {
			if a.Query == "" {
				return "", fmt.Errorf("search requires query")
			}
			if a.Path == "" {
				a.Path = "."
			}
			target, err := w.resolvePath(a.Path, accessRead)
			if err != nil {
				return "", err
			}
			if a.Glob != "" {
				if _, err := filepath.Match(a.Glob, "probe"); err != nil {
					return "", fmt.Errorf("invalid glob %q: %v", a.Glob, err)
				}
			}
			s := searcher{
				w:             w,
				osRoot:        target.root,
				searchDir:     target.abs,
				query:         a.Query,
				glob:          a.Glob,
				context:       clampInt(a.Context, 0, 0, 5),
				maxResults:    clampInt(a.MaxResults, w.cfg.MaxSearchResults, 1, w.cfg.MaxSearchResults),
				caseSensitive: true,
			}
			if a.CaseSensitive != nil {
				s.caseSensitive = *a.CaseSensitive
			}
			if w.cfg.FollowSymlinks {
				s.visited = make(map[string]bool)
			}
			if a.Mode == "regex" {
				flags := ""
				if !s.caseSensitive {
					flags = "(?i)"
				}
				re, err := regexp.Compile(flags + a.Query)
				if err != nil {
					return "", fmt.Errorf("invalid regex: %w", err)
				}
				s.re = re
			}
			if err := s.searchNode(ctx, target.rel, target.abs, 0); err != nil {
				return "", err
			}
			notice := s.truncationNotice()
			if s.out.Len() == 0 {
				return "No matches\n" + notice, nil
			}
			return fmt.Sprintf("%d match%s shown\n\n%s%s", s.results, plural(s.results), s.out.String(), notice), nil
		})
}

type searcher struct {
	w             *Workspace
	osRoot        *os.Root // nil => walk absolute paths (AllowOutsideRoot)
	searchDir     string   // absolute search root, for glob-relative matching
	query         string
	glob          string
	re            *regexp.Regexp
	context       int
	maxResults    int
	results       int
	caseSensitive bool
	out           strings.Builder
	// visited tracks canonical directory paths to stop symlink cycles from
	// re-traversing directories; nil unless FollowSymlinks is set.
	visited map[string]bool
	// truncated and depthLimited record that the walk stopped early, so a
	// partial result set is never presented as an exhaustive one.
	truncated    bool
	depthLimited bool
}

// truncationNotice discloses a walk that stopped before exhausting its scope.
func (s *searcher) truncationNotice() string {
	var b strings.Builder
	if s.truncated {
		fmt.Fprintf(&b, "[Search stopped at max_results=%d; more matches may exist.]\n", s.maxResults)
	}
	if s.depthLimited {
		fmt.Fprintf(&b, "[Search stopped at directory depth %d; deeper subdirectories were not searched.]\n", maxSearchDepth)
	}
	return b.String()
}

// searchNode walks one entry. rel is the name relative to the workspace root
// used for confined os.Root operations; abs is the absolute path used for
// display, glob matching, and unconfined operations. When osRoot is nil the
// caller opted out of confinement and only abs is used.
func (s *searcher) searchNode(ctx context.Context, rel, abs string, depth int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.results >= s.maxResults {
		s.truncated = true
		return nil
	}
	if depth > maxSearchDepth {
		s.depthLimited = true
		return nil
	}
	info, err := s.lstat(rel, abs)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		if !s.w.cfg.FollowSymlinks {
			return nil
		}
		// Follow the link by its target type. In confined mode os.Root.Stat
		// refuses targets outside the root; unconfined mode is AllowOutsideRoot.
		info, err = s.stat(rel, abs)
		if err != nil {
			return nil
		}
	}
	if info.IsDir() {
		if s.visited != nil {
			// Key on the canonical (symlink-resolved) path so a directory
			// reached again through a symlink cycle is visited once. This is
			// portable, unlike device/inode identity.
			key, err := filepath.EvalSymlinks(abs)
			if err != nil {
				key = filepath.Clean(abs)
			}
			if s.visited[key] {
				return nil
			}
			s.visited[key] = true
		}
		entries, err := s.readDirEntries(rel, abs)
		if err != nil {
			return nil
		}
		for _, ent := range entries {
			if s.results >= s.maxResults {
				s.truncated = true
				break
			}
			if ent.Name() == ".git" {
				continue
			}
			childRel := ""
			if s.osRoot != nil {
				childRel = filepath.Join(rel, ent.Name())
			}
			if err := s.searchNode(ctx, childRel, filepath.Join(abs, ent.Name()), depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() > s.w.cfg.MaxSearchFileBytes {
		return nil
	}
	if s.glob != "" && !s.globMatches(abs) {
		return nil
	}
	return s.searchFile(rel, abs)
}

func (s *searcher) lstat(rel, abs string) (os.FileInfo, error) {
	if s.osRoot != nil {
		return s.osRoot.Lstat(rel)
	}
	return os.Lstat(abs)
}

func (s *searcher) stat(rel, abs string) (os.FileInfo, error) {
	if s.osRoot != nil {
		return s.osRoot.Stat(rel)
	}
	return os.Stat(abs)
}

func (s *searcher) readDirEntries(rel, abs string) ([]os.DirEntry, error) {
	if s.osRoot == nil {
		return os.ReadDir(abs)
	}
	f, err := s.osRoot.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

func (s *searcher) openFile(rel, abs string) (*os.File, error) {
	if s.osRoot != nil {
		return s.osRoot.Open(rel)
	}
	return os.Open(abs)
}

// globMatches accepts a file when the glob matches its basename or its path
// relative to the search root, so patterns like "sub/*.go" work. The glob is
// validated up front, so Match errors cannot occur here.
func (s *searcher) globMatches(abs string) bool {
	if ok, _ := filepath.Match(s.glob, filepath.Base(abs)); ok {
		return true
	}
	rel, err := filepath.Rel(s.searchDir, abs)
	if err != nil {
		return false
	}
	ok, _ := slashpath.Match(filepath.ToSlash(s.glob), filepath.ToSlash(rel))
	return ok
}

func (s *searcher) searchFile(rel, abs string) error {
	f, err := s.openFile(rel, abs)
	if err != nil {
		return nil
	}
	defer f.Close()
	// Bound the read off the open handle so a file that grew since it was
	// stat-ed cannot allocate past the limit; over-limit files are skipped.
	data, err := io.ReadAll(io.LimitReader(f, s.w.cfg.MaxSearchFileBytes+1))
	if err != nil {
		return nil
	}
	if int64(len(data)) > s.w.cfg.MaxSearchFileBytes {
		return nil
	}
	if isBinary(data) {
		return nil
	}
	lines := splitLines(string(data))
	printed := false
	lastContext := -1
	for i, line := range lines {
		if s.results >= s.maxResults {
			s.truncated = true
			break
		}
		if !s.match(line) {
			continue
		}
		if !printed {
			s.out.WriteString(abs)
			s.out.WriteByte('\n')
			printed = true
		}
		from, to := i-s.context, i+s.context
		if from < 0 {
			from = 0
		}
		if to >= len(lines) {
			to = len(lines) - 1
		}
		if from <= lastContext {
			from = lastContext + 1
		}
		for j := from; j <= to; j++ {
			fmt.Fprintf(&s.out, "  %d %s\n", j+1, lines[j])
			lastContext = j
		}
		s.results++
	}
	if printed {
		s.out.WriteByte('\n')
	}
	return nil
}

func (s *searcher) match(line string) bool {
	if s.re != nil {
		return s.re.MatchString(line)
	}
	q, target := s.query, line
	if !s.caseSensitive {
		q = strings.ToLower(q)
		target = strings.ToLower(target)
	}
	return strings.Contains(target, q)
}

func splitLines(text string) []string {
	raw := strings.Split(text, "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	for i := range raw {
		raw[i] = strings.TrimRight(raw[i], "\r")
	}
	return raw
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "es"
}
