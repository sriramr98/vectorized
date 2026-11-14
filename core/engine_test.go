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
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN:    1,
		OpType: wal.OpSet,
		Data:   encodeEngineTestArgs(t, []byte("key"), []byte("value"), []byte("extra")),
	}}}
	engine := NewEngine(func() db.MutableMemTable { return store }, w)

	if _, err := engine.Recover(); err == nil {
		t.Fatal("Recover() error = nil, want SET arity error")
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

func TestEnginePostWALApplyFailurePreservesReadsAndRejectsWrites(t *testing.T) {
	store := newDefaultBasicMemTable()
	w := &engineTestWal{}
	engine := NewEngine(func() db.MutableMemTable { return store }, w)
	defer engine.Close()

	if err := engine.Set([]byte("existing"), []byte("value")); err != nil {
		t.Fatal(err)
	}
	assertEngineValue(t, engine, "existing", "value")

	// Inject an invalid assigned LSN at the WAL seam. The append succeeds,
	// but the real mutable memtable rejects the duplicate LSN during Apply.
	w.nextLSN = 0
	if err := engine.Set([]byte("uncertain"), []byte("value")); err == nil {
		t.Fatal("Set() with duplicate WAL LSN = nil, want apply failure")
	}
	if len(w.writes) != 2 {
		t.Fatalf("WAL writes = %d, want initial write and WAL-accepted failed apply", len(w.writes))
	}
	if _, found := store.Get("uncertain"); found {
		t.Fatal("failed Apply published the uncertain record")
	}

	if value, err := engine.Get([]byte("existing")); err != nil || string(value) != "value" {
		t.Errorf("Get(existing) after uncertain write = (%q, %v), want (value, nil)", value, err)
	}
	if _, err := engine.Get([]byte("uncertain")); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("Get(uncertain) after failed Apply = %v, want missing", err)
	}
	if err := engine.Set([]byte("later"), []byte("value")); !errors.Is(err, ErrEngineUnhealthy) {
		t.Errorf("Set() after uncertain write = %v, want unhealthy error", err)
	}
	if deleted, err := engine.Delete([]byte("existing")); !errors.Is(err, ErrEngineUnhealthy) || deleted {
		t.Errorf("Delete() after uncertain write = (%v, %v), want (false, unhealthy error)", deleted, err)
	}
	if len(w.writes) != 2 {
		t.Errorf("WAL writes after rejected operations = %d, want 2", len(w.writes))
	}
	if rec, found := store.Get("existing"); !found || rec.Deleted || string(rec.Data) != "value" {
		t.Errorf("existing record changed after rejected operations: (%+v, %v)", rec, found)
	}
	if _, found := store.Get("later"); found {
		t.Error("rejected Set published a new record")
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

// Each transition retains the newest logical record and writes it
// to the WAL once, even though memory application may retry after a rotation.
func TestEngineLatestValueAcrossThreeMemTableGenerations(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(newMemTableWithMaxSize(11), w)
	for _, step := range []struct{ key, value string }{
		{"a", "1"}, {"b", "2"}, {"a", "3"}, {"c", "4"},
	} {
		if err := engine.Set([]byte(step.key), []byte(step.value)); err != nil {
			t.Fatalf("Set(%q) = %v", step.key, err)
		}
	}
	assertEngineValue(t, engine, "a", "3")
	assertEngineValue(t, engine, "b", "2")
	assertEngineValue(t, engine, "c", "4")
	if len(w.writes) != 4 {
		t.Fatalf("WAL writes = %d, want one per Set", len(w.writes))
	}
	for i, step := range []struct{ key, value string }{
		{"a", "1"}, {"b", "2"}, {"a", "3"}, {"c", "4"},
	} {
		if w.writes[i].op != wal.OpSet {
			t.Fatalf("write %d op = %d, want SET", i, w.writes[i].op)
		}
		args, err := utils.DecodeLengthEncodedBytes(w.writes[i].data)
		if err != nil {
			t.Fatal(err)
		}
		if len(args) != 2 || string(args[0]) != step.key || string(args[1]) != step.value {
			t.Fatalf("write %d args = %q, want (%q, %q)", i, args, step.key, step.value)
		}
	}
	if w.nextLSN != 4 {
		t.Fatalf("WAL LSN = %d, want 4", w.nextLSN)
	}
}

func TestEngineOversizedSetFailsBeforeWALAppend(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(newMemTableWithMaxSize(10), w)
	if err := engine.Set([]byte("a"), []byte("x")); err == nil {
		t.Fatal("oversized Set succeeded")
	}
	if len(w.writes) != 0 {
		t.Fatalf("oversized Set appended %d WAL records", len(w.writes))
	}
	if len(engine.frozenStores) != 0 {
		t.Fatalf("oversized Set froze %d tables", len(engine.frozenStores))
	}
	if got := engine.store.GetAllSorted(); len(got) != 0 {
		t.Fatalf("active records = %+v, want empty", got)
	}
	if err := engine.Set([]byte("a"), []byte{}); err != nil {
		t.Fatalf("valid write after validation failure = %v", err)
	}
}

func TestEngineDeleteWALFailureLeavesLiveValueAndNoTombstone(t *testing.T) {
	w := &engineTestWal{}
	engine := NewEngine(newMemTableWithMaxSize(10), w)
	if err := engine.Set([]byte("a"), []byte{}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Set([]byte("b"), []byte{}); err != nil {
		t.Fatal(err)
	}
	w.writeErr = errors.New("WAL unavailable")
	if deleted, err := engine.Delete([]byte("a")); !errors.Is(err, w.writeErr) || deleted {
		t.Fatalf("Delete(a) with WAL failure = (%v, %v), want (false, WAL error)", deleted, err)
	}
	assertEngineValue(t, engine, "a", "")
	if len(w.writes) != 2 {
		t.Fatalf("WAL writes = %d, want only original SETs", len(w.writes))
	}
	if len(engine.frozenStores) != 2 {
		t.Fatalf("frozen tables = %d, want 2", len(engine.frozenStores))
	}
}

func TestEnginePostWALApplyFailureIsRecoveredAfterReopen(t *testing.T) {
	walDir := t.TempDir()
	firstWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	frozen := newDefaultBasicMemTable()
	first := NewEngine(func() db.MutableMemTable { return frozen }, firstWal)
	if _, err := first.Recover(); err != nil {
		t.Fatal(err)
	}
	frozen.Freeze()
	if err := first.Set([]byte("committed-to-wal"), []byte("value")); err == nil {
		t.Fatal("Set() with frozen active table = nil, want apply failure")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	secondWal, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	second := NewEngine(newBasicMemTable, secondWal)
	defer func() {
		if err := second.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()
	count, err := second.Recover()
	if err != nil || count != 1 {
		t.Fatalf("reopen Recover() = (%d, %v), want (1, nil)", count, err)
	}
	assertEngineValue(t, second, "committed-to-wal", "value")
}

// M02-T6: replay must use the same capacity transition as ordinary writes.
func TestEngineRecoverySpansMultipleMemTables(t *testing.T) {
	w := &engineTestWal{replayEntries: []wal.WalEntry{
		{LSN: 1, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("a"), []byte{})},
		{LSN: 2, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("b"), []byte{})},
		{LSN: 3, OpType: wal.OpDelete, Data: encodeEngineTestArgs(t, []byte("a"))},
		{LSN: 4, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("a"), []byte("v"))},
	}}
	engine := NewEngine(newMemTableWithMaxSize(11), w)
	count, err := engine.Recover()
	if err != nil {
		t.Fatalf("Recover() = (%d, %v), want (4, nil)", count, err)
	}
	if count != 4 {
		t.Fatalf("Recover() count = %d, want 4", count)
	}
	assertEngineValue(t, engine, "a", "v")
	assertEngineValue(t, engine, "b", "")
	if len(w.writes) != 0 {
		t.Fatalf("recovery appended %d WAL records", len(w.writes))
	}
}

func TestEngineRecoversMultiTableHistoryFromDurableWAL(t *testing.T) {
	walDir := t.TempDir()
	writer, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Replay(func(wal.WalEntry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct {
		op   wal.OpType
		args [][]byte
	}{
		{wal.OpSet, [][]byte{[]byte("a"), []byte{}}},
		{wal.OpSet, [][]byte{[]byte("b"), []byte{}}},
		{wal.OpDelete, [][]byte{[]byte("a")}},
		{wal.OpSet, [][]byte{[]byte("a"), []byte("v")}},
	} {
		if _, err := writer.Write(entry.op, encodeEngineTestArgs(t, entry.args...)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := wal.NewWal(nil, walDir)
	if err != nil {
		t.Fatal(err)
	}
	engine := NewEngine(newMemTableWithMaxSize(11), reader)
	defer func() {
		if err := engine.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()
	if count, err := engine.Recover(); err != nil || count != 4 {
		t.Fatalf("Recover() = (%d, %v), want (4, nil)", count, err)
	}
	assertEngineValue(t, engine, "a", "v")
	assertEngineValue(t, engine, "b", "")
	if err := engine.Set([]byte("c"), []byte{}); err != nil {
		t.Fatalf("Set() after multi-table recovery = %v", err)
	}
	assertEngineValue(t, engine, "c", "")
}

func TestEngineRecoveryRejectsRecordLargerThanConfiguredMemTable(t *testing.T) {
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN: 1, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("a"), []byte("x")),
	}}}
	engine := NewEngine(newMemTableWithMaxSize(10), w)
	if count, err := engine.Recover(); err == nil || count != 0 {
		t.Fatalf("Recover() = (%d, %v), want capacity error with no published records", count, err)
	}
	if got := engine.store.GetAllSorted(); len(got) != 0 {
		t.Fatalf("active records after failed recovery = %+v, want empty", got)
	}
	if len(engine.frozenStores) != 0 {
		t.Fatalf("frozen tables after failed recovery = %d", len(engine.frozenStores))
	}
	if len(w.writes) != 0 {
		t.Fatalf("recovery appended %d WAL records", len(w.writes))
	}
}

func TestEngineSuccessfulRecoveryDiscardsPreviousFrozenStores(t *testing.T) {
	w := &engineTestWal{replayEntries: []wal.WalEntry{{
		LSN: 1, OpType: wal.OpSet, Data: encodeEngineTestArgs(t, []byte("c"), []byte{}),
	}}}
	engine := NewEngine(newMemTableWithMaxSize(10), w)
	if err := engine.Set([]byte("a"), []byte{}); err != nil {
		t.Fatal(err)
	}
	if err := engine.Set([]byte("b"), []byte{}); err != nil {
		t.Fatal(err)
	}
	if count, err := engine.Recover(); err != nil || count != 1 {
		t.Fatalf("Recover() = (%d, %v), want (1, nil)", count, err)
	}
	assertEngineValue(t, engine, "c", "")
	for _, key := range []string{"a", "b"} {
		if _, err := engine.Get([]byte(key)); !errors.Is(err, ErrKeyNotFound) {
			t.Fatalf("Get(%q) after recovery = %v, want missing", key, err)
		}
	}
}

func TestEngineCloseIsIdempotentAndRejectsLaterOperations(t *testing.T) {
	engine := NewEngine(newBasicMemTable, &engineTestWal{})
	if err := engine.Set([]byte("a"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("first Close() = %v", err)
	}
	if err := engine.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if _, err := engine.Get([]byte("a")); err == nil {
		t.Fatal("Get() after Close() = nil, want closed error")
	}
	if err := engine.Set([]byte("a"), []byte("v")); err == nil {
		t.Fatal("Set() after Close() = nil, want closed error")
	}
	if _, err := engine.Delete([]byte("a")); err == nil {
		t.Fatal("Delete() after Close() = nil, want closed error")
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
	replayCalls   int
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
		w.replayCalls++
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
