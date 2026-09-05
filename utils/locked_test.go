package utils

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLockedFileCreatesAndLocksFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.lock")

	lockedFile, err := NewLockedFile(path)
	if err != nil {
		t.Fatalf("NewLockedFile() error = %v", err)
	}
	if err := lockedFile.TryLock(); err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if !lockedFile.locked.Load() {
		t.Fatal("locked = false after successful TryLock(), want true")
	}
	if err := lockedFile.TryLock(); !errors.Is(err, ErrResourceAlreadyLocked) {
		t.Fatalf("second TryLock() error = %v, want %v", err, ErrResourceAlreadyLocked)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock file: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("lock file mode = %v, want regular file", info.Mode())
	}
	if err := lockedFile.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestLockedFileContentionAndReacquisition(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wal.lock")

	owner, err := NewLockedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err := owner.TryLock(); err != nil {
		t.Fatal(err)
	}

	contender, err := NewLockedFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if err := contender.TryLock(); err == nil {
		t.Fatal("contending TryLock() error = nil, want lock contention")
	}
	if contender.locked.Load() {
		t.Fatal("contender marked locked after failed TryLock()")
	}

	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if err := contender.TryLock(); err != nil {
		t.Fatalf("TryLock() after owner Close() error = %v", err)
	}
}

func TestLockedFileCloseWithoutLock(t *testing.T) {
	lockedFile, err := NewLockedFile(filepath.Join(t.TempDir(), "wal.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if err := lockedFile.Close(); err != nil {
		t.Fatalf("Close() on unlocked file error = %v", err)
	}
}

func TestNewLockedFileRejectsMissingParent(t *testing.T) {
	_, err := NewLockedFile(filepath.Join(t.TempDir(), "missing", "wal.lock"))
	if err == nil {
		t.Fatal("NewLockedFile() error = nil, want missing-parent error")
	}
}

func TestLockedDirLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	lockDir, err := NewLockedDir(dir, ".lock")
	if err != nil {
		t.Fatalf("NewLockedDir() error = %v", err)
	}
	if err := lockDir.Release(); !errors.Is(err, ErrResourceNotLocked) {
		t.Fatalf("Release() before TryLock() error = %v, want %v", err, ErrResourceNotLocked)
	}
	if err := lockDir.TryLock(); err != nil {
		t.Fatalf("TryLock() error = %v", err)
	}
	if err := lockDir.TryLock(); !errors.Is(err, ErrResourceAlreadyLocked) {
		t.Fatalf("second TryLock() error = %v, want %v", err, ErrResourceAlreadyLocked)
	}
	if err := lockDir.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if lockDir.locked.Load() || lockDir.lockFile != nil {
		t.Fatal("directory lock state was not cleared after Release()")
	}
	if err := lockDir.Release(); !errors.Is(err, ErrResourceNotLocked) {
		t.Fatalf("second Release() error = %v, want %v", err, ErrResourceNotLocked)
	}
}

func TestLockedDirContentionAndReacquisition(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wal")
	owner, err := NewLockedDir(dir, ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.TryLock(); err != nil {
		t.Fatal(err)
	}

	contender, err := NewLockedDir(dir, ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if err := contender.TryLock(); err == nil {
		t.Fatal("contending TryLock() error = nil, want lock contention")
	}
	if contender.lockFile != nil || contender.locked.Load() {
		t.Fatal("contender retained lock state after failed TryLock()")
	}

	if err := owner.Release(); err != nil {
		t.Fatal(err)
	}
	if err := contender.TryLock(); err != nil {
		t.Fatalf("TryLock() after owner Release() error = %v", err)
	}
	if err := contender.Release(); err != nil {
		t.Fatal(err)
	}
}
