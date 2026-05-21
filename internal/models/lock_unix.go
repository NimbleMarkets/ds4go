//go:build !windows

package models

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

func lockFile(path string, nonBlocking bool) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	mode := unix.LOCK_EX
	if nonBlocking {
		mode |= unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), mode); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}
		return nil, err
	}

	// Write current PID to the lock file.
	if err := f.Truncate(0); err != nil {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
		return nil, err
	}
	pidStr := strconv.Itoa(os.Getpid())
	if _, err := f.Write([]byte(pidStr)); err != nil {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
		return nil, err
	}

	return &FileLock{f: f}, nil
}

// Close releases the lock and closes the underlying file.
func (l *FileLock) Close() error {
	unlockErr := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// checkLock tries to lock the file non-blocking.
// If it fails with EWOULDBLOCK, it returns true, nil (locked by someone else).
// If it succeeds, it unlocks immediately and returns false, nil (not locked).
func checkLock(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		// Unlock immediately.
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		return false, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) {
		return true, nil
	}
	return false, err
}
