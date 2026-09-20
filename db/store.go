// Package db defines the storage contract used by Vectorized's public API.
package db

type MemTable interface {
	Apply(record Record) error
	Delete(key string) (deleted bool)
	// Clear clears all data from the store
	Clear() error
}

type ReadOnlyMemTable interface {
	Get(key string) (r Record, found bool)
	// GetAllSorted returns all records sorted by key
	GetAllSorted() []Record
}

type MutableMemTable interface {
	MemTable
	ReadOnlyMemTable

	Freeze() ReadOnlyMemTable
}
