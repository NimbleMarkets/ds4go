//go:build !windows

package models

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
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
	mode := unix.LOCK_EX
	if nonBlocking {
		mode |= unix.LOCK_NB
	}
	if err := unix.Flock(int(f.Fd()), mode); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, errLocked
		}
		return nil, err
	}
	return &fileLock{f: f}, nil
}

// Close releases the lock and closes the underlying file.
func (l *fileLock) Close() error {
	unlockErr := unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}
