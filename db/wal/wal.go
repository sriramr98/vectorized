package wal

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/sriramr98/vectorized/utils"
	"golang.org/x/sys/unix"
)

const FILE_PREFIX = "wal_segment"

var (
	ErrUnknownFileName  = errors.New("unknown File name")
	ErrAlreadyClosed    = errors.New("wal already closed")
	ErrWalUnhealthy     = errors.New("wal is unhealthy")
	ErrInvalidOptions   = errors.New("invalid WAL options")
	ErrRecoveryRequired = errors.New("wal recovery is required")
	ErrAlreadyRecovered = errors.New("wal already recovered")
)

type walState uint8

const (
	walNeedsRecovery walState = iota
	walReady
	walUnhealthy
	walClosed
)

type Wal interface {
	Replay(func(WalEntry) error) (uint64, error)
	Write(op OpType, data []byte) error
	Close() error
}

type DurableWal struct {
	mu                 sync.Mutex
	dirpath            string
	opts               WalOptions
	segments           []*Segment
	openSegment        *Segment
	currentSegmentSize uint64
	dirLocker          *utils.LockedDir
	currentLSN         uint64
	state              walState
	logger             *slog.Logger
}

// NewWal creates a Wal with default options
func NewWal(logger *slog.Logger, dirpath string) (*DurableWal, error) {
	return NewWalWithOpts(dirpath, DefaultWalOpts, logger)
}

// NewWalWithOpts creates a Wal with custom options
func NewWalWithOpts(dirpath string, opts WalOptions, logger *slog.Logger) (*DurableWal, error) {
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	if logger == nil {
		logger = slog.Default()
	}
	dirpath, locker, err := prepareWalDirectory(dirpath)
	if err != nil {
		return nil, err
	}

	segments, err := discoverWalSegments(dirpath)
	if err != nil {
		return nil, errors.Join(err, locker.Release())
	}

	return &DurableWal{
		mu:                 sync.Mutex{},
		dirpath:            dirpath,
		opts:               opts,
		segments:           segments,
		dirLocker:          locker,
		currentSegmentSize: 0,
		logger:             logger,
		state:              walNeedsRecovery,
	}, nil
}

func prepareWalDirectory(dirpath string) (string, *utils.LockedDir, error) {
	dirpath, err := filepath.Abs(dirpath)
	if err != nil {
		return "", nil, err
	}
	if err := utils.EnsureDir(dirpath); err != nil {
		return "", nil, err
	}

	locker, err := utils.NewLockedDir(dirpath, "wal.lock")
	if err != nil {
		return "", nil, err
	}
	if err := locker.TryLock(); err != nil {
		return "", nil, errors.Join(err, locker.Release())
	}
	return dirpath, locker, nil
}

func (w *DurableWal) Write(opType OpType, data []byte) error {
	if opType != OpDelete && opType != OpSet {
		return ErrInvalidWalEntry
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case walClosed:
		return ErrAlreadyClosed
	case walUnhealthy:
		return ErrWalUnhealthy
	case walNeedsRecovery:
		return ErrRecoveryRequired
	}

	w.currentLSN += 1
	w.logger.Debug("writing entry to wal", "lsn", w.currentLSN)
	entry := WalEntryV1{
		LSN:    w.currentLSN,
		OpType: opType,
		Data:   data,
	}

	buf := bytes.NewBuffer([]byte{})
	dataLen, err := entry.Encode(buf)
	if err != nil {
		return err
	}

	if w.currentSegmentSize+uint64(dataLen) > utils.MBToBytes(w.opts.MaxFileSizeMB) {
		if err := w.rotateSegment(); err != nil {
			w.state = walUnhealthy
			return err
		}
	}

	// Segment is opened with O_APPEND, so writes always append and seek to end automatically
	n, err := writeRecord(w.openSegment.File, buf.Bytes())
	if err != nil {
		// A failed write may have appended only part of the record. Do not allow
		// another record to be appended until close and reopen repairs the tail.
		w.state = walUnhealthy
		return err
	}

	if err = w.openSegment.Sync(); err != nil {
		// The record may be buffered by the kernel but not durable on disk.
		w.state = walUnhealthy
		return err
	}

	w.currentSegmentSize += uint64(n)
	return nil
}

func writeRecord(writer io.Writer, record []byte) (int, error) {
	n, err := writer.Write(record)
	if err != nil {
		return n, err
	}
	if n != len(record) {
		return n, io.ErrShortWrite
	}
	return n, nil
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

	if w.dirLocker != nil {
		err := w.dirLocker.Release()
		if err != nil {
			return err
		}
		w.dirLocker = nil
	}

	w.state = walClosed
	return nil
}
