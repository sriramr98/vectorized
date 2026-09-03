// Package tcpserver provides the lifecycle for TCP listeners used by Vectorized.
package tcpserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
)

// Handler serves one accepted connection. It should return when ctx is done or
// when the peer disconnects.
type Handler interface {
	ServeConn(context.Context, net.Conn) error
}

// HandlerFunc adapts a function to a Handler.
type HandlerFunc func(context.Context, net.Conn) error

// ServeConn implements Handler.
func (f HandlerFunc) ServeConn(ctx context.Context, conn net.Conn) error {
	return f(ctx, conn)
}

// Server owns one listener and coordinates a graceful shutdown of its clients.
// It is protocol-agnostic: public clients and future replication clients can
// use the same server with different Handlers.
type Server struct {
	listener net.Listener
	handler  Handler
	logger   *slog.Logger

	mu      sync.Mutex
	closing bool
	conns   map[net.Conn]struct{}
	clients sync.WaitGroup
}

// New creates a server around an already-created listener.
func New(listener net.Listener, handler Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}

	return &Server{
		listener: listener,
		handler:  handler,
		logger:   logger,
		conns:    make(map[net.Conn]struct{}),
	}
}

// Serve accepts connections until ctx is canceled or the listener fails. On
// shutdown it closes the listener and all active connections, then waits for
// every Handler to return.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		return errors.New("tcpserver: listener is nil")
	}
	if s.handler == nil {
		return errors.New("tcpserver: handler is nil")
	}

	stopClosingListener := context.AfterFunc(ctx, func() {
		_ = s.listener.Close()
	})
	defer stopClosingListener()
	defer s.shutdown()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Temporary() {
				s.logger.Warn("temporary accept failure", "error", err)
				continue
			}
			return fmt.Errorf("accept TCP connection: %w", err)
		}

		if !s.track(conn) {
			_ = conn.Close()
			continue
		}

		s.clients.Add(1)
		go s.serveConn(ctx, conn)
	}
}

func (s *Server) serveConn(ctx context.Context, conn net.Conn) {
	defer s.clients.Done()
	defer s.untrack(conn)
	defer conn.Close()

	if err := s.handler.ServeConn(ctx, conn); err != nil && !errors.Is(err, io.EOF) && ctx.Err() == nil {
		s.logger.Debug("connection handler stopped", "remote_address", conn.RemoteAddr(), "error", err)
	}
}

func (s *Server) track(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing {
		return false
	}
	s.conns[conn] = struct{}{}
	return true
}

func (s *Server) untrack(conn net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, conn)
}

func (s *Server) shutdown() {
	s.mu.Lock()
	if s.closing {
		s.mu.Unlock()
		return
	}
	s.closing = true
	connections := make([]net.Conn, 0, len(s.conns))
	for conn := range s.conns {
		connections = append(connections, conn)
	}
	s.mu.Unlock()

	_ = s.listener.Close()
	for _, conn := range connections {
		_ = conn.Close()
	}
	s.clients.Wait()
}
