package models

import (
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
)

// ErrLocked is returned by TryLock when another process already holds the lock.
var ErrLocked = errors.New("lock already held by another process")

// FileLock is an advisory, OS-level exclusive lock. The kernel releases it
// automatically when the holding process exits, so an interrupted process
// never leaves a stale lock behind.
type FileLock struct {
	f *os.File
}

// LockExclusive acquires an exclusive lock on path, creating the file if
// needed. Unlike TryLock, it waits for the existing holder to release it.
func LockExclusive(path string) (*FileLock, error) {
	return lockFile(path, false)
}

// TryLock acquires a non-blocking exclusive lock on path, creating the file
// if needed. It returns ErrLocked if another process holds the lock.
func TryLock(path string) (*FileLock, error) {
	return lockFile(path, true)
}

// GetLockHolder returns the PID of the process holding the lock on path.
// If the lock is not currently held, it returns 0, nil.
func GetLockHolder(path string) (int, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return 0, nil
	}

	f, err := os.OpenFile(path, os.O_RDWR, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer f.Close()

	isLocked, err := checkLock(f)
	if err != nil {
		return 0, err
	}
	if !isLocked {
		return 0, nil
	}

	// Lock is held by someone, read their PID.
	if _, err := f.Seek(0, 0); err != nil {
		return 0, err
	}
	var buf [64]byte
	n, err := f.Read(buf[:])
	if err != nil && err != io.EOF {
		return 0, err
	}
	pidStr := strings.TrimSpace(string(buf[:n]))
	if pidStr == "" {
		return 0, nil
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, err
	}
	return pid, nil
}
