package db

import (
	"sync"
)

// MemoryStore is a concurrency-safe in-memory Store implementation.
type MemoryStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

// NewMemoryStore creates an empty in-memory key-value store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{values: make(map[string][]byte)}
}

// Set stores value under key.
func (s *MemoryStore) Set(key, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[string(key)] = clone(value)
	return nil
}

// Get returns a copy of the value stored under key.
func (s *MemoryStore) Get(key []byte) ([]byte, bool) {
	s.mu.RLock()
	value, found := s.values[string(key)]
	s.mu.RUnlock()
	if !found {
		return nil, false
	}
	return clone(value), true
}

// Delete removes key and reports whether it existed.
func (s *MemoryStore) Delete(key []byte) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, found := s.values[string(key)]; !found {
		return false
	}
	delete(s.values, string(key))
	return true
}

func (s *MemoryStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	clear(s.values)
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
