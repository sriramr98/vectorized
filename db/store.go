// Package db defines the storage contract used by Vectorized's public API.
package db

type MemTable interface {
	Apply(record Record) error
	// Clear clears all data from the store
	Clear() error

	// CanAccomodate returns whether a the memtable can take this record in or error out based on size limitation
	CanAccomodate(record Record) error
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

type MutableStoreCreator func() MutableMemTable

type MemStoreOpts struct {
	MaxSizeBytes uint64
}

func DefaultMemStoreOpts() MemStoreOpts {
	return MemStoreOpts{
		MaxSizeBytes: DefaultMaxSizeBytes,
	}
}
