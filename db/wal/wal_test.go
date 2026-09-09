package wal

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNewWalCreatesDirectoryAndFirstSegment(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "nested", "wal")

	w, err := NewWal(walDir, WalOptions{})
	if err != nil {
		t.Fatalf("NewWal() error = %v", err)
	}
	closeTestWal(t, w)

	wantDir, err := filepath.Abs(walDir)
	if err != nil {
		t.Fatal(err)
	}
	if w.dirpath != wantDir {
		t.Fatalf("wal directory = %q, want %q", w.dirpath, wantDir)
	}
	if len(w.segments) != 0 {
		t.Fatalf("closed segments = %d, want 0", len(w.segments))
	}
	if w.openSegment == nil {
		t.Fatal("open segment = nil")
	}
	if w.openSegment.idx != 1 {
		t.Fatalf("open segment index = %d, want 1", w.openSegment.idx)
	}
	if w.openSegment.File == nil {
		t.Fatal("open segment file = nil")
	}

	segmentPath := filepath.Join(wantDir, NewWalFileName(1))
	info, err := os.Stat(segmentPath)
	if err != nil {
		t.Fatalf("stat first segment: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("first segment mode = %v, want regular file", info.Mode())
	}
	if w.openSegment.path != segmentPath {
		t.Fatalf("open segment path = %q, want %q", w.openSegment.path, segmentPath)
	}
}

func TestNewWalDiscoversOnlyValidSegmentsAndAllocatesNextIndex(t *testing.T) {
	walDir := t.TempDir()
	for _, name := range []string{
		NewWalFileName(9),
		NewWalFileName(3),
		"not-a-wal",
		"other__100",
		NewWalFileName(0),
		NewWalFileName(10),
		NewWalFileName(7) + "/nested",
		"wal_file__not-a-number",
	} {
		filePath := filepath.Join(walDir, name)
		if filepath.Base(name) != name {
			if err := os.MkdirAll(filePath, 0o700); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filePath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	w, err := NewWal(walDir, WalOptions{})
	if err != nil {
		t.Fatalf("NewWal() error = %v", err)
	}
	closeTestWal(t, w)

	gotIndexes := make([]uint64, len(w.segments))
	for i, segment := range w.segments {
		gotIndexes[i] = segment.idx
		if segment.File != nil {
			t.Fatalf("closed segment %d has an open file", segment.idx)
		}
	}
	if want := []int{0, 3, 9, 10}; !reflect.DeepEqual(gotIndexes, want) {
		t.Fatalf("discovered segment indexes = %v, want %v", gotIndexes, want)
	}
	if w.openSegment.idx != 11 {
		t.Fatalf("open segment index = %d, want 11", w.openSegment.idx)
	}
	if _, err := os.Stat(filepath.Join(walDir, NewWalFileName(11))); err != nil {
		t.Fatalf("stat next segment: %v", err)
	}
}

func TestNewWalExclusivelyLocksDirectory(t *testing.T) {
	walDir := t.TempDir()

	first, err := NewWal(walDir, WalOptions{})
	if err != nil {
		t.Fatalf("first NewWal() error = %v", err)
	}
	defer func() {
		if first != nil {
			if err := first.Close(); err != nil {
				t.Errorf("first Close() error = %v", err)
			}
		}
	}()

	if _, err := NewWal(walDir, WalOptions{}); err == nil {
		t.Fatal("second NewWal() error = nil, want an exclusive-lock error")
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	first = nil
	second, err := NewWal(walDir, WalOptions{})
	if err != nil {
		t.Fatalf("NewWal() after Close() error = %v", err)
	}
	closeTestWal(t, second)
}

func TestNewWalRejectsFilePath(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewWal(filePath, WalOptions{}); err == nil {
		t.Fatal("NewWal() error = nil, want an error for a file path")
	}
}

func closeTestWal(t *testing.T, w *Wal) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

}
