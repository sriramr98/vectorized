package db

import (
	"bytes"
	"errors"
	"slices"
	"sync"
	"testing"
)

func newTestMemTable(maxSizeBytes uint64) *BasicMemTable {
	return NewMemoryStore(MemStoreOpts{MaxSizeBytes: maxSizeBytes})
}

func TestBasicMemTableSetGetPreservesRecordAndDefensivelyCopiesData(t *testing.T) {
	table := newTestMemTable(DefaultMaxSizeBytes)
	input := Record{Key: "key", Data: []byte("value"), Lsn: 42}

	if err := table.Apply(input); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	input.Data[0] = 'X'

	got, found := table.Get("key")
	if !found {
		t.Fatal("Get() found = false, want true")
	}
	if got.Key != "key" || got.Lsn != 42 || got.Deleted || string(got.Data) != "value" {
		t.Fatalf("Get() = %+v, want set record", got)
	}

	got.Data[0] = 'Y'
	again, found := table.Get("key")
	if !found || !bytes.Equal(again.Data, []byte("value")) {
		t.Fatalf("Get() after caller mutation = %+v, want unchanged value", again)
	}
}

func TestBasicMemTableDeleteHidesValueButRetainsTombstoneForFlush(t *testing.T) {
	table := newTestMemTable(DefaultMaxSizeBytes)
	if err := table.Apply(Record{Key: "b", Data: []byte("value"), Lsn: 7}); err != nil {
		t.Fatal(err)
	}

	if err := table.Apply(Record{Key: "b", Deleted: true, Lsn: 8}); err != nil {
		t.Fatal("Delete() = false, want true")
	}
	if val, _ := table.Get("b"); !val.Deleted {
		t.Fatal("Get() found deleted value, want false")
	}

	records := table.GetAllSorted()
	if len(records) != 1 || records[0].Key != "b" || !records[0].Deleted {
		t.Fatalf("GetAllSorted() = %+v, want tombstone for b", records)
	}
}

func TestBasicMemTableGetAllSortedIsKeyOrderedAndDefensivelyCopied(t *testing.T) {
	table := newTestMemTable(DefaultMaxSizeBytes)
	for _, record := range []Record{
		{Key: "z", Data: []byte("last"), Lsn: 1},
		{Key: "a", Data: []byte("first"), Lsn: 2},
		{Key: "m", Data: []byte("middle"), Lsn: 3},
	} {
		if err := table.Apply(record); err != nil {
			t.Fatal(err)
		}
	}

	records := table.GetAllSorted()
	keys := make([]string, len(records))
	for i, record := range records {
		keys[i] = record.Key
	}
	if want := []string{"a", "m", "z"}; !slices.Equal(keys, want) {
		t.Fatalf("GetAllSorted() keys = %v, want %v", keys, want)
	}

	records[0].Data[0] = 'X'
	got, found := table.Get("a")
	if !found || !bytes.Equal(got.Data, []byte("first")) {
		t.Fatalf("Get(a) after returned-record mutation = %+v, want unchanged value", got)
	}
}

func TestBasicMemTableFreezeCreatesReadOnlyTable(t *testing.T) {
	table := newTestMemTable(DefaultMaxSizeBytes)
	if err := table.Apply(Record{Key: "kept", Data: []byte("value"), Lsn: 1}); err != nil {
		t.Fatal(err)
	}

	frozen := table.Freeze()
	if err := table.Apply(Record{Key: "new", Data: []byte("value"), Lsn: 2}); !errors.Is(err, ErrSetNotAllowedInFrozen) {
		t.Fatalf("Set() on frozen table error = %v, want %v", err, ErrSetNotAllowedInFrozen)
	}
	if err := table.Apply(Record{
		Key:     "kept",
		Deleted: true,
		Lsn:     3,
	}); err == nil {
		t.Fatal("Delete() on frozen table should return error but didn't")
	}

	got, found := frozen.Get("kept")
	if !found || string(got.Data) != "value" {
		t.Fatalf("frozen Get(kept) = %+v, found %v; want original record", got, found)
	}
	if _, found := frozen.Get("new"); found {
		t.Fatal("frozen table contains rejected post-freeze write")
	}
}

func TestBasicMemTableReplacementUsesCurrentFootprint(t *testing.T) {
	old := Record{Key: "key", Data: []byte("old-value"), Lsn: 1}
	newer := Record{Key: "key", Data: []byte("a-larger-new-value"), Lsn: 2}
	table := newTestMemTable(uint64(newer.Len()))

	if err := table.Apply(old); err != nil {
		t.Fatal(err)
	}
	if err := table.Apply(newer); err != nil {
		t.Fatalf("Set() replacement error = %v, want nil because only the current record counts", err)
	}
	got, found := table.Get("key")
	if !found || got.Lsn != newer.Lsn || string(got.Data) != string(newer.Data) {
		t.Fatalf("Get() = %+v, want replacement record", got)
	}
}

func TestBasicMemTableClearRemovesDataAndReclaimsCapacity(t *testing.T) {
	record := Record{Key: "key", Data: []byte("value"), Lsn: 1}
	table := newTestMemTable(uint64(record.Len()))
	if err := table.Apply(record); err != nil {
		t.Fatal(err)
	}
	if err := table.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	if _, found := table.Get("key"); found {
		t.Fatal("Get() found cleared key")
	}
	if err := table.Apply(record); err != nil {
		t.Fatalf("Set() after Clear() error = %v, want capacity to be reclaimed", err)
	}
}

// M01-T1: ordering is table-wide, not just per key, and rejection is atomic.
func TestBasicMemTableRejectsNonIncreasingLSNWithoutChangingState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		record Record
	}{
		{"older replacement", Record{Key: "kept", Data: []byte("changed"), Lsn: 9}},
		{"equal replacement", Record{Key: "kept", Data: []byte("changed"), Lsn: 10}},
		{"older new key", Record{Key: "new", Data: []byte("value"), Lsn: 9}},
		{"equal new key", Record{Key: "new", Data: []byte("value"), Lsn: 10}},
		{"stale tombstone", Record{Key: "kept", Deleted: true, Lsn: 9}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := newTestMemTable(64)
			kept := Record{Key: "kept", Data: []byte("original"), Lsn: 10}
			if err := table.Apply(kept); err != nil {
				t.Fatal(err)
			}
			before := table.GetAllSorted()
			if err := table.Apply(tc.record); !errors.Is(err, ErrDataLSNStale) {
				t.Fatalf("Apply() error = %v, want %v", err, ErrDataLSNStale)
			}
			assertMemTableRecords(t, table, before)
			if err := table.Apply(Record{Key: "new", Data: []byte("value"), Lsn: 11}); err != nil {
				t.Fatalf("newer Apply() after rejection = %v", err)
			}
		})
	}

	t.Run("zero is not a positive LSN", func(t *testing.T) {
		table := newTestMemTable(64)
		if err := table.Apply(Record{Key: "a", Data: []byte("value"), Lsn: 0}); !errors.Is(err, ErrDataLSNStale) {
			t.Fatalf("Apply(LSN=0) error = %v, want %v", err, ErrDataLSNStale)
		}
		assertMemTableRecords(t, table, nil)
		if err := table.Apply(Record{Key: "a", Data: []byte("value"), Lsn: 1}); err != nil {
			t.Fatal(err)
		}
	})
}

// M01-T2: reject insert/replace/delete-marker attempts without consuming their
// LSN or capacity. Sizes here are independent of Record.Len(): 8 + 1 + key + data.
func TestBasicMemTableCapacityRejectionPreservesRecordsCapacityAndLSN(t *testing.T) {
	for _, tc := range []struct {
		name     string
		initial  []Record
		rejected Record
		retry    Record
	}{
		{
			name:     "insert",
			initial:  []Record{{Key: "a", Data: []byte{}, Lsn: 1}},
			rejected: Record{Key: "b", Data: []byte("x"), Lsn: 2},
			retry:    Record{Key: "b", Data: []byte{}, Lsn: 2},
		},
		{
			name:     "replace",
			initial:  []Record{{Key: "a", Data: []byte{}, Lsn: 1}, {Key: "b", Data: []byte{}, Lsn: 2}},
			rejected: Record{Key: "a", Data: []byte("x"), Lsn: 3},
			retry:    Record{Key: "a", Data: []byte{}, Lsn: 3},
		},
		{
			name:     "tombstone",
			initial:  []Record{{Key: "a", Data: []byte{}, Lsn: 1}},
			rejected: Record{Key: "bb", Deleted: true, Lsn: 2},
			retry:    Record{Key: "b", Deleted: true, Lsn: 2},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := newTestMemTable(20)
			for _, record := range tc.initial {
				if err := table.Apply(record); err != nil {
					t.Fatal(err)
				}
			}
			if err := table.Apply(tc.rejected); !errors.Is(err, ErrMemTableMaxSize) {
				t.Fatalf("over-capacity Apply() error = %v, want %v", err, ErrMemTableMaxSize)
			}
			assertMemTableRecords(t, table, tc.initial)
			if err := table.Apply(tc.retry); err != nil {
				t.Fatalf("retry using rejected LSN at exact capacity = %v", err)
			}
		})
	}
}

func TestBasicMemTableCapacityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name              string
		limit             uint64
		wantCapacityError bool
	}{
		{"exact fit", 13, false},
		{"one byte over", 12, true},
		{"zero capacity", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := newTestMemTable(tc.limit)
			record := Record{Key: "a", Data: []byte("123"), Lsn: 1} // 13 bytes
			err := table.Apply(record)
			if tc.wantCapacityError {
				if !errors.Is(err, ErrRecordTooLarge) {
					t.Fatalf("Apply() = %v, want capacity error", err)
				}
				assertMemTableRecords(t, table, nil)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertMemTableRecords(t, table, []Record{record})
		})
	}
}

func TestBasicMemTableOversizedFirstRecordDoesNotConsumeLSN(t *testing.T) {
	table := newTestMemTable(10)
	if err := table.Apply(Record{Key: "a", Data: []byte("x"), Lsn: 7}); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("oversized first Apply() = %v, want capacity error", err)
	}
	assertMemTableRecords(t, table, nil)
	if err := table.Apply(Record{Key: "a", Data: []byte{}, Lsn: 7}); err != nil {
		t.Fatalf("smaller retry with same LSN = %v", err)
	}
}

// M01-T3: a uint64 budget larger than max int must not become negative during
// accounting. This exercises overflow handling without allocating huge data.
func TestBasicMemTableLargeCapacityDoesNotWrap(t *testing.T) {
	table := newTestMemTable(^uint64(0))
	record := Record{Key: "a", Data: []byte("value"), Lsn: 1}
	if err := table.Apply(record); err != nil {
		t.Fatalf("small record under maximum uint64 capacity = %v, want nil", err)
	}
	assertMemTableRecords(t, table, []Record{record})
}

func TestBasicMemTableSmallerReplacementAndTombstoneReclaimCapacity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement Record
	}{
		{"smaller value", Record{Key: "a", Data: []byte{}, Lsn: 2}},
		{"tombstone", Record{Key: "a", Deleted: true, Lsn: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			table := newTestMemTable(20)
			if err := table.Apply(Record{Key: "a", Data: []byte("0123456789"), Lsn: 1}); err != nil {
				t.Fatal(err)
			}
			if err := table.Apply(tc.replacement); err != nil {
				t.Fatal(err)
			}
			second := Record{Key: "b", Data: []byte{}, Lsn: 3}
			if err := table.Apply(second); err != nil {
				t.Fatalf("Apply() using reclaimed capacity = %v", err)
			}
			if err := table.Apply(Record{Key: "c", Deleted: true, Lsn: 4}); !errors.Is(err, ErrMemTableMaxSize) {
				t.Fatalf("additional tombstone = %v, want capacity error", err)
			}
			assertMemTableRecords(t, table, []Record{tc.replacement, second})
		})
	}
}

// M01-T4: returned records remain owned by callers even after freeze, and
// changing a returned snapshot must not affect later capacity decisions.
func TestBasicMemTableSnapshotsDoNotAliasOrChangeCapacity(t *testing.T) {
	table := newTestMemTable(23)
	original := Record{Key: "a", Data: []byte("abc"), Lsn: 1}
	if err := table.Apply(original); err != nil {
		t.Fatal(err)
	}
	exported := table.GetAllSorted()
	exported[0].Key = "changed"
	exported[0].Data[0] = 'X'
	exported[0].Data = make([]byte, 100)
	exported[0].Lsn = 100
	exported[0].Deleted = true
	second := Record{Key: "b", Data: []byte{}, Lsn: 2}
	if err := table.Apply(second); err != nil {
		t.Fatalf("Apply() after snapshot mutation = %v", err)
	}
	assertMemTableRecords(t, table, []Record{original, second})

	frozen := table.Freeze()
	got, found := frozen.Get("a")
	if !found {
		t.Fatal("frozen Get(a) missing")
	}
	got.Data[0] = 'Y'
	all := frozen.GetAllSorted()
	all[0].Data[0] = 'Z'
	assertMemTableRecords(t, frozen, []Record{original, second})
}

// M01-T5: freezing twice does not require pointer identity, only stable contents.
func TestBasicMemTableRepeatedFreezeAndRejectedClearPreserveContents(t *testing.T) {
	table := newTestMemTable(32)
	records := []Record{
		{Key: "a", Data: []byte("kept"), Lsn: 1},
		{Key: "b", Deleted: true, Lsn: 2},
	}
	for _, record := range records {
		if err := table.Apply(record); err != nil {
			t.Fatal(err)
		}
	}
	first, second := table.Freeze(), table.Freeze()
	if err := table.Clear(); !errors.Is(err, ErrSetNotAllowedInFrozen) {
		t.Fatalf("Clear() frozen = %v, want %v", err, ErrSetNotAllowedInFrozen)
	}
	if err := table.Apply(Record{Key: "a", Data: []byte("changed"), Lsn: 3}); !errors.Is(err, ErrSetNotAllowedInFrozen) {
		t.Fatalf("Apply() after repeated freeze = %v, want frozen error", err)
	}
	assertMemTableRecords(t, first, records)
	assertMemTableRecords(t, second, records)
}

func TestBasicMemTableClearResetsMultipleRecordsAndLSN(t *testing.T) {
	table := newTestMemTable(20)
	for _, record := range []Record{
		{Key: "a", Data: []byte{}, Lsn: 40},
		{Key: "b", Deleted: true, Lsn: 41},
	} {
		if err := table.Apply(record); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := table.Clear(); err != nil {
			t.Fatalf("Clear() #%d = %v", i+1, err)
		}
		assertMemTableRecords(t, table, nil)
	}
	record := Record{Key: "c", Data: []byte("0123456789"), Lsn: 1}
	if err := table.Apply(record); err != nil {
		t.Fatalf("Apply() after Clear() = %v", err)
	}
	assertMemTableRecords(t, table, []Record{record})
}

// M01-T6: empty keys/values and arbitrary bytes participate in ordinary ordering.
func TestBasicMemTableEmptyAndBinaryKeys(t *testing.T) {
	table := newTestMemTable(128)
	if _, found := table.Get("missing"); found {
		t.Fatal("empty table found missing key")
	}
	assertMemTableRecords(t, table, nil)
	input := []Record{
		{Key: "\xff", Deleted: true, Lsn: 1},
		{Key: "a\x00", Data: []byte{0, 255}, Lsn: 2},
		{Key: "", Data: []byte{}, Lsn: 3},
		{Key: "\x00", Data: []byte{}, Lsn: 4},
	}
	for _, record := range input {
		if err := table.Apply(record); err != nil {
			t.Fatal(err)
		}
	}
	assertMemTableRecords(t, table, []Record{input[2], input[3], input[1], input[0]})
	if _, found := table.Get("a"); found {
		t.Fatal("binary key collided with its prefix")
	}
}

func TestBasicMemTableConcurrentReadsApplyAndFreeze(t *testing.T) {
	table := newTestMemTable(128)
	if err := table.Apply(Record{Key: "a", Data: []byte{1}, Lsn: 1}); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(4)
	go func() {
		defer workers.Done()
		<-start
		for lsn := uint64(2); lsn <= 100; lsn++ {
			err := table.Apply(Record{Key: "a", Data: []byte{byte(lsn)}, Lsn: lsn})
			if errors.Is(err, ErrSetNotAllowedInFrozen) {
				return
			}
			if err != nil {
				t.Errorf("concurrent Apply() = %v", err)
				return
			}
		}
	}()
	for range 2 {
		go func() {
			defer workers.Done()
			<-start
			for range 100 {
				got, found := table.Get("a")
				if !found || len(got.Data) != 1 || got.Data[0] != byte(got.Lsn) {
					t.Errorf("concurrent Get() returned incoherent record: %+v, found %v", got, found)
					return
				}
				got.Data[0] = 0 // May not alter another reader's record.
				records := table.GetAllSorted()
				if len(records) != 1 || len(records[0].Data) != 1 || records[0].Data[0] != byte(records[0].Lsn) {
					t.Errorf("concurrent export returned incoherent records: %+v", records)
					return
				}
				records[0].Data[0] = 0
			}
		}()
	}
	var frozen ReadOnlyMemTable
	var atFreeze []Record
	go func() {
		defer workers.Done()
		<-start
		frozen = table.Freeze()
		atFreeze = frozen.GetAllSorted()
	}()
	close(start)
	workers.Wait()
	assertMemTableRecords(t, frozen, atFreeze)
	if err := table.Apply(Record{Key: "a", Data: []byte("changed"), Lsn: 101}); !errors.Is(err, ErrSetNotAllowedInFrozen) {
		t.Fatalf("Apply() after concurrent freeze = %v, want frozen error", err)
	}
}

func assertMemTableRecords(t *testing.T, table ReadOnlyMemTable, want []Record) {
	t.Helper()
	equal := func(a, b Record) bool {
		return a.Key == b.Key && a.Lsn == b.Lsn && a.Deleted == b.Deleted && bytes.Equal(a.Data, b.Data)
	}
	got := table.GetAllSorted()
	if !slices.EqualFunc(got, want, equal) {
		t.Fatalf("GetAllSorted() = %+v, want %+v", got, want)
	}
	for _, expected := range want {
		record, found := table.Get(expected.Key)
		if !found || !equal(record, expected) {
			t.Fatalf("Get(%q) = %+v, found %v; want %+v", expected.Key, record, found, expected)
		}
	}
}
