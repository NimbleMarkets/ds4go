package openaiserver

import (
	"context"
	"errors"
	"github.com/NimbleMarkets/ds4go"
	"runtime"
	"sync"
	"time"

	"github.com/NimbleMarkets/ds4go/ds4api"
)

type decodeRequest struct {
	ctx  context.Context
	item ds4api.DecodeItem
	done chan error
}

// decodeScheduler batches ready, independent sessions. Prefill is performed in
// bounded chunks by each request; the binding serializes all native calls.
// Once submitted, a caller waits for acknowledgement even after cancellation,
// so its session cannot be freed or reassigned while native code still uses it.
type decodeScheduler struct {
	engine   *ds4api.Engine
	mu       sync.Mutex
	pending  []*decodeRequest
	running  bool
	maxBatch int
}

func (d *decodeScheduler) eval(ctx context.Context, session *ds4api.Session, token int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	req := &decodeRequest{ctx: ctx, item: ds4api.DecodeItem{Session: session, Token: token}, done: make(chan error, 1)}
	d.mu.Lock()
	d.pending = append(d.pending, req)
	if !d.running {
		d.running = true
		go d.run()
	}
	d.mu.Unlock()
	return <-req.done
}
func (d *decodeScheduler) run() {
	for {
		// A short coalescing window lets concurrently ready decoders join the batch.
		time.Sleep(50 * time.Microsecond)
		d.mu.Lock()
		if len(d.pending) == 0 {
			d.running = false
			d.mu.Unlock()
			return
		}
		n := min(len(d.pending), d.maxBatch)
		batch := append([]*decodeRequest(nil), d.pending[:n]...)
		d.pending = d.pending[n:]
		d.mu.Unlock()
		live := make([]*decodeRequest, 0, len(batch))
		items := make([]ds4api.DecodeItem, 0, len(batch))
		for _, req := range batch {
			if err := req.ctx.Err(); err != nil {
				req.done <- err
			} else {
				live = append(live, req)
				items = append(items, req.item)
			}
		}
		if len(items) == 0 {
			continue
		}
		err := d.engine.EvalBatch(items)
		for _, req := range live {
			req.done <- err
		}
	}
}

// Bound text-prefill work so other sessions can decode between chunks. Image
// prompts retain the engine's multimodal sync path so no image span is split.
func (s *server) syncPrompt(ctx context.Context, session *ds4api.Session, prompt *ds4.Prompt) error {
	cancel := func() bool { return ctx.Err() != nil }
	if s.batcher == nil || len(prompt.Images) > 0 {
		err := session.SyncMultimodalWithCancel(prompt.Tokens, prompt.Images, cancel)
		if errors.Is(err, ds4api.ErrCancelNotSupported) {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return session.SyncMultimodal(prompt.Tokens, prompt.Images)
		}
		if errors.Is(err, ds4api.ErrSessionSyncInterrupted) && ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	tokens := prompt.Tokens.Slice()
	start := session.CommonPrefix(prompt.Tokens)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		end := min(len(tokens), start+128)
		err := session.SyncWithCancel(tokens[:end], cancel)
		if errors.Is(err, ds4api.ErrCancelNotSupported) {
			err = session.Sync(tokens[:end])
		}
		if errors.Is(err, ds4api.ErrSessionSyncInterrupted) && ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if end == len(tokens) {
			return nil
		}
		start = end
		runtime.Gosched()
	}
}
