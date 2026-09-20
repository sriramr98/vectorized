package db

import (
	"bytes"
	"errors"
	"slices"
	"testing"
)

func newTestMemTable(maxSizeBytes uint64) *BasicMemTable {
	return &BasicMemTable{
		values:       make(map[string]*Record),
		maxSizeBytes: maxSizeBytes,
	}
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
	if !found || bytes.Equal(again.Data, []byte("value")) {
		t.Fatalf("Get() after caller mutation = %+v, want unchanged value", again)
	}
}

func TestBasicMemTableDeleteHidesValueButRetainsTombstoneForFlush(t *testing.T) {
	table := newTestMemTable(DefaultMaxSizeBytes)
	if err := table.Apply(Record{Key: "b", Data: []byte("value"), Lsn: 7}); err != nil {
		t.Fatal(err)
	}

	if !table.Delete("b") {
		t.Fatal("Delete() = false, want true")
	}
	if _, found := table.Get("b"); found {
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
	if table.Delete("kept") {
		t.Fatal("Delete() on frozen table = true, want false")
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
