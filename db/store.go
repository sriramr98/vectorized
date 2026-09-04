// Package db defines the storage contract used by Vectorized's public API.
package db

// Store is the byte-oriented key-value interface used by protocol handlers.
// Implementations must not retain or return caller-owned byte slices.
type Store interface {
	Set(key, value []byte) error
	Get(key []byte) (value []byte, found bool, err error)
	Delete(key []byte) (deleted bool, err error)
}
