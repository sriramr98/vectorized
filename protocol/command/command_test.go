package command_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/sriramr98/vectorized/protocol/command"
)

func TestDecodeRecognizesSupportedCommands(t *testing.T) {
	tests := []struct {
		name      string
		arguments [][]byte
		wantOp    command.Op
		wantArgs  [][]byte
	}{
		{"PING", [][]byte{[]byte("PING")}, command.OpPing, nil},
		{"SET", [][]byte{[]byte("SET"), []byte("key"), []byte("value")}, command.OpSet, [][]byte{[]byte("key"), []byte("value")}},
		{"GET is case insensitive", [][]byte{[]byte("get"), []byte("key")}, command.OpGet, [][]byte{[]byte("key")}},
		{"DEL", [][]byte{[]byte("DEL"), []byte("key")}, command.OpDelete, [][]byte{[]byte("key")}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := command.Decode(tt.arguments)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if got.Op != tt.wantOp {
				t.Fatalf("Decode().Op = %v, want %v", got.Op, tt.wantOp)
			}
			assertArgumentsEqual(t, got.Args, tt.wantArgs)
		})
	}
}

func TestDecodeRejectsIncorrectArity(t *testing.T) {
	tests := []struct {
		name      string
		arguments [][]byte
		wantName  string
		wantCount int
	}{
		{"missing command", nil, "command", 0},
		{"PING with argument", [][]byte{[]byte("PING"), []byte("extra")}, "PING", 2},
		{"SET missing value", [][]byte{[]byte("SET"), []byte("key")}, "SET", 2},
		{"GET missing key", [][]byte{[]byte("GET")}, "GET", 1},
		{"DEL with extra key", [][]byte{[]byte("DEL"), []byte("one"), []byte("two")}, "DEL", 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := command.Decode(tt.arguments)
			var arityError *command.ArityError
			if !errors.As(err, &arityError) {
				t.Fatalf("Decode() error = %v, want ArityError", err)
			}
			if arityError.Name != tt.wantName || arityError.Actual != tt.wantCount {
				t.Fatalf("ArityError = %#v, want Name %q and Actual %d", arityError, tt.wantName, tt.wantCount)
			}
		})
	}
}

func TestDecodeReturnsUnknownOpForUnsupportedCommand(t *testing.T) {
	got, err := command.Decode([][]byte{[]byte("EXISTS"), []byte("key")})
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if got.Op != command.OpUnknown || len(got.Args) != 0 {
		t.Fatalf("Decode() = %#v, want unknown request with no arguments", got)
	}
}

func TestDecodeCopiesArguments(t *testing.T) {
	key := []byte("key")
	request, err := command.Decode([][]byte{[]byte("GET"), key})
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 'x'

	assertArgumentsEqual(t, request.Args, [][]byte{[]byte("key")})
}

func assertArgumentsEqual(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argument count = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("argument %d = %q, want %q", i, got[i], want[i])
		}
	}
}
