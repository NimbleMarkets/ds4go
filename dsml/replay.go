package dsml

import (
	"container/list"
	"fmt"
	"sync"
)

// ReplayStore keeps an exact sampled DSML invoke block for tool-call IDs so
// later prompt renders can replay the original bytes instead of canonicalizing
// JSON back into DSML.
type ReplayStore struct {
	mu      sync.RWMutex
	maxIDs  int
	entries map[string]*list.Element
	order   *list.List
}

type replayEntry struct {
	id    string
	exact string
}

// NewReplayStore creates a bounded in-memory exact-DSML replay map.
func NewReplayStore(maxIDs int) *ReplayStore {
	if maxIDs <= 0 {
		maxIDs = 100000
	}
	return &ReplayStore{
		maxIDs:  maxIDs,
		entries: make(map[string]*list.Element),
		order:   list.New(),
	}
}

// Remember stores one tool-call ID to exact sampled DSML invoke block mapping.
func (s *ReplayStore) Remember(id, exact string) error {
	if s == nil || id == "" || exact == "" {
		return nil
	}
	if exact[0] != '<' || len(exact) < len(invokeStartToken) || exact[:len(invokeStartToken)] != invokeStartToken {
		return fmt.Errorf("dsml: exact replay block for %q is not an invoke block", id)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if elem, ok := s.entries[id]; ok {
		elem.Value.(*replayEntry).exact = exact
		s.order.MoveToBack(elem)
		return nil
	}
	elem := s.order.PushBack(&replayEntry{id: id, exact: exact})
	s.entries[id] = elem
	for len(s.entries) > s.maxIDs {
		oldest := s.order.Front()
		if oldest == nil {
			break
		}
		s.order.Remove(oldest)
		delete(s.entries, oldest.Value.(*replayEntry).id)
	}
	return nil
}

// Lookup returns the exact sampled DSML invoke block for id when available.
func (s *ReplayStore) Lookup(id string) (string, bool) {
	if s == nil || id == "" {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	elem, ok := s.entries[id]
	if !ok {
		return "", false
	}
	return elem.Value.(*replayEntry).exact, true
}
