package wal

import (
	"bytes"
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/sriramr98/vectorized/utils"
	"golang.org/x/sys/unix"
)

const FILE_PREFIX = "wal_segment"

var (
	ErrUnknownFileName = errors.New("unknown File name")
	ErrAlreadyClosed   = errors.New("wal already closed")
)

type Wal interface {
	Replay(func(WalEntry) error) error
	Write(op OpType, data []byte) error
}

type DurableWal struct {
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
	logger             *slog.Logger
}

// NewWal creates a Wal with default options
func NewWal(ctx context.Context, logger *slog.Logger, dirpath string) (*DurableWal, error) {
	return NewWalWithOpts(ctx, dirpath, DefaultWalOpts, logger)
}

// NewWalWithOpts creates a Wal with custom options
func NewWalWithOpts(ctx context.Context, dirpath string, opts WalOptions, logger *slog.Logger) (*DurableWal, error) {
	if logger == nil {
		logger = slog.Default()
	}
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

	w := &DurableWal{
		mu:          sync.Mutex{},
		dirpath:     dirpath,
		opts:        opts,
		segments:    segments,
		openSegment: openSegment,
		dirLocker:   locker,
		// every new wal initialization always creates a new segment for future writes which makes this simpler
		currentSegmentSize: 0,
		latestSegmentId:    uint64(nextSegmentId),
		logger:             logger,
	}

	var latestLSN uint64
	if err := w.Replay(func(e WalEntry) error {
		if e.LSN <= latestLSN {
			return fmt.Errorf("WAL LSN %d is not greater than previous LSN %d", e.LSN, latestLSN)
		}
		latestLSN = e.LSN
		return nil
	}); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("recover WAL LSN: %w", err)
	}
	w.currentLSN = latestLSN

	return w, nil
}

func (w *DurableWal) Write(opType OpType, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrAlreadyClosed
	}

	w.currentLSN += 1
	w.logger.Debug("writing entry to wal", "lsn", w.currentLSN)
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
		if err := w.openSegment.Sync(); err != nil {
			return err
		}

		w.currentSegmentSize += uint64(n)
		return nil
	}
}

func (w *DurableWal) rotateSegment() error {
	w.logger.Debug("rotating segment")
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

	return nil
}

func (w *DurableWal) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.logger.Debug("closing wal")

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

// Replay reads all existing segment files, including the active segment, and
// emits entries in segment and LSN order.
func (w *DurableWal) Replay(fn func(e WalEntry) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrAlreadyClosed
	}

	var previousLSN uint64

	segments := append(slices.Clone(w.segments), w.openSegment)
	for _, segment := range segments {
		if segment == nil {
			continue
		}

		if len(segment.entryCache) > 0 {
			for _, entry := range segment.entryCache {
				previousLSN = entry.LSN
				if err := fn(entry); err != nil {
					return err
				}
			}
			continue
		}

		file, err := os.Open(segment.path)
		if err != nil {
			return fmt.Errorf("open WAL segment %q: %w", segment.path, err)
		}

		err = replaySegment(file, func(entry WalEntry) error {
			if entry.LSN <= previousLSN {
				return fmt.Errorf("WAL LSN %d is not greater than previous LSN %d", entry.LSN, previousLSN)
			}

			segment.entryCache = append(segment.entryCache, entry)
			previousLSN = entry.LSN
			return fn(entry)
		})
		closeErr := file.Close()
		if err != nil {
			return fmt.Errorf("replay WAL segment %q: %w", segment.path, err)
		}
		if closeErr != nil {
			return fmt.Errorf("close WAL segment %q: %w", segment.path, closeErr)
		}
	}

	return nil
}

func replaySegment(r io.Reader, fn func(WalEntry) error) error {
	header := make([]byte, walEntryHeaderSize)
	for {
		// first read header bytes to identify body lenth
		n, err := io.ReadFull(r, header)
		if err == io.EOF && n == 0 {
			return nil // clean end between records
		}
		if err != nil {
			return fmt.Errorf("read WAL header: %w", err)
		}

		// this represents the body length
		dataLen := binary.BigEndian.Uint32(header[14:18])
		record := make([]byte, walEntryHeaderSize+int(dataLen))
		copy(record, header)

		// read body
		if _, err := io.ReadFull(r, record[walEntryHeaderSize:]); err != nil {
			return fmt.Errorf("read WAL record body: %w", err)
		}

		entry, err := DecodeWalEntry(record)
		if err != nil {
			return err
		}
		if err := fn(entry); err != nil {
			return err
		}
	}
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
