package utils

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestLengthEncodeBytesWireFormat(t *testing.T) {
	input := [][]byte{
		[]byte("key"),
		[]byte{},
		{0x00, 0xff},
	}

	var got bytes.Buffer
	if err := LengthEncodeBytes(input, &got); err != nil {
		t.Fatalf("LengthEncodeBytes() error = %v", err)
	}

	want := make([]byte, 0, 8+8+3+8+8+2)
	want = binary.BigEndian.AppendUint64(want, 3) // argument count
	want = binary.BigEndian.AppendUint64(want, 3)
	want = append(want, "key"...)
	want = binary.BigEndian.AppendUint64(want, 0)
	want = binary.BigEndian.AppendUint64(want, 2)
	want = append(want, 0x00, 0xff)
	if !bytes.Equal(got.Bytes(), want) {
		t.Fatalf("encoded bytes = %x, want %x", got.Bytes(), want)
	}
}

func TestLengthEncodingErrors(t *testing.T) {
	tests := []struct {
		name string
		data [][]byte
	}{
		{
			name: "nil_byte",
			data: [][]byte{nil},
		},
		{
			name: "empty_bytes",
			data: [][]byte{},
		},
	}

	w := bytes.NewBuffer([]byte{})

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := LengthEncodeBytes(test.data, w)
			if err == nil {
				t.Fatal("expected error but got none")
			}
		})
	}
}

func TestLengthEncodeBytesRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		data [][]byte
	}{
		{"multiple binary arguments", [][]byte{[]byte("key"), {0x00, 0xff}, []byte("value")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var encoded bytes.Buffer
			if err := LengthEncodeBytes(tt.data, &encoded); err != nil {
				t.Fatalf("LengthEncodeBytes() error = %v", err)
			}

			got, err := DecodeLengthEncodedBytes(encoded.Bytes())
			if err != nil {
				t.Fatalf("DecodeLengthEncodedBytes() error = %v", err)
			}
			if len(got) != len(tt.data) {
				t.Fatalf("decoded %d arguments, want %d", len(got), len(tt.data))
			}
			for i := range tt.data {
				if !bytes.Equal(got[i], tt.data[i]) {
					t.Errorf("argument %d = %x, want %x", i, got[i], tt.data[i])
				}
			}
		})
	}
}

func TestLengthEncodeBytesReturnsHeaderWriteError(t *testing.T) {
	err := LengthEncodeBytes([][]byte{[]byte("value")}, shortFirstWriteWriter{})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("LengthEncodeBytes() error = %v, want %v", err, io.ErrShortWrite)
	}
}

func TestLengthEncodeBytesReturnsEachHeaderWriteError(t *testing.T) {
	wantErr := errors.New("header write failed")
	tests := []struct {
		name        string
		failedWrite int
	}{
		{"argument count", 1},
		{"argument length", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writer := &failSpecificWriteWriter{failedWrite: tt.failedWrite, err: wantErr}
			err := LengthEncodeBytes([][]byte{[]byte("value")}, writer)
			if !errors.Is(err, wantErr) {
				t.Fatalf("LengthEncodeBytes() error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestLengthEncodeBytesReturnsPayloadWriteFailures(t *testing.T) {
	writeErr := errors.New("payload write failed")
	tests := []struct {
		name string
		w    io.Writer
		want error
	}{
		{"writer error", &failAfterWritesWriter{remainingSuccessfulWrites: 2, err: writeErr}, writeErr},
		{"short write", &failAfterWritesWriter{remainingSuccessfulWrites: 2}, io.ErrShortWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := LengthEncodeBytes([][]byte{[]byte("value")}, tt.w)
			if !errors.Is(err, tt.want) {
				t.Fatalf("LengthEncodeBytes() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDecodeLengthEncodedBytesRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"missing argument count", nil},
		{"truncated argument length", append(uint64s(1), 0, 0, 0, 0)},
		{"declared length exceeds remaining data", append(uint64s(1, 2), 'x')},
		{"trailing data", append(uint64s(0), 'x')},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeLengthEncodedBytes(tt.data)
			if err == nil {
				t.Fatalf("DecodeLengthEncodedBytes() = %x, nil error; want malformed-input error", got)
			}
		})
	}
}

func TestDecodeLengthEncodedBytesRejectsAllocationBounds(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"excessive argument count", uint64s(maxLengthEncodedArguments + 1)},
		{"count exceeds available length fields", uint64s(2, 0)},
		{"oversized argument", uint64s(1, maxLengthEncodedArgumentSize+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeLengthEncodedBytes(tt.data); err == nil {
				t.Fatal("DecodeLengthEncodedBytes() error = nil, want allocation-bound error")
			}
		})
	}
}

func TestDecodeLengthEncodedBytesReturnsIndependentArguments(t *testing.T) {
	encoded := append(uint64s(1, 3), 'k', 'e', 'y')
	got, err := DecodeLengthEncodedBytes(encoded)
	if err != nil {
		t.Fatalf("DecodeLengthEncodedBytes() error = %v", err)
	}

	got[0][0] = 'K'
	if encoded[16] != 'k' {
		t.Fatalf("decoded argument aliases encoded input: input = %q, want %q", encoded[16:], "key")
	}
}

func uint64s(values ...uint64) []byte {
	encoded := make([]byte, 0, len(values)*8)
	for _, value := range values {
		encoded = binary.BigEndian.AppendUint64(encoded, value)
	}
	return encoded
}

type shortFirstWriteWriter struct {
	writes int
}

func (w shortFirstWriteWriter) Write(p []byte) (int, error) {
	if w.writes == 0 {
		w.writes++
		return len(p) - 1, nil
	}
	return len(p), nil
}

type failAfterWritesWriter struct {
	remainingSuccessfulWrites int
	err                       error
}

type failSpecificWriteWriter struct {
	writes      int
	failedWrite int
	err         error
}

func (w *failSpecificWriteWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failedWrite {
		return 0, w.err
	}
	return len(p), nil
}

func (w *failAfterWritesWriter) Write(p []byte) (int, error) {
	if w.remainingSuccessfulWrites > 0 {
		w.remainingSuccessfulWrites--
		return len(p), nil
	}
	if w.err != nil {
		return 0, w.err
	}
	return len(p) - 1, nil
}

var (
	_ io.Writer = shortFirstWriteWriter{}
	_ io.Writer = (*failAfterWritesWriter)(nil)
)
