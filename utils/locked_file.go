package utils

import (
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// LockedFile represents a file in the fs that's been locked for use
type LockedFile struct {
	*os.File

	locked atomic.Bool
	mu     *sync.Mutex
}

func NewLockedFile(fpath string) (*LockedFile, error) {
	file, err := EnsureFile(fpath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}

	return &LockedFile{
		File:   file,
		locked: atomic.Bool{},
		mu:     &sync.Mutex{},
	}, nil

}

func (l *LockedFile) TryLock() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.File == nil {
		return ErrResourceClosed
	}

	if l.locked.Load() {
		return ErrResourceAlreadyLocked
	}

	if err := unix.Flock(int(l.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	l.locked.Store(true)

	return nil
}

// Close will release the lock an close the file ( if lock existed )
func (l *LockedFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	// If file is locked, remove lock
	if l.locked.Load() {
		if err := unix.Flock(int(l.Fd()), unix.LOCK_UN); err != nil {
			//TODO: What do we do here? Should we close the file in this scenario????
			return err
		}
	}

	l.locked.Store(false)

	// always close file even if lock was never acquired
	var err error
	if l.File != nil {
		err = l.File.Close()
	}

	l.File = nil

	return err
}
