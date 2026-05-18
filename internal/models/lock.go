package models

import (
	"errors"
	"os"
)

// errLocked is returned by tryLock when another process already holds the
// lock for a model download.
var errLocked = errors.New("a download for this model is already in progress")

// fileLock is an advisory, OS-level exclusive lock. The kernel releases it
// automatically when the holding process exits, so an interrupted download
// never leaves a stale lock behind.
type fileLock struct {
	f *os.File
}

// lockExclusive acquires an exclusive lock on path, creating the file if
// needed. Unlike tryLock, it waits for the existing holder to release it.
func lockExclusive(path string) (*fileLock, error) {
	return lockFile(path, false)
}
