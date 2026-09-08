package wal

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sriramr98/vectorized/utils"
	"golang.org/x/sys/unix"
)

const FILE_PREFIX = "wal_segment"

var (
	ErrUnknownFileName = errors.New("unknown file name")
	ErrAlreadyClosed   = errors.New("wal already closed")
)

type WalOptions struct {
	maxFileSizeMB uint64        // max size of open segment file before it's rotated
	maxFileCount  uint          // max no of wal files to be kept in the wal folder. Older ones can be deleted at any time
	alwaysSync    bool          // always fsync when a wal entry is written. Expensive but highly durable. Either alwaysSync takes higher priority on syncDuration
	syncInterval  time.Duration // time internal between two fsync calls of the same segment. Discarded if alwaysSync is set to true
}

type WalFileName string

func NewWalFileName(idx int) string {
	return fmt.Sprintf("%s__%d", FILE_PREFIX, idx)
}

// Parse the name, validates and returns the index
func (name WalFileName) Parse() (int, error) {
	parts := strings.Split(string(name), "__")
	if len(parts) != 2 {
		return 0, ErrUnknownFileName
	}

	if parts[0] != FILE_PREFIX {
		return 0, ErrUnknownFileName
	}

	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}

	if idx < 0 {
		return 0, errors.New("Error un-parseable wal index")
	}

	return idx, nil
}

type Segment struct {
	idx  int
	file *os.File
	path string
	mu   *sync.RWMutex
}

func NewSegment(file *os.File, path string, idx int) *Segment {
	return &Segment{
		idx:  idx,
		file: file,
		path: path,
		mu:   &sync.RWMutex{},
	}
}

func (s *Segment) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.file != nil {
		if err := s.file.Sync(); err != nil {
			return err
		}

		return s.file.Close()
	}

	return nil
}

type Wal struct {
	mu          *sync.Mutex
	dirpath     string
	opts        WalOptions
	segments    []*Segment
	openSegment *Segment
	dirLocker   *utils.LockedDir
	closed      bool
}

func NewWal(dirpath string, opts WalOptions) (*Wal, error) {
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

	files, err := os.ReadDir(dirpath)
	if err != nil {
		return nil, err
	}

	var segments []*Segment
	var lastMaxId int

	for _, entry := range files {
		if entry.IsDir() {
			continue
		}

		name := WalFileName(entry.Name())
		idx, err := name.Parse()
		if err != nil {
			// unknown file name. Skip
			continue
		}

		// we don't need to open file since we won't be writing to them
		segments = append(segments, NewSegment(nil, filepath.Join(dirpath, entry.Name()), idx))
		lastMaxId = max(lastMaxId, idx)
	}

	slices.SortFunc(segments, func(a, b *Segment) int {
		return cmp.Compare(a.idx, b.idx)
	})

	// we always create a new file for open segment even if previous file wasn't used to it's full capacity
	openSegment, err := createWalFile(dirpath, lastMaxId+1)
	if err != nil {
		return nil, err
	}

	return &Wal{
		mu:          &sync.Mutex{},
		dirpath:     dirpath,
		opts:        opts,
		segments:    segments,
		openSegment: openSegment,
		dirLocker:   locker,
	}, nil
}

func (w *Wal) Write(data []byte, opType OpType) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.closed {
		return ErrAlreadyClosed
	}

	entry := WalEntryV1{
		LSN:    1, //TODO: Make dynamic
		OpType: opType,
		Data:   data,
	}

	w.rotateSegmentIfRequired(entry)

	buf := bytes.NewBuffer([]byte{})
	if err := entry.Encode(buf); err != nil {
		return err
	}

	// Segment is opened with O_APPEND, so writes always append and seek to end automatically
	_, err := w.openSegment.file.Write(buf.Bytes())
	return err
}

func (w *Wal) rotateSegmentIfRequired(entry WalEntry) {

}

func (w *Wal) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.openSegment != nil && w.openSegment.file != nil {
		// release lock before closing segments
		if err := unix.Flock(int(w.openSegment.file.Fd()), unix.LOCK_UN); err != nil {
			return err
		}

		if err := w.openSegment.Close(); err != nil {
			return err
		}
	}

	for _, segment := range w.segments {
		if err := segment.Close(); err != nil {
			return err
		}
	}

	err := w.dirLocker.Release()
	if err != nil {
		return err
	}

	w.closed = true
	return nil
}

func createWalFile(dirpath string, idx int) (*Segment, error) {
	file_name := NewWalFileName(idx)
	file_path := filepath.Join(dirpath, file_name)

	file, err := os.OpenFile(file_path, os.O_RDWR|os.O_CREATE|os.O_APPEND, utils.PermFileReadWriteOwnerOnly)

	if err != nil {
		return nil, err
	}

	return NewSegment(file, file_path, idx), nil
}
