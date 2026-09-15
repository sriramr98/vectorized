package core

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/utils"
)

func TestDeleteMissingKeyReturnsNotDeletedWithoutWritingWAL(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(db.NewMemoryStore(), w)

	deleted, err := engine.Delete([]byte("missing"))
	if err != nil {
		t.Fatalf("Delete() error = %v, want nil", err)
	}
	if deleted {
		t.Fatal("Delete() deleted = true, want false")
	}
	if len(w.writes) != 0 {
		t.Fatalf("WAL writes = %d, want 0", len(w.writes))
	}
}

func TestRecoverRejectsSetWithExtraArguments(t *testing.T) {
	store := db.NewMemoryStore()
	if err := store.Set([]byte("existing"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN:    1,
		OpType: wal.OpSet,
		Data:   encodeEngineTestArgs(t, []byte("key"), []byte("value"), []byte("extra")),
	}}}
	engine := NewEngine(store, w)

	if _, err := engine.Recover(); err == nil {
		t.Fatal("Recover() error = nil, want SET arity error")
	}
	assertEngineValue(t, engine, "existing", "value")
}

func TestRecoverDoesNotPublishPartiallyRebuiltStore(t *testing.T) {
	store := db.NewMemoryStore()
	if err := store.Set([]byte("existing"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{
		{LSN: 1, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("new"), []byte("new-value"))},
		{LSN: 2, OpType: wal.OpSet, Data: []byte("malformed")},
	}}
	engine := NewEngine(store, w)

	if _, err := engine.Recover(); err == nil {
		t.Fatal("Recover() error = nil, want malformed-record error")
	}
	assertEngineValue(t, engine, "existing", "value")
	if _, err := engine.Get([]byte("new")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(new) error = %v, want %v", err, ErrKeyNotFound)
	}
}

func TestRecoverPublishesCompletelyRebuiltStore(t *testing.T) {
	store := db.NewMemoryStore()
	if err := store.Set([]byte("stale"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN:    1,
		OpType: wal.OpSet,
		Data:   encodeEngineTestArgs(t, []byte("recovered"), []byte("value")),
	}}}
	engine := NewEngine(store, w)

	count, err := engine.Recover()
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	if count != 1 {
		t.Fatalf("Recover() count = %d, want 1", count)
	}
	assertEngineValue(t, engine, "recovered", "value")
	if _, err := engine.Get([]byte("stale")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(stale) error = %v, want %v", err, ErrKeyNotFound)
	}
}

func TestSetResetsReusedEncodingBuffer(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(db.NewMemoryStore(), w)

	if err := engine.Set([]byte("first"), []byte("one")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Set([]byte("second"), []byte("two")); err != nil {
		t.Fatal(err)
	}

	want := [][][]byte{
		{[]byte("first"), []byte("one")},
		{[]byte("second"), []byte("two")},
	}
	if len(w.writes) != len(want) {
		t.Fatalf("WAL writes = %d, want %d", len(w.writes), len(want))
	}
	for i := range want {
		got, err := utils.DecodeLengthEncodedBytes(w.writes[i].data)
		if err != nil {
			t.Fatalf("decode WAL write %d: %v", i, err)
		}
		if len(got) != 2 || !bytes.Equal(got[0], want[i][0]) || !bytes.Equal(got[1], want[i][1]) {
			t.Fatalf("WAL write %d arguments = %q, want %q", i, got, want[i])
		}
	}
}

func assertEngineValue(t *testing.T, engine *Engine, key, want string) {
	t.Helper()
	got, err := engine.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q) error = %v", key, err)
	}
	if string(got) != want {
		t.Fatalf("Get(%q) = %q, want %q", key, got, want)
	}
}

func encodeEngineTestArgs(t *testing.T, args ...[]byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := utils.LengthEncodeBytes(args, &encoded); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

type engineTestWalWrite struct {
	op   wal.OpType
	data []byte
}

type engineTestWal struct {
	writes        []engineTestWalWrite
	replayEntries []wal.WalEntry
}

func (w *engineTestWal) Write(op wal.OpType, data []byte) error {
	w.writes = append(w.writes, engineTestWalWrite{op: op, data: bytes.Clone(data)})
	return nil
}

func (w *engineTestWal) Replay(fn func(wal.WalEntry) error) (uint64, error) {
	for i, entry := range w.replayEntries {
		if err := fn(entry); err != nil {
			return uint64(i), err
		}
	}
	return uint64(len(w.replayEntries)), nil
}

func (w *engineTestWal) Close() error { return nil }
