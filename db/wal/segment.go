package wal

import (
	"cmp"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/sriramr98/vectorized/utils"
)

type Segment struct {
	*os.File
	idx  uint64
	path string
	mu   sync.RWMutex
}

func NewSegment(File *os.File, path string, idx uint64) *Segment {
	return &Segment{
		File: File,
		idx:  idx,
		path: path,
		mu:   sync.RWMutex{},
	}
}

func discoverWalSegments(dirpath string) ([]*Segment, error) {
	entries, err := os.ReadDir(dirpath)
	if err != nil {
		return nil, err
	}

	segments := make([]*Segment, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		idx, err := WalFileName(entry.Name()).Parse()
		if err != nil {
			continue
		}
		segments = append(segments, NewSegment(nil, filepath.Join(dirpath, entry.Name()), uint64(idx)))
	}

	slices.SortFunc(segments, func(a, b *Segment) int {
		return cmp.Compare(a.idx, b.idx)
	})
	return segments, nil
}

func (w *DurableWal) activateWritableSegment() error {
	if len(w.segments) == 0 {
		segment, err := createWalFile(w.dirpath, 1)
		if err != nil {
			return err
		}
		w.openSegment = segment
		w.currentSegmentSize = 0
		return nil
	}

	latest := w.segments[len(w.segments)-1]
	segment, reused, err := reuseOrCreateSegment(latest, w.opts, w.dirpath)
	if err != nil {
		return err
	}
	info, err := segment.Stat()
	if err != nil {
		_ = segment.Close()
		return err
	}
	if reused {
		w.segments = w.segments[:len(w.segments)-1]
	}
	w.openSegment = segment
	w.currentSegmentSize = uint64(info.Size())
	return nil
}

func (w *DurableWal) rotateSegment() error {
	w.logger.Debug("rotating segment")
	if err := w.openSegment.Close(); err != nil {
		return err
	}
	w.segments = append(w.segments, w.openSegment)

	newSegment, err := createWalFile(w.dirpath, w.openSegment.idx+1)
	if err != nil {
		return err
	}
	w.openSegment = newSegment
	w.currentSegmentSize = 0
	return nil
}

// createWalFile creates a segment and synchronizes its new directory entry.
func createWalFile(dirpath string, idx uint64) (*Segment, error) {
	return createWalFileWithSync(dirpath, idx, utils.SyncDir)
}

func createWalFileWithSync(dirpath string, idx uint64, syncDir func(string) error) (*Segment, error) {
	name := NewWalFileName(idx)
	path := filepath.Join(dirpath, name)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, utils.PermFileReadWriteOwnerOnly)
	if err != nil {
		return nil, err
	}
	if err := syncDir(dirpath); err != nil {
		return nil, errors.Join(err, file.Close())
	}
	return NewSegment(file, path, idx), nil
}

func reuseOrCreateSegment(segment *Segment, opts WalOptions, dirpath string) (*Segment, bool, error) {
	if segment == nil {
		return nil, false, errors.New("invalid segment")
	}

	info, err := os.Stat(segment.path)
	if err != nil {
		return nil, false, err
	}
	if info.Size() >= int64(utils.MBToBytes(opts.MaxFileSizeMB)) {
		created, err := createWalFile(dirpath, segment.idx+1)
		return created, false, err
	}

	file, err := os.OpenFile(segment.path, os.O_RDWR|os.O_APPEND, utils.PermFileReadWriteOwnerOnly)
	if err != nil {
		return nil, false, err
	}
	segment.File = file
	return segment, true, nil
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
