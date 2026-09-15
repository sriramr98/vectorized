// Package db defines the storage contract used by Vectorized's public API.
package db

type MemStore interface {
	Set(key, value []byte) error
	Get(key []byte) (value []byte, found bool)
	Delete(key []byte) (deleted bool)
	// Clear clears all data from the store
	Clear()
}
