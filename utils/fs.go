package utils

import (
	"errors"
	"os"
	"path/filepath"
)

const (
	PermFileReadWriteOwnerOnly = 0600
	PermDirFullPermOwnerOnly   = 0700
)

var (
	ErrResourceFileExpected = errors.New("expected to find a file but found a directory")
)

// OpenFile creates a file (or truncates if exists) with current owner perms only
func OpenFile(fpath string, flag int) (*os.File, error) {
	return os.OpenFile(fpath, flag, PermFileReadWriteOwnerOnly)
}

// checks if dir is present, else creates dir with owner only perms
func EnsureDir(dirpath string) error {
	// validate the dirpath
	if info, err := os.Stat(dirpath); err != nil {
		if os.IsNotExist(err) {
			// create dir with owner only perms so that other users cannot interfere.
			if err := os.MkdirAll(dirpath, PermDirFullPermOwnerOnly); err != nil {
				return err
			}
		} else {
			// some other error in checking for folder
			return err
		}
	} else if !info.IsDir() {
		// path is valid file but not a directory
		return errors.New("wal path is not a directory")
	}

	return nil
}

// EnsureFile takes a full file path and creates file if not present. File created will have owner permissions only
func EnsureFile(fp string, flags int) (*os.File, error) {
	fpath, err := filepath.Abs(fp)
	if err != nil {
		return nil, err
	}

	file, err := OpenFile(fpath, flags)
	if err != nil {
		return nil, err
	}

	return file, err
}
