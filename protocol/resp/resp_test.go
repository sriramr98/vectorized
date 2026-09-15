package resp_test

import (
	"bufio"
	"errors"
	"strings"
	"testing"

	"github.com/sriramr98/vectorized/protocol/resp"
)

func TestReadRequest(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("*2\r\n$4\r\nPING\r\n$5\r\nhello\r\n"))

	args, err := resp.Parse(reader)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(args[0]), "PING"; got != want {
		t.Fatalf("first argument = %q, want %q", got, want)
	}
	if got, want := string(args[1]), "hello"; got != want {
		t.Fatalf("second argument = %q, want %q", got, want)
	}
}

func TestReadRequestRejectsMalformedFrame(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("*1\n$4\r\nPING\r\n"))

	_, err := resp.Parse(reader)
	var protocolError *resp.ProtocolError
	if !errors.As(err, &protocolError) {
		t.Fatalf("ReadRequest() error = %v, want protocol error", err)
	}
}
