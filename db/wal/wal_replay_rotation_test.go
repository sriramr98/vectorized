package wal

import (
	"bytes"
	"log/slog"
	"reflect"
	"testing"
)

func TestReplayReturnsEntriesAfterSegmentRotation(t *testing.T) {
	walDir := t.TempDir()
	w, err := NewWalWithOpts(
		walDir,
		WalOptions{maxFileSizeMB: 1},
		slog.Default(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := w.Close(); err != nil {
			t.Errorf("close WAL: %v", err)
		}
	}()

	want := []WalEntry{
		{LSN: 1, OpType: OpSet, Data: bytes.Repeat([]byte("a"), 700*1024)},
		{LSN: 2, OpType: OpDelete, Data: bytes.Repeat([]byte("b"), 700*1024)},
	}
	for _, entry := range want {
		if err := w.Write(entry.OpType, entry.Data); err != nil {
			t.Fatal(err)
		}
	}

	if got := w.openSegment.idx; got != 2 {
		t.Fatalf("active segment = %d, want 2 after rotation", got)
	}

	var got []WalEntry
	n, err := w.Replay(func(entry WalEntry) error {
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
