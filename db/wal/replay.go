package wal

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sriramr98/vectorized/utils"
)

// Replay performs the WAL's one-time startup recovery. It validates and emits
// every record present at startup, establishes the next LSN, and only then
// opens the active segment for writes.
func (w *DurableWal) Replay(fn func(WalEntry) error) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case walClosed:
		return 0, ErrAlreadyClosed
	case walUnhealthy:
		return 0, ErrWalUnhealthy
	case walReady:
		return 0, ErrAlreadyRecovered
	}

	count, latestLSN, err := w.replaySegments(fn)
	if err != nil {
		return 0, err
	}
	if err := w.activateWritableSegment(); err != nil {
		return 0, fmt.Errorf("activate WAL segment: %w", err)
	}

	w.currentLSN = latestLSN
	w.state = walReady
	return count, nil
}

func (w *DurableWal) replaySegments(fn func(WalEntry) error) (uint64, uint64, error) {
	var count uint64
	var latestLSN uint64

	for i, segment := range w.segments {
		file, err := os.Open(segment.path)
		if err != nil {
			return 0, 0, fmt.Errorf("open WAL segment %q: %w", segment.path, err)
		}

		allowTruncatedTail := i == len(w.segments)-1

		validSize, truncatedTail, replayErr := replaySegment(file, allowTruncatedTail, func(entry WalEntry) error {
			if entry.LSN <= latestLSN {
				return fmt.Errorf("WAL LSN %d is not greater than previous LSN %d", entry.LSN, latestLSN)
			}
			if err := fn(entry); err != nil {
				return err
			}
			latestLSN = entry.LSN
			count++
			return nil
		})
		closeErr := file.Close()
		if replayErr != nil {
			return 0, 0, fmt.Errorf("replay WAL segment %q: %w", segment.path, replayErr)
		}
		if closeErr != nil {
			return 0, 0, fmt.Errorf("close WAL segment %q: %w", segment.path, closeErr)
		}
		if truncatedTail {
			if err := truncateAndSyncSegment(segment.path, validSize); err != nil {
				return 0, 0, fmt.Errorf("truncate torn WAL tail in segment %q: %w", segment.path, err)
			}
		}
	}

	return count, latestLSN, nil
}

func replaySegment(r io.Reader, allowTruncatedTail bool, fn func(WalEntry) error) (int64, bool, error) {
	header := make([]byte, walEntryHeaderSize)
	var validSize int64
	for {
		n, err := io.ReadFull(r, header)
		if err == io.EOF && n == 0 {
			return validSize, false, nil
		}
		if err != nil {
			if allowTruncatedTail && errors.Is(err, io.ErrUnexpectedEOF) {
				return validSize, true, nil
			}
			return validSize, false, fmt.Errorf("read WAL header: %w", err)
		}

		dataLen := binary.BigEndian.Uint32(header[14:18])
		if dataLen > MaxWalRecordDataBytes {
			return validSize, false, fmt.Errorf("%w: %d bytes", ErrWalRecordTooLarge, dataLen)
		}
		record := make([]byte, walEntryHeaderSize+int(dataLen))
		copy(record, header)

		if _, err := io.ReadFull(r, record[walEntryHeaderSize:]); err != nil {
			if allowTruncatedTail && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
				return validSize, true, nil
			}
			return validSize, false, fmt.Errorf("read WAL record body: %w", err)
		}

		entry, err := DecodeWalEntry(record)
		if err != nil {
			return validSize, false, err
		}
		if err := fn(entry); err != nil {
			return validSize, false, err
		}
		validSize += int64(len(record))
	}
}

func truncateAndSyncSegment(path string, size int64) error {
	file, err := os.OpenFile(path, os.O_RDWR, utils.PermFileReadWriteOwnerOnly)
	if err != nil {
		return err
	}
	if err := file.Truncate(size); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
