package wal

import (
	"bytes"
	"context"
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
	w, err := NewWalWithOpts(context.TODO(), walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	first := bytes.Repeat([]byte("a"), 700*1024)
	second := bytes.Repeat([]byte("b"), 700*1024)
	if err := w.Write(OpSet, first); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(OpDelete, second); err != nil {
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
	w, err := NewWalWithOpts(context.TODO(), walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB}, nil)
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
		if err := w.Write(OpSet, data); err != nil {
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
	w, err := NewWalWithOpts(context.TODO(), t.TempDir(), WalOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(OpSet, []byte("after close")); !errors.Is(err, ErrAlreadyClosed) {
		t.Fatalf("Write after Close() error = %v, want %v", err, ErrAlreadyClosed)
	}
}

func TestWriteAssignsStrictlyIncreasingLSNs(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(context.TODO(), walDir, DefaultWalOpts, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	for _, data := range [][]byte{[]byte("one"), []byte("two"), []byte("three")} {
		if err := w.Write(OpSet, data); err != nil {
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

func TestReplayReturnsEntriesInSegmentAndLSNOrder(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(context.TODO(), walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	data := [][]byte{
		bytes.Repeat([]byte("a"), 700*1024),
		bytes.Repeat([]byte("b"), 700*1024),
	}
	defer w.Close()
	for _, d := range data {
		if err := w.Write(OpSet, d); err != nil {
			t.Fatal(err)
		}
	}

	var got [][]byte
	n, err := w.Replay(func(entry WalEntry) error {
		got = append(got, entry.Data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != uint64(len(data)) {
		t.Fatalf("expected wal to replay %d records but got %d", len(data), n)
	}
	want := [][]byte{bytes.Repeat([]byte("a"), 700*1024), bytes.Repeat([]byte("b"), 700*1024)}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("Replay returned records out of order or with changed data")
	}
}

func TestReplayTruncatesIncompleteFinalRecord(t *testing.T) {
	tests := []struct {
		name   string
		suffix func(t *testing.T) []byte
	}{
		{
			name: "partial header",
			suffix: func(t *testing.T) []byte {
				return []byte{0xde, 0xad, 0xbe}
			},
		},
		{
			name: "partial body",
			suffix: func(t *testing.T) []byte {
				t.Helper()
				var encoded bytes.Buffer
				entry := WalEntry{LSN: 2, OpType: OpSet, Data: []byte("incomplete")}
				if _, err := entry.Encode(&encoded); err != nil {
					t.Fatal(err)
				}
				return encoded.Bytes()[:encoded.Len()-2]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			walDir := t.TempDir()
			w, err := NewWalWithOpts(context.TODO(), walDir, DefaultWalOpts, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer closeWal(t, w)

			if err := w.Write(OpSet, []byte("complete")); err != nil {
				t.Fatal(err)
			}

			path := filepath.Join(walDir, NewWalFileName(1))
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := file.Write(tt.suffix(t)); err != nil {
				_ = file.Close()
				t.Fatal(err)
			}
			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			var got []WalEntry
			n, err := w.Replay(func(entry WalEntry) error {
				got = append(got, entry)
				return nil
			})
			if err != nil {
				t.Fatalf("Replay() error = %v, want torn final record ignored", err)
			}
			if n != 1 || len(got) != 1 || string(got[0].Data) != "complete" {
				t.Fatalf("Replay() returned %d entries, want only the complete record", n)
			}

			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Size() != before.Size() {
				t.Fatalf("segment size after recovery = %d, want %d", after.Size(), before.Size())
			}
		})
	}
}

func TestReplayRejectsIncompleteRecordBeforeFinalSegment(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(context.TODO(), walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(OpSet, bytes.Repeat([]byte("a"), 700*1024)); err != nil {
		t.Fatal(err)
	}
	if err := w.Write(OpSet, bytes.Repeat([]byte("b"), 700*1024)); err != nil {
		t.Fatal(err)
	}
	closeWal(t, w)

	path := filepath.Join(walDir, NewWalFileName(1))
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0xde, 0xad, 0xbe}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := NewWalWithOpts(context.TODO(), walDir, WalOptions{maxFileSizeMB: testSegmentSizeMB}, nil); err == nil {
		t.Fatal("NewWalWithOpts() error = nil, want incomplete non-final record error")
	}
}

func TestNewWalRecoversTornTailInLastExistingSegment(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(context.TODO(), walDir, DefaultWalOpts, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(OpSet, []byte("complete")); err != nil {
		t.Fatal(err)
	}
	closeWal(t, w)

	path := filepath.Join(walDir, NewWalFileName(1))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte{0xde, 0xad, 0xbe}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewWalWithOpts(context.TODO(), walDir, DefaultWalOpts, nil)
	if err != nil {
		t.Fatalf("NewWalWithOpts() error = %v, want torn tail recovered", err)
	}
	defer closeWal(t, reopened)
	if reopened.currentLSN != 1 {
		t.Fatalf("current LSN after recovery = %d, want 1", reopened.currentLSN)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("segment size after startup recovery = %d, want %d", after.Size(), before.Size())
	}
}

func TestReplayRejectsOversizedRecordBeforeAllocatingBody(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(context.TODO(), walDir, DefaultWalOpts, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, w)

	header := make([]byte, walEntryHeaderSize)
	header[4] = byte(walEntryVersionV1)
	binaryBigEndianPutUint32(header[14:18], MaxWalRecordDataBytes+1)
	path := filepath.Join(walDir, NewWalFileName(1))
	if err := os.WriteFile(path, header, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = w.Replay(func(WalEntry) error { return nil })
	if !errors.Is(err, ErrWalRecordTooLarge) {
		t.Fatalf("Replay() error = %v, want %v", err, ErrWalRecordTooLarge)
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

func binaryBigEndianPutUint32(data []byte, value uint32) {
	data[0] = byte(value >> 24)
	data[1] = byte(value >> 16)
	data[2] = byte(value >> 8)
	data[3] = byte(value)
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

func closeWal(t *testing.T, w *DurableWal) {
	t.Helper()
	if err := w.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}
