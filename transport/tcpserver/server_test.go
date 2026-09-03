package tcpserver_test

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sriramr98/vectorized/transport/tcpserver"
)

func TestServeClosesActiveConnectionsOnCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	handlerStarted := make(chan struct{})
	handlerStopped := make(chan struct{})
	server := tcpserver.New(listener, tcpserver.HandlerFunc(func(ctx context.Context, conn net.Conn) error {
		close(handlerStarted)
		defer close(handlerStopped)
		<-ctx.Done()
		return nil
	}), nil)

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx) }()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}

	cancel()

	select {
	case <-handlerStopped:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop")
	}
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("Serve() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not stop")
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if _, err := conn.Read(buffer); !errors.Is(err, io.EOF) {
		t.Fatalf("connection read error = %v, want EOF after server shutdown", err)
	}
}

func TestServeRejectsInvalidConfiguration(t *testing.T) {
	server := tcpserver.New(nil, tcpserver.HandlerFunc(func(context.Context, net.Conn) error {
		return nil
	}), nil)
	if err := server.Serve(context.Background()); err == nil {
		t.Fatal("Serve() error = nil, want configuration error")
	}
}
