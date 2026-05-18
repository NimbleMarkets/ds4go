//go:build windows

package models

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock acquires a non-blocking exclusive lock on path, creating the file
// if needed. It returns errLocked if another process holds the lock.
func tryLock(path string) (*fileLock, error) {
	return lockFile(path, true)
}

func lockFile(path string, nonBlocking bool) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonBlocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &windows.Overlapped{}); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errLocked
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// Close releases the lock and closes the underlying file.
func (l *fileLock) Close() error {
	unlockErr := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &windows.Overlapped{})
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
