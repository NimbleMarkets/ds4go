package ds4

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestExtractPIDs(t *testing.T) {
	got := extractPIDs("model lock held by PID 1234; process 5678 is active")
	if len(got) != 2 || got[0] != 1234 || got[1] != 5678 {
		t.Fatalf("extractPIDs() = %#v", got)
	}
}

func TestEnrichEngineOpenError(t *testing.T) {
	old := processNameForPID
	processNameForPID = func(pid int) (string, error) {
		if pid != 1234 {
			return "", fmt.Errorf("unexpected pid %d", pid)
		}
		return "ds4-server", nil
	}
	defer func() { processNameForPID = old }()

	err := EnrichEngineOpenError(errors.New("ds4_engine_open: lock held by pid 1234"))
	if !strings.Contains(err.Error(), "pid 1234: ds4-server") {
		t.Fatalf("enriched error = %q", err)
	}
}

func TestIsVisionEncoderMissing(t *testing.T) {
	if !IsVisionEncoderMissing(errors.New("ds4_session_sync_multimodal: vision encoder is not loaded")) {
		t.Error("libds4's message was not classified")
	}
	if !IsVisionEncoderMissing(fmt.Errorf("wrapped: %w", errors.New("vision encoder is not loaded"))) {
		t.Error("wrapped message was not classified")
	}
	if !IsVisionEncoderMissing(errors.New("ds4_session_sync_multimodal: model does not support image messages")) {
		t.Error("libds4's other image refusal was not classified")
	}
	if !IsVisionEncoderMissing(fmt.Errorf("wrapped: %w", errors.New("model does not support image messages"))) {
		t.Error("wrapped second message was not classified")
	}
	if IsVisionEncoderMissing(errors.New("decode requires a synchronized checkpoint")) || IsVisionEncoderMissing(nil) {
		t.Error("unrelated error classified as a missing encoder")
	}
}

// Enriching an already-enriched error must not append a second details line:
// the CLI enriches after root NewEngine, which has enriched already.
func TestEnrichEngineOpenErrorIsIdempotent(t *testing.T) {
	old := processNameForPID
	processNameForPID = func(int) (string, error) { return "ds4-server", nil }
	defer func() { processNameForPID = old }()

	once := EnrichEngineOpenError(errors.New("ds4_engine_open: lock held by pid 1234"))
	twice := EnrichEngineOpenError(once)
	if twice.Error() != once.Error() {
		t.Errorf("second enrichment changed the error:\nonce:  %q\ntwice: %q", once, twice)
	}
}
