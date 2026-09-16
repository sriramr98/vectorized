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

func TestSetExistingKeyRemainsUnchangedWhenWALWriteFails(t *testing.T) {
	store := db.NewMemoryStore()
	if err := store.Set([]byte("key"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("WAL write failed")
	engine := NewEngine(store, &engineTestWal{writeErr: wantErr})

	if err := engine.Set([]byte("key"), []byte("new")); !errors.Is(err, wantErr) {
		t.Fatalf("Set() error = %v, want %v", err, wantErr)
	}
	assertEngineValue(t, engine, "key", "old")
}

func TestSetNewKeyRemainsAbsentWhenWALWriteFails(t *testing.T) {
	wantErr := errors.New("WAL write failed")
	engine := NewEngine(db.NewMemoryStore(), &engineTestWal{writeErr: wantErr})

	if err := engine.Set([]byte("key"), []byte("value")); !errors.Is(err, wantErr) {
		t.Fatalf("Set() error = %v, want %v", err, wantErr)
	}
	if _, err := engine.Get([]byte("key")); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Get(key) error = %v, want %v", err, ErrKeyNotFound)
	}
}

func TestSetWritesWALBeforeApplyingMemory(t *testing.T) {
	var events []string
	w := &orderedEngineTestWal{events: &events}
	store := &orderedEngineTestStore{MemoryStore: db.NewMemoryStore(), events: &events}
	engine := NewEngine(store, w)

	if err := engine.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"wal", "memory"}; !slices.Equal(events, want) {
		t.Fatalf("mutation order = %v, want %v", events, want)
	}
}

func TestSetReportsMemoryFailureAfterWALWrite(t *testing.T) {
	w := &engineTestWal{}
	store := &failingSetEngineTestStore{
		MemoryStore: db.NewMemoryStore(),
		err:         errors.New("memory write failed"),
	}
	engine := NewEngine(store, w)

	if err := engine.Set([]byte("key"), []byte("value")); err == nil {
		t.Fatal("Set() error = nil, want memory-application error")
	}
	if len(w.writes) != 1 {
		t.Fatalf("WAL writes = %d, want 1 committed write", len(w.writes))
	}
	if _, found := store.Get([]byte("key")); found {
		t.Fatal("memory contains key after failed application")
	}
}

func TestEngineRecoversFromDurableWALAfterReopen(t *testing.T) {
	walDir := t.TempDir()
	firstWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	firstEngine := NewEngine(db.NewMemoryStore(), firstWal)
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
	secondEngine := NewEngine(db.NewMemoryStore(), secondWal)
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
	thirdEngine := NewEngine(db.NewMemoryStore(), thirdWal)
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
}

func (w *engineTestWal) Write(op wal.OpType, data []byte) error {
	if w.writeErr != nil {
		return w.writeErr
	}
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

type orderedEngineTestWal struct {
	events *[]string
}

func (w *orderedEngineTestWal) Write(wal.OpType, []byte) error {
	*w.events = append(*w.events, "wal")
	return nil
}

func (*orderedEngineTestWal) Replay(func(wal.WalEntry) error) (uint64, error) { return 0, nil }
func (*orderedEngineTestWal) Close() error                                    { return nil }

type orderedEngineTestStore struct {
	*db.MemoryStore
	events *[]string
}

func (s *orderedEngineTestStore) Set(key, value []byte) error {
	*s.events = append(*s.events, "memory")
	return s.MemoryStore.Set(key, value)
}

type failingSetEngineTestStore struct {
	*db.MemoryStore
	err error
}

func (s *failingSetEngineTestStore) Set([]byte, []byte) error {
	return s.err
}
