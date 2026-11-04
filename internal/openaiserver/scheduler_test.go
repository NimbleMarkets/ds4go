package openaiserver

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
	"github.com/NimbleMarkets/ds4go/internal/cliopts"
)

func TestSchedulerAdvancesIndependentSessions(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	scheduler := decodeScheduler{engine: engine, maxBatch: 4}
	sessions := make([]*ds4api.Session, 4)
	for i := range sessions {
		s, err := engine.NewSession(64)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.Sync([]int{i + 1, 2, 3}); err != nil {
			t.Fatal(err)
		}
		sessions[i] = s
	}
	var wg sync.WaitGroup
	for i, s := range sessions {
		wg.Add(1)
		go func(i int, s *ds4api.Session) {
			defer wg.Done()
			for n := 0; n < 8; n++ {
				if err := scheduler.eval(context.Background(), s, 10+i); err != nil {
					t.Error(err)
					return
				}
			}
		}(i, s)
	}
	wg.Wait()
	for i, s := range sessions {
		if s.Pos() != 11 {
			t.Errorf("session %d pos=%d", i, s.Pos())
		}
		for _, token := range s.Tokens().Slice()[3:] {
			if token != 10+i {
				t.Errorf("session %d got token %d", i, token)
			}
		}
	}
}
func TestSchedulerCancelledRequestDoesNotAdvance(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	session, err := engine.NewSession(64)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Sync([]int{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler := decodeScheduler{engine: engine, maxBatch: 2}
	if err := scheduler.eval(ctx, session, 4); err != context.Canceled {
		t.Fatalf("err=%v", err)
	}
	if session.Pos() != 3 {
		t.Fatal("cancelled request advanced")
	}
}
func TestResidentSessionsServeConcurrentRequestsAndClose(t *testing.T) {
	lib := ds4api.NewMockLibrary()
	engine, err := lib.NewEngine(ds4api.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	s := newHandler(engine, nil, &cliopts.ServerConfig{EngineFlags: cliopts.EngineFlags{Ctx: 512}, Tokens: 8, BatchedSession: 2})
	defer s.Close()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRecorder()
			s.ServeHTTP(r, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}],"temperature":0,"max_tokens":8,"reasoning_effort":"none"}`)))
			if r.Code != 200 {
				t.Errorf("%d %s", r.Code, r.Body.String())
			}
		}()
	}
	wg.Wait()
	if len(s.slots) != 2 {
		t.Fatalf("slots returned=%d", len(s.slots))
	}
	for i := 0; i < 2; i++ {
		session := <-s.slots
		if session == nil {
			t.Fatal("resident session missing")
		}
		s.slots <- session
	}
	done := make(chan struct{})
	go func() { s.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown blocked")
	}
	if err := s.generate(context.Background(), chatRequest{}, &result{}, nil); err != context.Canceled {
		t.Fatalf("request after close=%v", err)
	}
}
