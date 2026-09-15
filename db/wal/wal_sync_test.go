package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPeriodicSyncMakesSubLimitWritesAvailableFromTheActiveSegment(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{
		maxFileSizeMB: 1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer closeWal(t, w)

	data := []byte("periodically synced")
	if err := w.Write(OpSet, data); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(filepath.Join(walDir, NewWalFileName(1)))
	if err != nil {
		t.Fatal(err)
	}
	entries := decodeSegmentEntries(t, contents)
	if len(entries) != 1 || !bytes.Equal(entries[0].Data, data) {
		t.Fatalf("active segment entries = %+v, want one entry containing %q", entries, data)
	}
}

func decodeSegmentEntries(t *testing.T, data []byte) []WalEntry {
	t.Helper()

	var entries []WalEntry
	for len(data) > 0 {
		if len(data) < walEntryHeaderSize {
			t.Fatalf("segment ends with a partial header")
		}
		recordLen := walEntryHeaderSize + int(binaryBigEndianUint32(data[14:18]))
		if recordLen > len(data) {
			t.Fatalf("segment ends with a partial record")
		}
		entry, err := DecodeWalEntry(data[:recordLen])
		if err != nil {
			t.Fatalf("decode segment entry: %v", err)
		}
		entries = append(entries, entry)
		data = data[recordLen:]
	}
	return entries
}
