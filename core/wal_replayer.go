package core

import (
	"encoding/binary"
	"fmt"
	"log/slog"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
)

func ReplayWal(walStore wal.Wal, store db.Store) error {
	return walStore.Replay(func(e wal.WalEntry) error {
		args, err := decodeRunLengthEncoded(e.Data)
		if err != nil {
			return err
		}

		slog.Default().Info("adding entry to store", "op", e.OpType)

		switch e.OpType {
		case wal.OpSet:
			if len(args) != 2 {
				return fmt.Errorf("SET WAL entry has %d arguments, want 2", len(args))
			}
			return store.Set(args[0], args[1])
		case wal.OpDelete:
			if len(args) != 1 {
				return fmt.Errorf("DELETE WAL entry has %d arguments, want 1", len(args))
			}
			_, err := store.Delete(args[0])
			return err
		default:
			return fmt.Errorf("unsupported WAL operation: %d", e.OpType)
		}
	})
}

func decodeRunLengthEncoded(data []byte) ([][]byte, error) {
	// Format: uint32 argument count, followed by uint32 length + bytes for
	// each argument. This must match the WAL argument encoder.
	const lengthSize = 4

	if len(data) < lengthSize {
		return nil, fmt.Errorf("encoded arguments: missing argument count")
	}

	argCount := binary.BigEndian.Uint32(data[:lengthSize])
	offset := lengthSize
	args := make([][]byte, 0, argCount)

	for i := range argCount {
		if len(data)-offset < lengthSize {
			return nil, fmt.Errorf("encoded argument %d: missing length", i)
		}

		argLen := uint64(binary.BigEndian.Uint32(data[offset : offset+lengthSize]))
		offset += lengthSize
		if argLen > uint64(len(data)-offset) {
			return nil, fmt.Errorf("encoded argument %d: length %d exceeds remaining data", i, argLen)
		}

		end := offset + int(argLen)
		args = append(args, append([]byte(nil), data[offset:end]...))
		offset = end
	}

	if offset != len(data) {
		return nil, fmt.Errorf("encoded arguments: %d trailing bytes", len(data)-offset)
	}

	return args, nil
}
