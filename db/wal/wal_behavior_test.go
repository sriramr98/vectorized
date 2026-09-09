package wal

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const testSegmentSizeMB = 1

func TestWriteRotatesBeforeARecordWouldExceedTheSegmentLimit(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB})
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	first := bytes.Repeat([]byte("a"), 700*1024)
	second := bytes.Repeat([]byte("b"), 700*1024)
	if err := w.Write(first, OpSet); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(second, OpDelete); err != nil {
		t.Fatal(err)
	}

	if got := w.openSegment.idx; got != 2 {
		t.Fatalf("active segment = %d, want 2", got)
	}
	if got := len(w.segments); got != 1 {
		t.Fatalf("closed segments = %d, want 1", got)
	}

	entries := readSegmentEntries(t, filepath.Join(walDir, NewWalFileName(1)))
	if len(entries) != 1 || !bytes.Equal(entries[0].Data, first) {
		t.Fatalf("segment 1 entries = %d, want exactly the first record", len(entries))
	}
	entries = readSegmentEntries(t, filepath.Join(walDir, NewWalFileName(2)))
	if len(entries) != 1 || !bytes.Equal(entries[0].Data, second) {
		t.Fatalf("segment 2 does not contain exactly the second record")
	}
}

func TestRotationPreservesRecordOrderAndDoesNotSplitRecords(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB})
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	writes := [][]byte{
		bytes.Repeat([]byte("1"), 600*1024),
		bytes.Repeat([]byte("2"), 600*1024),
		bytes.Repeat([]byte("3"), 600*1024),
	}
	for _, data := range writes {
		if err := w.Write(data, OpSet); err != nil {
			t.Fatal(err)
		}
	}

	var got [][]byte
	for _, path := range walSegmentPaths(t, walDir) {
		for _, entry := range readSegmentEntries(t, path) {
			got = append(got, entry.Data)
		}
	}
	if len(got) != len(writes) {
		t.Fatalf("recovered record count = %d, want %d", len(got), len(writes))
	}
	for i := range writes {
		if !bytes.Equal(got[i], writes[i]) {
			t.Fatalf("record %d was reordered or changed", i)
		}
	}
}

func TestWriteAfterCloseIsRejected(t *testing.T) {
	w, err := NewWalWithOpts(t.TempDir(), WalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Write([]byte("after close"), OpSet); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("Write after Close() error = %v, want %v", err, ErrAlreadyClosed)
	}
}

func TestWriteAssignsStrictlyIncreasingLSNs(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, DefaultWalOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	for _, data := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		if err := w.Write(data, OpSet); err != nil {
			t.Fatal(err)
		}
	}

	// we need to sync to ensure page cache entries are flushed to disk
	if err := w.openSegment.Sync(); err != nil {
		t.Fatal(err)
	}

	fileName := w.openSegment.Name()

	entries := readSegmentEntries(t, fileName)
	if len(entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].LSN <= entries[i-1].LSN {
			t.Fatalf("LSNs = %d then %d, want strictly increasing", entries[i-1].LSN, entries[i].LSN)
		}
	}
}

// Replay is intentionally expressed as a small interface so this test file
// can be added before the concrete Replay signature is finalized. The
// expected contract is Replay(func(WalEntry) error) error.
func TestReplayReturnsEntriesInSegmentAndLSNOrder(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{
		bytes.Repeat([]byte("a"), 700*1024),
		bytes.Repeat([]byte("b"), 700*1024),
	} {
		if err := w.Write(data, OpSet); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	replayer, ok := any(w).(interface {
		Replay(func(WalEntry) error) error
	})
	if !ok {
		t.Skip("enable when Wal.Replay(func(WalEntry) error) error is implemented")
	}
	var got [][]byte
	if err := replayer.Replay(func(entry WalEntry) error {
		got = append(got, entry.Data)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := [][]byte{bytes.Repeat([]byte("a"), 700*1024), bytes.Repeat([]byte("b"), 700*1024)}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Replay returned records out of order or with changed data")
	}
}

func TestReplayIgnoresOrReportsOnlyTheIncompleteFinalRecord(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(walDir, WalOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write([]byte("complete"), OpSet); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(walDir, NewWalFileName(1))
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(contents, []byte{0xde, 0xad, 0xbe}...), 0o600); err != nil {
		t.Fatal(err)
	}

	replayer, ok := any(w).(interface {
		Replay(func(WalEntry) error) error
	})
	if !ok {
		t.Skip("enable when Wal.Replay(func(WalEntry) error) error is implemented")
	}
	var got []WalEntry
	err = replayer.Replay(func(entry WalEntry) error {
		got = append(got, entry)
		return nil
	})
	if err != nil && len(got) != 1 {
		t.Fatalf("Replay error = %v after %d valid entries, want valid prefix", err, len(got))
	}
	if len(got) != 1 || !bytes.Equal(got[0].Data, []byte("complete")) {
		t.Fatalf("Replay returned %d entries, want the complete prefix record", len(got))
	}
}

func readSegmentEntries(t *testing.T, path string) []WalEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var entries []WalEntry
	for len(data) > 0 {
		if len(data) < walEntryHeaderSize {
			t.Fatalf("segment %q ends with a partial header", path)
		}
		recordLen := walEntryHeaderSize + int(binaryBigEndianUint32(data[14:18]))
		if recordLen > len(data) {
			t.Fatalf("segment %q ends with a partial record", path)
		}
		entry, err := DecodeWalEntry(data[:recordLen])
		if err != nil {
			t.Fatalf("decode %q: %v", path, err)
		}
		entries = append(entries, entry)
		data = data[recordLen:]
	}
	return entries
}

func binaryBigEndianUint32(data []byte) uint32 {
	return uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
}

func walSegmentPaths(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range entries {
		if _, err := WalFileName(entry.Name()).Parse(); err == nil {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths
}

func closeWal(t *testing.T, w *Wal) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
