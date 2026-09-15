package utils

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

var ErrUnableToWriteCompleteData error = errors.New("unable to write data completely")

const (
	maxLengthEncodedArguments    = 1_024
	maxLengthEncodedArgumentSize = 64 << 20
)

// takes a slice of bytes where every element is a data to be length encoded into the final data
func LengthEncodeBytes(data [][]byte, w io.Writer) error {
	//<len_args><len><bytes><len><bytes><len3><bytes>

	arg_count := len(data)
	if arg_count == 0 {
		return errors.New("cannot encode empty data")
	}

	if err := binary.Write(w, binary.BigEndian, uint64(arg_count)); err != nil {
		return err
	}

	for _, d := range data {
		if d == nil {
			return errors.New("cannot encode nil byte")
		}
		dLen := len(d)
		if err := binary.Write(w, binary.BigEndian, uint64(dLen)); err != nil {
			return err
		}
		n, err := w.Write(d)
		if err != nil {
			return err
		}

		if n != dLen {
			return io.ErrShortWrite
		}
	}

	return nil
}

func DecodeLengthEncodedBytes(data []byte) ([][]byte, error) {
	// Format: uint64 argument count, followed by uint64 length + bytes for
	// each argument. This must match the WAL argument encoder.
	const lengthSize = 8

	if len(data) < lengthSize {
		return nil, fmt.Errorf("encoded arguments: missing argument count")
	}

	argCount := binary.BigEndian.Uint64(data[:lengthSize])
	if argCount > maxLengthEncodedArguments {
		return nil, fmt.Errorf("encoded arguments: count %d exceeds maximum %d", argCount, maxLengthEncodedArguments)
	}
	// Even zero-length arguments require an eight-byte length field. Reject an
	// impossible count before using it as a slice capacity.
	if argCount > uint64((len(data)-lengthSize)/lengthSize) {
		return nil, fmt.Errorf("encoded arguments: count %d exceeds available length fields", argCount)
	}
	offset := lengthSize
	args := make([][]byte, 0, argCount)

	for i := range argCount {
		if len(data)-offset < lengthSize {
			return nil, fmt.Errorf("encoded argument %d: missing length", i)
		}

		argLen := uint64(binary.BigEndian.Uint64(data[offset : offset+lengthSize]))
		offset += lengthSize
		if argLen > maxLengthEncodedArgumentSize {
			return nil, fmt.Errorf("encoded argument %d: length %d exceeds maximum %d", i, argLen, maxLengthEncodedArgumentSize)
		}
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
