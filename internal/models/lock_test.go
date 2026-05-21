package models

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTryLockPreventsConcurrentAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf.lock")

	first, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}

	if _, err := TryLock(path); !errors.Is(err, ErrLocked) {
		t.Fatalf("second TryLock err = %v, want ErrLocked", err)
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Once released, the lock is acquirable again.
	again, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock after release: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatalf("Close after re-acquire: %v", err)
	}
}

func TestGetLockHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.gguf.lock")

	// 1. Non-existent file should return 0, nil
	pid, err := GetLockHolder(path)
	if err != nil {
		t.Fatalf("GetLockHolder for non-existent file: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}

	// 2. Lock the file
	lock, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock failed: %v", err)
	}
	defer lock.Close()

	// 3. Query lock holder - should be our own PID
	pid, err = GetLockHolder(path)
	if err != nil {
		t.Fatalf("GetLockHolder for locked file failed: %v", err)
	}
	if pid != os.Getpid() {
		t.Errorf("expected %d, got %d", os.Getpid(), pid)
	}

	// 4. Release the lock
	if err := lock.Close(); err != nil {
		t.Fatalf("failed to close lock: %v", err)
	}

	// 5. Query lock holder - should return 0, nil now
	pid, err = GetLockHolder(path)
	if err != nil {
		t.Fatalf("GetLockHolder after release failed: %v", err)
	}
	if pid != 0 {
		t.Errorf("expected 0, got %d", pid)
	}
}

func TestGetProcessName(t *testing.T) {
	name := GetProcessName(os.Getpid())
	if name == "" {
		t.Errorf("expected non-empty process name")
	}
}

func TestAcquireEngineRunLock(t *testing.T) {
	modelPath := filepath.Join(t.TempDir(), "model.gguf")

	// 1. First lock should succeed
	lock, err := AcquireEngineRunLock(modelPath)
	if err != nil {
		t.Fatalf("expected AcquireEngineRunLock to succeed, got: %v", err)
	}
	if lock == nil {
		t.Fatal("expected lock to be non-nil")
	}
	defer lock.Close()

	// 2. Second lock should fail with descriptive error
	_, err = AcquireEngineRunLock(modelPath)
	if err == nil {
		t.Fatal("expected second AcquireEngineRunLock to fail")
	}
	if !errors.Is(err, ErrLocked) && !strings.Contains(err.Error(), "engine lock held by process") {
		t.Errorf("expected error to mention engine lock holder, got: %v", err)
	}

	// 3. After closing, should succeed again
	lock.Close()
	lock2, err := AcquireEngineRunLock(modelPath)
	if err != nil {
		t.Fatalf("expected AcquireEngineRunLock to succeed after close, got: %v", err)
	}
	if lock2 == nil {
		t.Fatal("expected lock2 to be non-nil")
	}
	lock2.Close()
}

