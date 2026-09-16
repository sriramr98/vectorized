package wal

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestReplayCreatesFirstSegmentForEmptyWal(t *testing.T) {
	walDir := filepath.Join(t.TempDir(), "nested", "wal")

	w, err := NewWal(nil, walDir)
	if err != nil {
		t.Fatalf("NewWal() error = %v", err)
	}
	if w.openSegment != nil {
		t.Fatal("active segment exists before startup recovery")
	}
	if _, err := w.Replay(func(WalEntry) error { return nil }); err != nil {
		t.Fatalf("Replay() error = %v", err)
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
	if w.openSegment != nil {
		t.Fatal("open segment should be nil after close")
	}

	segmentPath := filepath.Join(wantDir, NewWalFileName(1))
	info, err := os.Stat(segmentPath)
	if err != nil {
		t.Fatalf("stat first segment: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("first segment mode = %v, want regular file", info.Mode())
	}
}

func TestNewWalDiscoversOnlyValidSegmentsAndReusesLatestWithSpace(t *testing.T) {
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

	w, err := NewWalWithOpts(walDir, DefaultWalOpts, nil)
	if err != nil {
		t.Fatalf("NewWal() error = %v", err)
	}
	defer closeTestWal(t, w)
	if _, err := w.Replay(func(WalEntry) error { return nil }); err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	gotIndexes := make([]uint64, len(w.segments))
	for i, segment := range w.segments {
		gotIndexes[i] = segment.idx
		if segment.File != nil {
			t.Fatalf("closed segment %d has an open file", segment.idx)
		}
	}
	if want := []uint64{0, 3, 9}; slices.Compare(gotIndexes, want) != 0 {
		t.Fatalf("discovered segment indexes = %v, want %v", gotIndexes, want)
	}
	if w.openSegment.idx != 10 {
		t.Fatalf("open segment index = %d, want reused segment 10", w.openSegment.idx)
	}
	if w.openSegment.File == nil {
		t.Fatal("reused active segment does not have an open file")
	}
	if _, err := os.Stat(filepath.Join(walDir, NewWalFileName(11))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat unused next segment error = %v, want %v", err, os.ErrNotExist)
	}
}

func TestNewWalExclusivelyLocksDirectory(t *testing.T) {
	walDir := t.TempDir()

	first, err := NewWalWithOpts(walDir, DefaultWalOpts, nil)
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

	if _, err := NewWalWithOpts(walDir, DefaultWalOpts, nil); err == nil {
		t.Fatal("second NewWal() error = nil, want an exclusive-lock error")
	}

	if err := first.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	first = nil
	second, err := NewWalWithOpts(walDir, DefaultWalOpts, nil)
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

	if _, err := NewWalWithOpts(filePath, DefaultWalOpts, nil); err == nil {
		t.Fatal("NewWal() error = nil, want an error for a file path")
	}
}

func TestNewWalWithOptsRejectsZeroSegmentSize(t *testing.T) {
	_, err := NewWalWithOpts(t.TempDir(), WalOptions{}, nil)
	if !errors.Is(err, ErrInvalidOptions) {
		t.Fatalf("NewWalWithOpts() error = %v, want %v", err, ErrInvalidOptions)
	}
}

func TestCreateWalFileSyncsItsDirectory(t *testing.T) {
	walDir := t.TempDir()
	var syncedPath string
	segment, err := createWalFileWithSync(walDir, 1, func(path string) error {
		syncedPath = path
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := segment.Close(); err != nil {
			t.Errorf("close segment: %v", err)
		}
	}()
	if syncedPath != walDir {
		t.Fatalf("synced directory = %q, want %q", syncedPath, walDir)
	}
}

func TestCreateWalFileReturnsDirectorySyncError(t *testing.T) {
	wantErr := errors.New("directory sync failed")
	segment, err := createWalFileWithSync(t.TempDir(), 1, func(string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("createWalFileWithSync() error = %v, want %v", err, wantErr)
	}
	if segment != nil {
		t.Fatal("createWalFileWithSync() returned a segment after sync failure")
	}
}

func closeTestWal(t *testing.T, w *DurableWal) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

}
