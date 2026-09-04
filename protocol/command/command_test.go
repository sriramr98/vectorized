package command_test

import (
	"errors"
	"testing"

	"github.com/sriramr98/vectorized/protocol/command"
	"github.com/sriramr98/vectorized/protocol/resp"
)

func TestDecodeReturnsTypedCommands(t *testing.T) {
	parsed, err := command.Decode(resp.Request{Arguments: [][]byte{[]byte("SET"), []byte("key"), []byte("value")}})
	if err != nil {
		t.Fatal(err)
	}

	set, ok := parsed.(command.Set)
	if !ok {
		t.Fatalf("Decode() command = %T, want command.Set", parsed)
	}
	if got, want := string(set.Key), "key"; got != want {
		t.Fatalf("Set.Key = %q, want %q", got, want)
	}
	if got, want := string(set.Value), "value"; got != want {
		t.Fatalf("Set.Value = %q, want %q", got, want)
	}
}

func TestDecodeValidatesArity(t *testing.T) {
	_, err := command.Decode(resp.Request{Arguments: [][]byte{[]byte("GET")}})
	var arityError *command.ArityError
	if !errors.As(err, &arityError) {
		t.Fatalf("Decode() error = %v, want arity error", err)
	}
	if got, want := arityError.Error(), "wrong number of arguments for 'GET' command"; got != want {
		t.Fatalf("ArityError = %q, want %q", got, want)
	}
}

func TestDecodeCopiesArguments(t *testing.T) {
	key := []byte("key")
	parsed, err := command.Decode(resp.Request{Arguments: [][]byte{[]byte("GET"), key}})
	if err != nil {
		t.Fatal(err)
	}
	key[0] = 'x'

	get := parsed.(command.Get)
	if got, want := string(get.Key), "key"; got != want {
		t.Fatalf("Get.Key = %q, want %q", got, want)
	}
}
