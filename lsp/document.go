package lsp

import "sync"

// docStore tracks the current version of each open document. The Client passes
// text and languageID straight to the RPC layer, so the store only needs to
// own version numbers.
//
// Keys are normalized via pathFromURI (percent-decoded filesystem path) for
// robustness against variant URI encodings (matching diagBuffer behavior).
type docStore struct {
	mu       sync.Mutex
	versions map[string]int
}

func newDocStore() *docStore {
	return &docStore{versions: make(map[string]int)}
}

// open records a document at version 1, resetting it if it already exists.
// Returns the resulting version.
func (s *docStore) open(uri string) int {
	key := pathFromURI(uri)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.versions[key] = 1
	return 1
}

// update bumps the version of an open document. Returns the new version, or 0
// if the document is not open.
func (s *docStore) update(uri string) int {
	key := pathFromURI(uri)
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[key]
	if !ok {
		return 0
	}
	v++
	s.versions[key] = v
	return v
}

// version returns the current version of an open document.
func (s *docStore) version(uri string) (int, bool) {
	key := pathFromURI(uri)
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.versions[key]
	return v, ok
}

func (s *docStore) close(uri string) {
	key := pathFromURI(uri)
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.versions, key)
}
