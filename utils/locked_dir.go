package utils

import (
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
)

var (
	ErrResourceAlreadyLocked = errors.New("resource trying to lock is already locked")
	ErrResourceNotLocked     = errors.New("resource trying to get release is not locked")
	ErrResourceClosed        = errors.New("resource trying to open is already closed")
)

// LockedDir represents a directory in the fs that's been advisory locked.
type LockedDir struct {
	lockFile     *LockedFile
	dirpath      string
	lockFileName string
	locked       atomic.Bool
	mu           *sync.Mutex
}

func NewLockedDir(dirpath string, lockFileName string) (*LockedDir, error) {
	if err := EnsureDir(dirpath); err != nil {
		return nil, err
	}

	return &LockedDir{
		dirpath:      dirpath,
		lockFileName: lockFileName,
		locked:       atomic.Bool{},
		mu:           &sync.Mutex{},
	}, nil
}

// TryLock creates a <name>.lock file in the given folder and holds exclusive lock for that file. If lock is occupied, returns an error
func (l *LockedDir) TryLock() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.locked.Load() {
		return ErrResourceAlreadyLocked
	}
	lockFilePath := filepath.Join(l.dirpath, l.lockFileName)
	lockFile, err := NewLockedFile(lockFilePath)
	if err != nil {
		return err
	}

	if err := lockFile.TryLock(); err != nil {
		// I'm not sure what to do with this error. Returning this error will incorrectly inform caller that failure is on file close, not on lock acquisition.
		lockFile.Close()
		return err
	}

	l.locked.Store(true)
	l.lockFile = lockFile

	return nil
}

func (l *LockedDir) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.locked.Load() {
		return ErrResourceNotLocked
	}

	if err := l.lockFile.Close(); err != nil {
		return err
	}

	l.lockFile = nil
	l.locked.Store(false)
	return nil
}
