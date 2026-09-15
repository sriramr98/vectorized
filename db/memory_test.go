package db_test

import (
	"testing"

	"github.com/sriramr98/vectorized/db"
)

func TestMemoryStoreCopiesKeysAndValues(t *testing.T) {
	store := db.NewMemoryStore()
	key := []byte("key")
	value := []byte("value")
	if err := store.Set(key, value); err != nil {
		t.Fatal(err)
	}

	key[0] = 'x'
	value[0] = 'x'
	got, found := store.Get([]byte("key"))
	if !found {
		t.Fatal("Get() found = false, want true")
	}
	if gotValue := string(got); gotValue != "value" {
		t.Fatalf("Get() value = %q, want %q", gotValue, "value")
	}

	got[0] = 'x'
	again, found := store.Get([]byte("key"))
	if !found {
		t.Fatalf("expected to find key \"key\" but not found")
	}
	if gotValue := string(again); gotValue != "value" {
		t.Fatalf("second Get() value = %q, want %q", gotValue, "value")
	}
}

func TestMemoryStoreDelete(t *testing.T) {
	store := db.NewMemoryStore()
	if err := store.Set([]byte("key"), []byte("value")); err != nil {
		t.Fatal(err)
	}

	deleted := store.Delete([]byte("key"))
	if !deleted {
		t.Fatal("Delete() deleted = false, want true")
	}

	deleted = store.Delete([]byte("key"))
	if deleted {
		t.Fatal("second Delete() deleted = true, want false")
	}
}
