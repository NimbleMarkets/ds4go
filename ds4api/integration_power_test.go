//go:build ds4_integration

// Run with: go test -tags ds4_integration ./ds4api/ -run TestRealLibraryPowerRoundTrip
// Requires DS4_LIB to point at a libds4 built at or after upstream commit 444afce
// and DS4_MODEL to point at a GGUF model file usable for engine open.

package ds4api

import (
	"os"
	"testing"
)

func TestRealLibraryPowerRoundTrip(t *testing.T) {
	libPath := os.Getenv("DS4_LIB")
	model := os.Getenv("DS4_MODEL")
	if libPath == "" || model == "" {
		t.Skip("DS4_LIB and DS4_MODEL must both be set")
	}
	lib, err := Load(libPath)
	if err != nil {
		t.Fatalf("Load(%q): %v", libPath, err)
	}
	eng, err := lib.NewEngine(EngineOptions{
		ModelPath:    model,
		Backend:      BackendMetal,
		PowerPercent: 37,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	if got := eng.Power(); got != 37 {
		t.Fatalf("Engine.Power() = %d, want 37 — cEngineOptions ABI is wrong", got)
	}
	if err := eng.SetPower(75); err != nil {
		t.Fatalf("SetPower(75): %v", err)
	}
	if got := eng.Power(); got != 75 {
		t.Fatalf("Engine.Power() after SetPower(75) = %d, want 75", got)
	}
}
