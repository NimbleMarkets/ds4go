package workspacetool

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	ds4 "github.com/NimbleMarkets/ds4go"
)

type readArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	MaxLines  int    `json:"max_lines"`
	Whole     bool   `json:"whole"`
	Raw       bool   `json:"raw"`
}

type moreArgs struct {
	Count int `json:"count"`
}

// ReadTool returns a tool that reads a text file or line range.
func (w *Workspace) ReadTool() ds4.ToolHandler {
	return newTool(schema("read", "Read a text file or a range of lines.", readParams),
		func(ctx context.Context, a readArgs) (string, error) {
			_ = ctx
			target, err := w.resolvePath(a.Path, accessRead)
			if err != nil {
				return "", err
			}
			start := clampInt(a.StartLine, 1, 1, 0)
			count := clampInt(a.MaxLines, w.cfg.MaxReadLines, 1, w.cfg.MaxReadLines)
			return w.readRange(target, start, count, a.Whole, a.Raw, true)
		})
}

// MoreTool returns a tool that continues the previous read-like output.
func (w *Workspace) MoreTool() ds4.ToolHandler {
	return newTool(schema("more", "Continue the previous read-like output.", moreParams),
		func(ctx context.Context, a moreArgs) (string, error) {
			_ = ctx
			w.mu.Lock()
			more := w.more
			w.mu.Unlock()
			if !more.valid {
				return "", fmt.Errorf("no previous output to continue")
			}
			target, err := w.resolvePath(more.path, accessRead)
			if err != nil {
				return "", err
			}
			count := clampInt(a.Count, w.cfg.MaxReadLines, 1, w.cfg.MaxReadLines)
			return w.readRange(target, more.nextLine, count, false, more.raw, true)
		})
}

func (w *Workspace) readRange(t pathTarget, startLine, maxLines int, whole, raw, setMore bool) (string, error) {
	if whole {
		return w.readWhole(t, raw)
	}
	path := t.abs

	f, err := t.open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	var out strings.Builder
	r := bufio.NewReaderSize(f, 64<<10)
	lineNo := 0
	shown := 0
	hasMore := false
	var lineBuf []byte
	for {
		frag, readErr := r.ReadSlice('\n')
		if readErr == bufio.ErrBufferFull {
			// A single line longer than the read buffer: accumulate it, but
			// never past MaxReadBytes so memory stays bounded.
			lineBuf = append(lineBuf, frag...)
			if int64(len(lineBuf)) > w.cfg.MaxReadBytes {
				return "", fmt.Errorf("read output exceeds MaxReadBytes")
			}
			continue
		}
		var line string
		if len(lineBuf) > 0 {
			lineBuf = append(lineBuf, frag...)
			line = string(lineBuf)
			lineBuf = lineBuf[:0]
		} else {
			line = string(frag)
		}
		if len(line) > 0 {
			lineNo++
			if strings.ContainsRune(line, '\x00') {
				return "", fmt.Errorf("refusing to read binary file: %s", path)
			}
			if lineNo >= startLine && shown < maxLines {
				if raw {
					out.WriteString(line)
				} else {
					fmt.Fprintf(&out, "%d %s", lineNo, strings.TrimRight(line, "\n"))
					out.WriteByte('\n')
				}
				shown++
				if int64(out.Len()) > w.cfg.MaxReadBytes {
					return "", fmt.Errorf("read output exceeds MaxReadBytes")
				}
			} else if lineNo >= startLine && shown >= maxLines {
				hasMore = true
				break
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}

	if !raw {
		header := fmt.Sprintf("%s: lines %d-%d", path, startLine, startLine+shown-1)
		if shown == 0 {
			header = fmt.Sprintf("%s: no lines at or after %d", path, startLine)
		}
		if hasMore {
			header += fmt.Sprintf("; continue_offset=%d; call more with count=%d to read the next chunk", startLine+shown, maxLines)
		}
		outStr := out.String()
		out.Reset()
		out.WriteString(header)
		out.WriteByte('\n')
		out.WriteString(outStr)
	} else if hasMore {
		// Keep the file bytes exact: the truncation notice leads the output so
		// it can never be mistaken for file content.
		notice := fmt.Sprintf("[Read truncated: showing lines %d-%d; continue_offset=%d; call more with count=%d for the next chunk.]\n",
			startLine, startLine+shown-1, startLine+shown, maxLines)
		content := out.String()
		out.Reset()
		out.WriteString(notice)
		out.WriteString(content)
	}

	w.setMore(path, startLine+shown, raw, hasMore && setMore)
	return out.String(), nil
}

func (w *Workspace) readWhole(t pathTarget, raw bool) (string, error) {
	path := t.abs
	f, err := t.open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	// Read at most one byte past the limit off the open handle: this bounds
	// allocation and closes the stat/read race a growing file could exploit.
	data, err := io.ReadAll(io.LimitReader(f, w.cfg.MaxReadBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > w.cfg.MaxReadBytes {
		return "", fmt.Errorf("file is too large for whole read: exceeds MaxReadBytes (%d)", w.cfg.MaxReadBytes)
	}
	if isBinary(data) {
		return "", fmt.Errorf("refusing to read binary file: %s", path)
	}
	w.setMore(path, 0, raw, false)
	if raw {
		return string(data), nil
	}
	lines := strings.SplitAfter(string(data), "\n")
	var out strings.Builder
	fmt.Fprintf(&out, "%s: whole file, %d bytes\n", path, len(data))
	for i, line := range lines {
		if line == "" {
			continue
		}
		fmt.Fprintf(&out, "%d %s", i+1, strings.TrimRight(line, "\n"))
		out.WriteByte('\n')
		if int64(out.Len()) > w.cfg.MaxReadBytes {
			return "", fmt.Errorf("read output exceeds MaxReadBytes")
		}
	}
	// Bound every render path, including a header-only render with no numbered
	// lines, where the in-loop check above never runs.
	if int64(out.Len()) > w.cfg.MaxReadBytes {
		return "", fmt.Errorf("read output exceeds MaxReadBytes")
	}
	return out.String(), nil
}

func (w *Workspace) setMore(path string, nextLine int, raw, valid bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !valid {
		w.more = moreState{}
		return
	}
	w.more = moreState{path: path, nextLine: nextLine, raw: raw, valid: true}
}
