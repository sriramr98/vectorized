package wal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPeriodicSyncMakesSubLimitWritesAvailableFromTheActiveSegment(t *testing.T) {
	const interval = 25 * time.Millisecond

	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{
		maxFileSizeMB: 1,
		syncInterval:  interval,
	})
	if err != nil {
		t.Fatal(err)
	}

	defer closeWal(t, w)

	data := []byte("periodically synced")
	if err := w.Write(data, OpSet); err != nil {
		t.Fatal(err)
	}

	// The record is smaller than the segment limit, so it remains in the
	// active segment. Give the periodic sync time to run, then verify that the
	// complete record can be read from disk.
	time.Sleep(2 * interval)

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
