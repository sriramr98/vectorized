package core

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/utils"
)

func newBasicMemTable() db.MutableMemTable {
	return newDefaultBasicMemTable()
}

func newDefaultBasicMemTable() *db.BasicMemTable {
	return db.NewMemoryStore(db.DefaultMemStoreOpts())
}

func newMemTableWithMaxSize(maxSizeBytes uint64) db.MutableStoreCreator {
	return func() db.MutableMemTable {
		return db.NewMemoryStore(db.MemStoreOpts{MaxSizeBytes: maxSizeBytes})
	}
}

func TestRecoverRejectsSetWithExtraArguments(t *testing.T) {
	store := newDefaultBasicMemTable()
	if err := store.Apply(db.Record{
		Key:  "existing",
		Data: []byte("value"),
		Lsn:  1,
	}); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN:    1,
		OpType: wal.OpSet,
		Data:   encodeEngineTestArgs(t, []byte("key"), []byte("value"), []byte("extra")),
	}}}
	engine := NewEngine(func() db.MutableMemTable { return store }, w)

	if _, err := engine.Recover(); err == nil {
		t.Fatal("Recover() error = nil, want SET arity error")
	}
	assertEngineValue(t, engine, "existing", "value")
}

func TestRecoverDoesNotPublishPartiallyRebuiltStore(t *testing.T) {
	store := newDefaultBasicMemTable()
	if err := store.Apply(db.Record{
		Key:  "existing",
		Data: []byte("value"),
		Lsn:  1,
	}); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{
		{LSN: 1, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("new"), []byte("new-value"))},
		{LSN: 2, OpType: wal.OpSet, Data: []byte("malformed")},
	}}
	engine := NewEngine(func() db.MutableMemTable { return store }, w)

	if _, err := engine.Recover(); err == nil {
		t.Fatal("Recover() error = nil, want malformed-record error")
	}
	assertEngineValue(t, engine, "existing", "value")
	if _, err := engine.Get([]byte("new")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(new) error = %v, want %v", err, ErrKeyNotFound)
	}
}

func TestRecoverPublishesCompletelyRebuiltStore(t *testing.T) {
	store := newDefaultBasicMemTable()
	if err := store.Apply(db.Record{
		Key:  "stale",
		Data: []byte("value"),
		Lsn:  1,
	}); err != nil {
		t.Fatal(err)
	}
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN:    1,
		OpType: wal.OpSet,
		Data:   encodeEngineTestArgs(t, []byte("recovered"), []byte("value")),
	}}}
	engine := NewEngine(func() db.MutableMemTable { return newDefaultBasicMemTable() }, w)

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
	engine := NewEngine(newBasicMemTable, w)

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

func TestSetExistingKeyRemainsUnchangedWhenWALWriteFails(t *testing.T) {
	store := newDefaultBasicMemTable()
	if err := store.Apply(db.Record{
		Key:  "key",
		Data: []byte("old"),
		Lsn:  1,
	}); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("WAL write failed")
	engine := NewEngine(func() db.MutableMemTable { return store }, &engineTestWal{writeErr: wantErr})

	if err := engine.Set([]byte("key"), []byte("new")); !errors.Is(err, wantErr) {
		t.Fatalf("Set() error = %v, want %v", err, wantErr)
	}
	assertEngineValue(t, engine, "key", "old")
}

func TestSetNewKeyRemainsAbsentWhenWALWriteFails(t *testing.T) {
	wantErr := errors.New("WAL write failed")
	engine := NewEngine(newBasicMemTable, &engineTestWal{writeErr: wantErr})

	if err := engine.Set([]byte("key"), []byte("value")); !errors.Is(err, wantErr) {
		t.Fatalf("Set() error = %v, want %v", err, wantErr)
	}
	if _, err := engine.Get([]byte("key")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(key) error = %v, want %v", err, ErrKeyNotFound)
	}
}

func TestSetWritesWALBeforeApplyingMemory(t *testing.T) {
	var events []string
	store := newDefaultBasicMemTable()
	w := &orderedEngineTestWal{
		events: &events,
		wasApplied: func() bool {
			_, found := store.Get("key")
			return found
		},
	}
	engine := NewEngine(func() db.MutableMemTable { return store }, w)

	if err := engine.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if w.appliedBeforeWAL {
		t.Fatal("memory mutation happened before WAL write")
	}
	if want := []string{"wal"}; !slices.Equal(events, want) {
		t.Fatalf("WAL events = %v, want %v", events, want)
	}
	if _, found := store.Get("key"); !found {
		t.Fatal("memory mutation did not happen after WAL write")
	}
}

func TestSetReportsMemoryFailureAfterWALWrite(t *testing.T) {
	w := &engineTestWal{}
	store := newDefaultBasicMemTable()
	store.Freeze()
	engine := NewEngine(func() db.MutableMemTable { return store }, w)

	if err := engine.Set([]byte("key"), []byte("value")); err == nil {
		t.Fatal("Set() error = nil, want memory-application error")
	}
	if len(w.writes) != 1 {
		t.Fatalf("WAL writes = %d, want 1 committed write", len(w.writes))
	}
	if _, found := store.Get("key"); found {
		t.Fatal("memory contains key after failed application")
	}
}

func TestSetRotatesFullMutableTableAndReadsFrozenTable(t *testing.T) {
	engine := NewEngine(newMemTableWithMaxSize(11), &engineTestWal{})

	if err := engine.Set([]byte("a"), []byte("1")); err != nil {
		t.Fatalf("first Set() error = %v", err)
	}
	if err := engine.Set([]byte("b"), []byte("2")); err != nil {
		t.Fatalf("second Set() error = %v", err)
	}

	if len(engine.frozenStores) != 1 {
		t.Fatalf("frozen stores = %d, want 1", len(engine.frozenStores))
	}
	if got, found := engine.frozenStores[0].Get("a"); !found || string(got.Data) != "1" {
		t.Fatalf("frozen Get(a) = (%+v, %v), want frozen value", got, found)
	}
	if got, found := engine.store.Get("b"); !found || string(got.Data) != "2" {
		t.Fatalf("active Get(b) = (%+v, %v), want mutable value", got, found)
	}

	assertEngineValue(t, engine, "a", "1")
	assertEngineValue(t, engine, "b", "2")
}

func TestDeleteAfterRotationWritesNewerTombstoneToActiveTable(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(newMemTableWithMaxSize(22), w)

	for _, record := range []struct{ key, value string }{
		{"a", "1"},
		{"b", "2"},
		{"c", "3"},
	} {
		if err := engine.Set([]byte(record.key), []byte(record.value)); err != nil {
			t.Fatalf("Set(%q) error = %v", record.key, err)
		}
	}

	deleted, err := engine.Delete([]byte("a"))
	if err != nil || !deleted {
		t.Fatalf("Delete() = (%v, %v), want (true, nil)", deleted, err)
	}
	if _, err := engine.Get([]byte("a")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(a) error = %v, want %v", err, ErrKeyNotFound)
	}

	var tombstone db.Record
	for _, record := range engine.store.GetAllSorted() {
		if record.Key == "a" {
			tombstone = record
			break
		}
	}
	if !tombstone.Deleted || tombstone.Lsn != 4 {
		t.Fatalf("active tombstone = %+v, want delete tombstone at LSN 4", tombstone)
	}
}

func TestEngineRecoversFromDurableWALAfterReopen(t *testing.T) {
	walDir := t.TempDir()
	firstWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	firstEngine := NewEngine(newBasicMemTable, firstWal)
	if _, err := firstEngine.Recover(); err != nil {
		t.Fatal(err)
	}
	if err := firstEngine.Set([]byte("kept"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if err := firstEngine.Set([]byte("deleted"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if deleted, err := firstEngine.Delete([]byte("deleted")); err != nil || !deleted {
		t.Fatalf("Delete() = (%v, %v), want (true, nil)", deleted, err)
	}
	if err := firstEngine.Close(); err != nil {
		t.Fatal(err)
	}

	secondWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	secondEngine := NewEngine(newBasicMemTable, secondWal)
	count, err := secondEngine.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("Recover() count = %d, want 3", count)
	}
	assertEngineValue(t, secondEngine, "kept", "value")
	if _, err := secondEngine.Get([]byte("deleted")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(deleted) error = %v, want %v", err, ErrKeyNotFound)
	}
	if err := secondEngine.Set([]byte("after-recovery"), []byte("next-lsn")); err != nil {
		t.Fatal(err)
	}
	if err := secondEngine.Close(); err != nil {
		t.Fatal(err)
	}

	thirdWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	thirdEngine := NewEngine(newBasicMemTable, thirdWal)
	defer func() {
		if err := thirdEngine.Close(); err != nil {
			t.Errorf("close third engine: %v", err)
		}
	}()
	count, err = thirdEngine.Recover()
	if err != nil {
		t.Fatal(err)
	}
	if count != 4 {
		t.Fatalf("third Recover() count = %d, want 4", count)
	}
	assertEngineValue(t, thirdEngine, "after-recovery", "next-lsn")
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
	writeErr      error
	nextLSN       uint64
}

func (w *engineTestWal) Write(op wal.OpType, data []byte) (uint64, error) {
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.writes = append(w.writes, engineTestWalWrite{op: op, data: bytes.Clone(data)})
	w.nextLSN++
	return w.nextLSN, nil
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

type orderedEngineTestWal struct {
	events           *[]string
	wasApplied       func() bool
	appliedBeforeWAL bool
	nextLSN          uint64
}

func (w *orderedEngineTestWal) Write(wal.OpType, []byte) (uint64, error) {
	w.appliedBeforeWAL = w.wasApplied()
	*w.events = append(*w.events, "wal")
	w.nextLSN++
	return w.nextLSN, nil
}

func (*orderedEngineTestWal) Replay(func(wal.WalEntry) error) (uint64, error) { return 0, nil }
func (*orderedEngineTestWal) Close() error                                    { return nil }
