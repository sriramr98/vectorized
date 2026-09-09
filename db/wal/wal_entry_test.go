package wal

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func TestWalEntryEncodeDecodeV1(t *testing.T) {
	tests := []struct {
		name  string
		entry WalEntry
	}{
		{"set empty", WalEntry{LSN: 0, OpType: OpSet}},
		{"delete empty", WalEntry{LSN: 1, OpType: OpDelete}},
		{"ordinary data", WalEntry{LSN: 42, OpType: OpSet, Data: []byte("hello")}},
		{"binary data", WalEntry{LSN: 99, OpType: OpDelete, Data: []byte{0, 1, 2, 0xff, '\n'}}},
		{"maximum lsn", WalEntry{LSN: ^uint64(0), OpType: OpSet, Data: []byte("max")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if _, err := tt.entry.Encode(&buf); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}

			got, err := DecodeWalEntry(buf.Bytes())
			if err != nil {
				t.Fatalf("DecodeWalEntry() error = %v", err)
			}
			if got.LSN != tt.entry.LSN || got.OpType != tt.entry.OpType || !bytes.Equal(got.Data, tt.entry.Data) {
				t.Fatalf("decoded entry = %#v, want %#v", got, tt.entry)
			}
		})
	}
}

func TestWalEntryEncodeV1WireFormat(t *testing.T) {
	entry := WalEntry{LSN: 0x0102030405060708, OpType: OpDelete, Data: []byte{0xaa, 0xbb}}
	var buf bytes.Buffer
	if _, err := entry.Encode(&buf); err != nil {
		t.Fatal(err)
	}

	want := make([]byte, walEntryHeaderSize+len(entry.Data))
	want[4] = byte(walEntryVersionV1)
	binary.BigEndian.PutUint64(want[5:13], entry.LSN)
	want[13] = byte(entry.OpType)
	binary.BigEndian.PutUint32(want[14:18], uint32(len(entry.Data)))
	copy(want[18:], entry.Data)
	binary.BigEndian.PutUint32(want[:4], crc32.ChecksumIEEE(want[4:]))

	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("encoded bytes = %x, want %x", buf.Bytes(), want)
	}
}

func TestWalEntryEncodeAppendsWithoutOverwritingBuffer(t *testing.T) {
	entry := WalEntry{LSN: 7, OpType: OpSet, Data: []byte("value")}
	prefix := []byte("prefix")
	var buf bytes.Buffer
	buf.Write(prefix)

	if _, err := entry.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes()[:len(prefix)], prefix) {
		t.Fatalf("prefix changed: got %q, want %q", buf.Bytes()[:len(prefix)], prefix)
	}

	got, err := DecodeWalEntry(buf.Bytes()[len(prefix):])
	if err != nil {
		t.Fatalf("DecodeWalEntry() error = %v", err)
	}
	if got.LSN != entry.LSN || got.OpType != entry.OpType || !bytes.Equal(got.Data, entry.Data) {
		t.Fatalf("decoded entry = %#v, want %#v", got, entry)
	}
}

func TestDecodeWalEntryRejectsInvalidRecords(t *testing.T) {
	entry := WalEntry{LSN: 1, OpType: OpSet, Data: []byte("data")}
	var buf bytes.Buffer
	if _, err := entry.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	record := buf.Bytes()

	tests := []struct {
		name string
		data []byte
		want error
	}{
		{"empty", nil, ErrInvalidWalEntry},
		{"truncated", record[:len(record)-1], ErrInvalidWalEntry},
		{"corrupt checksum", append([]byte(nil), record...), ErrInvalidWalEntry},
		{"unsupported version", append([]byte(nil), record...), ErrUnsupportedWalVersion},
		{"unknown operation", append([]byte(nil), record...), ErrInvalidWalEntry},
	}
	tests[2].data[18] ^= 1
	tests[3].data[4] = 2
	binary.BigEndian.PutUint32(tests[3].data[:4], crc32.ChecksumIEEE(tests[3].data[4:]))
	// Recalculate the checksum so this case reaches operation validation.
	tests[4].data[13] = 99
	binary.BigEndian.PutUint32(tests[4].data[:4], crc32.ChecksumIEEE(tests[4].data[4:]))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeWalEntry(tt.data)
			if !errors.Is(err, tt.want) {
				t.Fatalf("DecodeWalEntry() error = %v, want wrapped %v", err, tt.want)
			}
		})
	}
}

func FuzzWalEntryEncodeDecodeV1(f *testing.F) {
	f.Add(uint64(0), uint8(OpSet), []byte(nil))
	f.Add(uint64(42), uint8(OpDelete), []byte("hello"))
	f.Add(^uint64(0), uint8(OpSet), []byte{0, 1, 2, 0xff})

	f.Fuzz(func(t *testing.T, lsn uint64, op uint8, data []byte) {
		if op != uint8(OpSet) && op != uint8(OpDelete) {
			op = uint8(OpSet)
		}
		entry := WalEntry{LSN: lsn, OpType: OpType(op), Data: data}
		var buf bytes.Buffer
		if _, err := entry.Encode(&buf); err != nil {
			t.Fatal(err)
		}
		got, err := DecodeWalEntry(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		if got.LSN != entry.LSN || got.OpType != entry.OpType || !bytes.Equal(got.Data, entry.Data) {
			t.Fatalf("decoded entry = %#v, want %#v", got, entry)
		}
	})
}
