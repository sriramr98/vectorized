package wal

import (
	"os"
	"sync"
)

type Segment struct {
	*os.File
	idx        uint64
	path       string
	mu         sync.RWMutex
	entryCache []WalEntry
}

func NewSegment(File *os.File, path string, idx uint64) *Segment {
	return &Segment{
		File:       File,
		idx:        idx,
		path:       path,
		mu:         sync.RWMutex{},
		entryCache: []WalEntry{},
	}
}

func (s *Segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.File != nil {
		if err := s.Sync(); err != nil {
			return err
		}

		if err := s.File.Close(); err != nil {
			return err
		}

		s.File = nil
	}

	return nil
}
