package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
)

type OpType uint8

const (
	NoOp     OpType = 0
	OpSet    OpType = 1
	OpDelete OpType = 2
)

const (
	walEntryVersionV1 OpType = 1
	// checksum + version + LSN + operation + data length
	walEntryHeaderSize = 4 + 1 + 8 + 1 + 4
	// MaxWalRecordDataBytes bounds allocations made from an on-disk length. It
	// accommodates the largest current SET payload: two 64 MiB RESP arguments
	// plus the argument-count and two argument-length fields.
	MaxWalRecordDataBytes = 2*(64<<20) + 3*8
)

var (
	ErrInvalidWalEntry       = errors.New("invalid wal entry")
	ErrUnsupportedWalVersion = errors.New("unsupported wal entry version")
	ErrWalRecordTooLarge     = errors.New("wal record data exceeds maximum size")
)

// WalEntryV1 is the first version of the WAL entry format.
type WalEntryV1 struct {
	LSN    uint64
	OpType OpType
	Data   []byte
}

// WalEntry is retained as the default WAL entry type. It currently aliases V1.
type WalEntry = WalEntryV1

// Encode converts a V1 WalEntry into bytes for writing to disk.
// Returns the total number of bytes and
func (we WalEntryV1) Encode(buf *bytes.Buffer) (int, error) {
	if buf == nil {
		return -1, errors.New("wal entry: nil buffer")
	}
	if len(we.Data) > MaxWalRecordDataBytes {
		return -1, fmt.Errorf("%w: %d bytes", ErrWalRecordTooLarge, len(we.Data))
	}

	start := buf.Len()

	// reserve space for checksum which gets calculated at the end
	if err := binary.Write(buf, binary.BigEndian, uint32(0)); err != nil {
		return -1, err
	}

	if err := binary.Write(buf, binary.BigEndian, uint8(walEntryVersionV1)); err != nil {
		return -1, err
	}

	// Write headers
	if err := binary.Write(buf, binary.BigEndian, we.LSN); err != nil {
		return -1, err
	}

	if err := binary.Write(buf, binary.BigEndian, we.OpType); err != nil {
		return -1, err
	}

	dataLen := uint32(len(we.Data))
	if err := binary.Write(buf, binary.BigEndian, dataLen); err != nil {
		return -1, err
	}

	if _, err := buf.Write(we.Data); err != nil {
		return -1, err
	}

	serialized := buf.Bytes()[start:]

	// Don't include the reserved 4 bytes for CRC when calculating the CRC.
	checksum := crc32.ChecksumIEEE(serialized[4:])

	// Write the checksum to the beginning of the record
	binary.BigEndian.PutUint32(serialized[:4], checksum)

	totalLength := len(serialized)

	return totalLength, nil
}

// DecodeWalEntry decodes a versioned WAL entry.
func DecodeWalEntry(data []byte) (WalEntry, error) {
	if len(data) < 5 {
		return WalEntry{}, fmt.Errorf("%w: record is too short", ErrInvalidWalEntry)
	}

	switch data[4] {
	case uint8(walEntryVersionV1):
		entry, err := DecodeWalEntryV1(data)
		return entry, err
	default:
		return WalEntry{}, fmt.Errorf("%w: %d", ErrUnsupportedWalVersion, data[4])
	}
}

// DecodeWalEntryV1 decodes one complete V1 WAL entry.
func DecodeWalEntryV1(data []byte) (WalEntryV1, error) {
	if len(data) < walEntryHeaderSize {
		return WalEntryV1{}, fmt.Errorf("%w: record is too short", ErrInvalidWalEntry)
	}

	dataLen := binary.BigEndian.Uint32(data[14:18])
	if dataLen > MaxWalRecordDataBytes {
		return WalEntryV1{}, fmt.Errorf("%w: %d bytes", ErrWalRecordTooLarge, dataLen)
	}

	storedChecksum := binary.BigEndian.Uint32(data[:4])
	calculatedChecksum := crc32.ChecksumIEEE(data[4:])
	if storedChecksum != calculatedChecksum {
		return WalEntryV1{}, fmt.Errorf("%w: checksum mismatch", ErrInvalidWalEntry)
	}

	if data[4] != uint8(walEntryVersionV1) {
		return WalEntryV1{}, fmt.Errorf("%w: %d", ErrUnsupportedWalVersion, data[4])
	}

	expectedLen := uint64(walEntryHeaderSize) + uint64(dataLen)
	if expectedLen != uint64(len(data)) {
		return WalEntryV1{}, fmt.Errorf("%w: data length does not match record length", ErrInvalidWalEntry)
	}

	entry := WalEntryV1{
		LSN:    binary.BigEndian.Uint64(data[5:13]),
		OpType: OpType(data[13]),
		Data:   append([]byte(nil), data[18:]...),
	}
	if entry.OpType != OpSet && entry.OpType != OpDelete {
		return WalEntryV1{}, fmt.Errorf("%w: unknown operation %d", ErrInvalidWalEntry, entry.OpType)
	}

	return entry, nil
}
