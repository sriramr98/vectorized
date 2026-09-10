package wal

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/sriramr98/vectorized/utils"
	"golang.org/x/sys/unix"
)

const FILE_PREFIX = "wal_segment"

var (
	ErrUnknownFileName = errors.New("unknown File name")
	ErrAlreadyClosed   = errors.New("wal already closed")
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

type Wal struct {
	mu                 sync.Mutex
	dirpath            string
	opts               WalOptions
	segments           []*Segment
	openSegment        *Segment
	currentSegmentSize uint64
	latestSegmentId    uint64
	dirLocker          *utils.LockedDir
	closed             bool
	currentLSN         uint64
	syncTimer          *time.Timer
}

// NewWal creates a Wal with default options
func NewWal(ctx context.Context, dirpath string) (*Wal, error) {
	return NewWalWithOpts(ctx, dirpath, DefaultWalOpts)
}

// NewWalWithOpts creates a Wal with custom options
func NewWalWithOpts(ctx context.Context, dirpath string, opts WalOptions) (*Wal, error) {
	dirpath, err := filepath.Abs(dirpath)
	if err != nil {
		return nil, err
	}

	if err := utils.EnsureDir(dirpath); err != nil {
		return nil, err
	}

	locker, err := utils.NewLockedDir(dirpath, "wal.lock")
	if err != nil {
		return nil, err
	}

	if err := locker.TryLock(); err != nil {
		return nil, err
	}

	Files, err := os.ReadDir(dirpath)
	if err != nil {
		if err := locker.Release(); err != nil {
			return nil, err
		}
		return nil, err
	}

	var segments []*Segment
	var lastMaxId uint64

	for _, entry := range Files {
		if entry.IsDir() {
			continue
		}

		name := WalFileName(entry.Name())
		idx, err := name.Parse()
		if err != nil {
			// unknown File name. Skip
			continue
		}

		// we don't need to open File since we won't be writing to them
		segments = append(segments, NewSegment(nil, filepath.Join(dirpath, entry.Name()), uint64(idx)))
		lastMaxId = max(lastMaxId, uint64(idx))
	}

	slices.SortFunc(segments, func(a, b *Segment) int {
		return cmp.Compare(a.idx, b.idx)
	})

	// we always create a new File for open segment even if previous File wasn't used to it's full capacity
	nextSegmentId := lastMaxId + 1
	openSegment, err := createWalFile(dirpath, nextSegmentId)
	if err != nil {
		if err := locker.Release(); err != nil {
			return nil, err
		}
		return nil, err
	}

	w := &Wal{
		mu:          sync.Mutex{},
		dirpath:     dirpath,
		opts:        opts,
		segments:    segments,
		openSegment: openSegment,
		dirLocker:   locker,
		// every new wal initialization always creates a new segment for future writes which makes this simpler
		currentSegmentSize: 0,
		latestSegmentId:    uint64(nextSegmentId),
		syncTimer:          time.NewTimer(opts.syncInterval),
	}

	go w.schedulePeriodicSync(ctx)

	return w, nil
}

func (w *Wal) Write(data []byte, opType OpType) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrAlreadyClosed
	}

	w.currentLSN += 1
	entry := WalEntryV1{
		LSN:    w.currentLSN,
		OpType: opType,
		Data:   data,
	}

	buf := bytes.NewBuffer([]byte{})
	n, err := entry.Encode(buf)
	if err != nil {
		return err
	}

	if w.currentSegmentSize+uint64(n) > utils.MBToBytes(w.opts.maxFileSizeMB) {
		if err := w.rotateSegment(); err != nil {
			return err
		}
	}

	// Segment is opened with O_APPEND, so writes always append and seek to end automatically
	if n, err = w.openSegment.File.Write(buf.Bytes()); err != nil {
		return err
	} else {
		w.currentSegmentSize += uint64(n)
		return nil
	}
}

func (w *Wal) rotateSegment() error {
	// Segment.Close will flush any in-memory changes to disk
	if err := w.openSegment.Close(); err != nil {
		return err
	}

	w.segments = append(w.segments, w.openSegment)

	w.latestSegmentId = w.latestSegmentId + 1

	newSegment, err := createWalFile(w.dirpath, w.latestSegmentId)
	if err != nil {
		return err
	}

	w.currentSegmentSize = 0
	w.openSegment = newSegment

	if w.syncTimer != nil {
		w.syncTimer.Reset(w.opts.syncInterval)
	}

	return nil
}

func (w *Wal) schedulePeriodicSync(ctx context.Context) {
	for {
		select {
		case <-w.syncTimer.C:
			w.mu.Lock()

			if w.closed {
				w.syncTimer.Stop()
				return
			}

			if err := w.openSegment.Sync(); err != nil {
				log.Printf("error periodically syncing open segment: %s\n", err)
			}

			w.mu.Unlock()
		case <-ctx.Done():
			w.mu.Lock()
			defer w.mu.Unlock()

			if w.syncTimer != nil {
				w.syncTimer.Stop()
				w.syncTimer = nil
			}
			return
		}

	}
}

func (w *Wal) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.openSegment != nil && w.openSegment.File != nil {
		// release lock before closing segments
		if err := unix.Flock(int(w.openSegment.File.Fd()), unix.LOCK_UN); err != nil {
			return err
		}

		if err := w.openSegment.Close(); err != nil {
			return err
		}
	}
	w.openSegment = nil

	for _, segment := range w.segments {
		if err := segment.Close(); err != nil {
			return err
		}
	}
	clear(w.segments)

	err := w.dirLocker.Release()
	if err != nil {
		return err
	}
	w.dirLocker = nil

	w.closed = true
	return nil
}

func createWalFile(dirpath string, idx uint64) (*Segment, error) {
	File_name := NewWalFileName(idx)
	File_path := filepath.Join(dirpath, File_name)

	File, err := os.OpenFile(File_path, os.O_RDWR|os.O_CREATE|os.O_APPEND, utils.PermFileReadWriteOwnerOnly)

	if err != nil {
		return nil, err
	}

	return NewSegment(File, File_path, idx), nil
}
