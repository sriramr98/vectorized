package wal

import (
	"bytes"
	"log/slog"
	"reflect"
	"slices"
	"testing"
)

func TestReplayReturnsEntriesAfterSegmentRotation(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(
		walDir,
		WalOptions{MaxFileSizeMB: 1},
		slog.Default(),
	)
	if err != nil {
		t.Fatal(err)
	}
	recoverEmptyWal(t, w)

	want := []WalEntry{
		{LSN: 1, OpType: OpSet, Data: bytes.Repeat([]byte("a"), 700*1024)},
		{LSN: 2, OpType: OpDelete, Data: bytes.Repeat([]byte("b"), 700*1024)},
		{LSN: 3, OpType: OpSet, Data: bytes.Repeat([]byte("c"), 700*1024)},
	}
	writes := slices.Clone(want)
	for _, entry := range writes {
		if err := w.Write(entry.OpType, entry.Data); err != nil {
			t.Fatal(err)
		}
	}

	if got := w.openSegment.idx; got != 3 {
		t.Fatalf("active segment = %d, want 3 after rotation", got)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewWalWithOpts(walDir, WalOptions{MaxFileSizeMB: 1}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer closeWal(t, reopened)

	var got []WalEntry
	n, err := reopened.Replay(func(entry WalEntry) error {
		got = append(got, entry)
		return nil
	})

	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}

	if n != uint64(len(want)) {
		t.Fatalf("expected %d records to be replayed but got %d", len(want), n)
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Replay() entries differ from writes: got %d entries, want %d", len(got), len(want))
	}
}
