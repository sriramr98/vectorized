package handlers_test

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/handlers"
)

func TestPublicTCPHandlerRespondsToPingAndUnknownCommands(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- handlers.NewPublicTCPHandler(db.NewMemoryStore(), wal.NewInMemWal(), nil).ServeConn(context.Background(), serverConn)
		_ = serverConn.Close()
	}()

	assertRequestResponse(t, clientConn, "*1\r\n$4\r\nPING\r\n", "+PONG\r\n")
	assertRequestResponse(t, clientConn, "*1\r\n$7\r\nNOTHING\r\n", "-ERR unknown or unsupported command\r\n")

	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("PublicTcpHandler() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after client disconnect")
	}
}

func TestPublicTCPHandlerReportsMalformedRequests(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- handlers.NewPublicTCPHandler(db.NewMemoryStore(), wal.NewInMemWal(), nil).ServeConn(context.Background(), serverConn)
		_ = serverConn.Close()
	}()

	assertRequestResponse(t, clientConn, "PING\r\n", "-ERR protocol error\r\n")
	if err := clientConn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := clientConn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("connection read error = %v, want EOF", err)
	}

	select {
	case err := <-handlerDone:
		if err == nil {
			t.Fatal("PublicTcpHandler() error = nil, want malformed request error")
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after malformed request")
	}
}

func TestPublicTCPHandlerStoresValues(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	handlerDone := make(chan error, 1)
	go func() {
		handlerDone <- handlers.NewPublicTCPHandler(db.NewMemoryStore(), wal.NewInMemWal(), nil).ServeConn(context.Background(), serverConn)
		_ = serverConn.Close()
	}()

	assertRequestResponse(t, clientConn, "*3\r\n$3\r\nSET\r\n$3\r\nkey\r\n$5\r\nvalue\r\n", "+OK\r\n")
	assertRequestResponse(t, clientConn, "*2\r\n$3\r\nSET\r\n$3\r\nkey\r\n", "-ERR wrong number of arguments for 'SET' command\r\n")
	assertRequestResponse(t, clientConn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n", "$5\r\nvalue\r\n")
	assertRequestResponse(t, clientConn, "*2\r\n$3\r\nDEL\r\n$3\r\nkey\r\n", ":1\r\n")
	assertRequestResponse(t, clientConn, "*2\r\n$3\r\nGET\r\n$3\r\nkey\r\n", "$-1\r\n")

	if err := clientConn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-handlerDone:
		if err != nil {
			t.Fatalf("PublicTCPHandler() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after client disconnect")
	}
}

func assertRequestResponse(t *testing.T, conn net.Conn, request, response string) {
	t.Helper()
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(response))
	if _, err := io.ReadFull(conn, buffer); err != nil {
		t.Fatal(err)
	}
	if got := string(buffer); got != response {
		t.Fatalf("response = %q, want %q", got, response)
	}
}
