//go:build windows

package models

import (
	"errors"
	"os"
	"strconv"

	"golang.org/x/sys/windows"
)

func lockFile(path string, nonBlocking bool) (*FileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK)
	if nonBlocking {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	// Lock 1 byte at offset 100.
	ol := windows.Overlapped{
		Offset:     100,
		OffsetHigh: 0,
	}
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ol); err != nil {
		f.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, ErrLocked
		}
		return nil, err
	}

	// Write current PID to the lock file.
	if err := f.Truncate(0); err != nil {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
		f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, 0); err != nil {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
		f.Close()
		return nil, err
	}
	pidStr := strconv.Itoa(os.Getpid())
	if _, err := f.Write([]byte(pidStr)); err != nil {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil {
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
		f.Close()
		return nil, err
	}

	return &FileLock{f: f}, nil
}

// Close releases the lock and closes the underlying file.
func (l *FileLock) Close() error {
	ol := windows.Overlapped{
		Offset:     100,
		OffsetHigh: 0,
	}
	unlockErr := windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, &ol)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// checkLock tries to lock the file non-blocking at offset 100.
// If it fails with ERROR_LOCK_VIOLATION, it returns true, nil (locked by someone else).
// If it succeeds, it unlocks immediately and returns false, nil (not locked).
func checkLock(f *os.File) (bool, error) {
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	ol := windows.Overlapped{
		Offset:     100,
		OffsetHigh: 0,
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &ol)
	if err == nil {
		// Unlock immediately.
		windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
		return false, nil
	}
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return true, nil
	}
	return false, err
}
