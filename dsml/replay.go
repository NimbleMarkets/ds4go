package dsml

import (
	"container/list"
	"fmt"
	"strings"
	"sync"
)

// ReplayStore keeps an exact sampled DSML tool_calls block for tool-call IDs so
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

// Remember stores one tool-call ID to exact sampled DSML tool_calls block mapping.
func (s *ReplayStore) Remember(id, exact string) error {
	if s == nil || id == "" || exact == "" {
		return nil
	}
	trimmed := strings.TrimSpace(exact)
	if !strings.HasPrefix(trimmed, "<"+dsmlMarker+toolCallsBlockName+">") &&
		!strings.HasPrefix(trimmed, "<"+dsmlMarkerShort+toolCallsBlockName+">") &&
		!strings.HasPrefix(trimmed, "<tool_calls>") {
		return fmt.Errorf("dsml: exact replay block for %q is not a tool_calls block", id)
	}
	if !strings.Contains(trimmed, toolCallsEndToken) &&
		!strings.Contains(trimmed, "</"+dsmlMarkerShort+toolCallsBlockName+">") &&
		!strings.Contains(trimmed, "</tool_calls>") {
		return fmt.Errorf("dsml: exact replay block for %q is unterminated", id)
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

// Lookup returns the exact sampled DSML tool_calls block for id when available.
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

// LookupBlock returns a replay block when every id exists and maps to the same
// exact sampled DSML tool_calls block.
func (s *ReplayStore) LookupBlock(ids []string) (string, bool) {
	if s == nil || len(ids) == 0 {
		return "", false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var exact string
	for _, id := range ids {
		if id == "" {
			return "", false
		}
		elem, ok := s.entries[id]
		if !ok {
			return "", false
		}
		current := elem.Value.(*replayEntry).exact
		if exact == "" {
			exact = current
		} else if exact != current {
			return "", false
		}
	}
	return exact, exact != ""
}
