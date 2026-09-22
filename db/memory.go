package db

import (
	"errors"
	"slices"
	"sync"
)

var (
	ErrMemTableMaxSize             = errors.New("mem table has reached max size. freeze and create new mem table")
	ErrSetNotAllowedInFrozen error = errors.New("unable to modify data in a frozen table")
	ErrDataLSNStale                = errors.New("Apply received for an older LSN. Data is stale")
)

var DefaultMaxSizeBytes uint64 = 64 * 1024 * 1024 // 64MB

type Record struct {
	Key     string
	Data    []byte
	Lsn     uint64
	Deleted bool //tombstone value to track deleted data
}

func (r Record) Len() int {
	return 8 + 1 + len(r.Data) + len(r.Key) // 8 for LSN, 1 for Deleted
}

func (r *Record) Clone() Record {
	return Record{
		Key:     r.Key,
		Data:    clone(r.Data),
		Lsn:     r.Lsn,
		Deleted: r.Deleted,
	}
}

// BasicMemTable is a concurrent safe mem table implementation which is not optimised
type BasicMemTable struct {
	mu               sync.RWMutex
	values           map[string]*Record
	frozen           bool
	currentSizeBytes uint64
	maxSizeBytes     uint64
	lastLSN          uint64
}

// NewMemoryStore creates an empty in-memory key-value store.
func NewMemoryStore() *BasicMemTable {
	return &BasicMemTable{values: make(map[string]*Record), frozen: false, currentSizeBytes: 0, maxSizeBytes: DefaultMaxSizeBytes}
}

// Set stores value under key. errors if memtable is frozen
func (s *BasicMemTable) Apply(rec Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.frozen {
		return ErrSetNotAllowedInFrozen
	}

	if rec.Lsn <= s.lastLSN {
		return ErrDataLSNStale
	}

	old, exists := s.values[rec.Key]
	val := rec.Clone()

	if exists {
		sizeAfterNewEntry := s.currentSizeBytes - uint64(old.Len()) + uint64(val.Len()) // Current Total Size - Old Entry Size + New Entry Size ( becuase old entry is to be replaced with new entry in memtable )

		if sizeAfterNewEntry > s.maxSizeBytes {
			return ErrMemTableMaxSize
		}

		s.currentSizeBytes = sizeAfterNewEntry
	} else {
		if val.Len()+int(s.currentSizeBytes) > int(s.maxSizeBytes) {
			return ErrMemTableMaxSize
		}

		s.currentSizeBytes += uint64(val.Len())
	}

	s.values[rec.Key] = &val
	s.lastLSN = rec.Lsn

	return nil
}

// Get returns a copy of the value stored under key. supports reads even when frozen
func (s *BasicMemTable) Get(key string) (Record, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, found := s.values[key]
	if !found {
		return Record{}, false
	}

	if value.Deleted {
		return Record{}, false
	}

	return value.Clone(), true
}

func (s *BasicMemTable) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.frozen {
		return ErrSetNotAllowedInFrozen
	}

	s.currentSizeBytes = 0
	clear(s.values)
	return nil
}

func (s *BasicMemTable) Freeze() ReadOnlyMemTable {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.frozen = true
	return s
}

func (s *BasicMemTable) GetAllSorted() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)
	for key := range s.values {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	values := make([]Record, len(s.values))

	for idx, key := range keys {
		values[idx] = s.values[key].Clone()
	}

	return values

}

func clone(value []byte) []byte {
	clonedValue := make([]byte, len(value))
	copy(clonedValue, value)

	return clonedValue
}
