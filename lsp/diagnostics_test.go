package lsp

import (
	"context"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func pubParams(uri string, version int32, msgs ...string) protocol.PublishDiagnosticsParams {
	ds := make([]protocol.Diagnostic, 0, len(msgs))
	for _, m := range msgs {
		ds = append(ds, protocol.Diagnostic{Severity: protocol.SeverityError, Message: m})
	}
	return protocol.PublishDiagnosticsParams{URI: protocol.DocumentURI(uri), Version: version, Diagnostics: ds}
}

func TestDiag_VersionCorrelatedWaitReturnsImmediately(t *testing.T) {
	b := newDiagBuffer(time.Second, 300*time.Millisecond)
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.publish(pubParams("file:///a.lua", 2, "boom"))
	}()
	got, err := b.wait(context.Background(), "file:///a.lua", 2, 2*time.Second)
	if err != nil {
		t.Fatalf("wait err: %v", err)
	}
	if len(got) != 1 || got[0].Message != "boom" {
		t.Fatalf("got %+v, want one 'boom'", got)
	}
}

func TestDiag_TimeSettleFallbackWhenNoVersion(t *testing.T) {
	b := newDiagBuffer(200*time.Millisecond, 100*time.Millisecond)
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.publish(pubParams("file:///a.lua", 0, "first"))
		time.Sleep(20 * time.Millisecond)
		b.publish(pubParams("file:///a.lua", 0, "first", "second")) // burst 2
	}()
	got, err := b.wait(context.Background(), "file:///a.lua", 9, time.Second)
	if err != nil {
		t.Fatalf("wait err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d diags, want 2 (settled after burst)", len(got))
	}
}

func TestDiag_NoPublishReturnsEmptyNoError(t *testing.T) {
	b := newDiagBuffer(80*time.Millisecond, 50*time.Millisecond)
	got, err := b.wait(context.Background(), "file:///a.lua", 1, time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d, want 0", len(got))
	}
}

// A versioned publish that arrives after firstWait must still be correlated:
// FirstWait bounds only the no-version fallback, not the version-correlated
// wait, which honors the full timeout (regression for the firstWait cap bug).
func TestDiag_VersionedCorrelationWaitsPastFirstWait(t *testing.T) {
	b := newDiagBuffer(50*time.Millisecond, 30*time.Millisecond)
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.publish(pubParams("file:///a.lua", 2, "old")) // older version, within firstWait
		time.Sleep(200 * time.Millisecond)              // well past firstWait
		b.publish(pubParams("file:///a.lua", 3, "current"))
	}()
	got, err := b.wait(context.Background(), "file:///a.lua", 3, 2*time.Second)
	if err != nil {
		t.Fatalf("wait err: %v", err)
	}
	if len(got) != 1 || got[0].Message != "current" {
		t.Fatalf("got %+v, want one 'current' (waited past firstWait, not the stale 'old')", got)
	}
}

// Publishes for an unrelated URI must not reset this URI's settle window or
// starve it (regression for the global-counter starvation bug).
func TestDiag_PerURISettleIgnoresOtherURIs(t *testing.T) {
	b := newDiagBuffer(100*time.Millisecond, 80*time.Millisecond)
	stop := make(chan struct{})
	go func() {
		time.Sleep(10 * time.Millisecond)
		b.publish(pubParams("file:///a.lua", 0, "a")) // unversioned -> settle path
		// Hammer a different URI; with a global counter this would keep
		// resetting a.lua's settle window and time it out.
		for {
			select {
			case <-stop:
				return
			default:
				b.publish(pubParams("file:///b.lua", 0, "noise"))
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()
	got, err := b.wait(context.Background(), "file:///a.lua", 9, time.Second)
	close(stop)
	if err != nil {
		t.Fatalf("wait err: %v (other-URI noise starved the waiter)", err)
	}
	if len(got) != 1 || got[0].Message != "a" {
		t.Fatalf("got %+v, want one 'a'", got)
	}
}

func TestDiag_SnapshotReturnsCopy(t *testing.T) {
	b := newDiagBuffer(0, 0)
	uri := "file:///a.lua"
	b.publish(pubParams(uri, 1, "x", "y"))
	s := b.snapshot(uri)
	if len(s) != 2 {
		t.Fatalf("len %d, want 2", len(s))
	}
	s[0].Message = "MUTATED" // must not affect stored state
	if again := b.snapshot(uri); again[0].Message != "x" {
		t.Fatalf("snapshot aliased internal slice: got %q", again[0].Message)
	}
}

func TestDiag_OnChangeCallbackFires(t *testing.T) {
	b := newDiagBuffer(0, 0)
	ch := make(chan int, 1)
	b.onChange(func(_ string, _ int32, diags []Diagnostic) { ch <- len(diags) })
	b.publish(pubParams("file:///a.lua", 1, "x"))
	select {
	case n := <-ch:
		if n != 1 {
			t.Fatalf("callback got %d, want 1", n)
		}
	case <-time.After(time.Second):
		t.Fatal("callback never fired")
	}
}

// The callback receives a canonical URI derived from the normalized path, not
// the raw wire URI, so a consumer can correlate it against the buffer regardless
// of how the server percent-encoded the URI it echoed back.
func TestDiag_OnChangeCallbackReceivesCanonicalURI(t *testing.T) {
	b := newDiagBuffer(0, 0)
	ch := make(chan string, 1)
	b.onChange(func(uri string, _ int32, _ []Diagnostic) { ch <- uri })
	b.publish(pubParams("file:///dir/a b.lua", 1, "x")) // server: decoded space
	select {
	case got := <-ch:
		// Canonical form re-encodes the space and resolves to the same key.
		if pathFromURI(got) != "/dir/a b.lua" {
			t.Fatalf("callback uri %q does not normalize to the buffer key", got)
		}
	case <-time.After(time.Second):
		t.Fatal("callback never fired")
	}
}

// Entries are keyed by normalized path, so a publish whose URI is percent-
// encoded differently than the lookup URI still resolves to the same entry.
func TestDiag_KeyNormalizesURIEncoding(t *testing.T) {
	b := newDiagBuffer(0, 0)
	b.publish(pubParams("file:///dir/a%20b.lua", 1, "boom")) // server: encoded space
	got := b.snapshot("file:///dir/a b.lua")                 // client: decoded space
	if len(got) != 1 || got[0].Message != "boom" {
		t.Fatalf("encoding mismatch lost diagnostics: %+v", got)
	}
}
