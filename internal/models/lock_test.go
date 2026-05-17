package models

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestTryLockPreventsConcurrentAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf.lock")

	first, err := tryLock(path)
	if err != nil {
		t.Fatalf("first tryLock: %v", err)
	}

	if _, err := tryLock(path); !errors.Is(err, errLocked) {
		t.Fatalf("second tryLock err = %v, want errLocked", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Once released, the lock is acquirable again.
	again, err := tryLock(path)
	if err != nil {
		t.Fatalf("tryLock after release: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("Close after re-acquire: %v", err)
	}
}
