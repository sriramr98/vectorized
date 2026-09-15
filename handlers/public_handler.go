package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/sriramr98/vectorized/core"
	"github.com/sriramr98/vectorized/protocol/command"
	"github.com/sriramr98/vectorized/protocol/resp"
)

// PublicTCPHandler serves the RESP protocol over a public client connection.
type PublicTCPHandler struct {
	engine *core.Engine
	logger *slog.Logger
}

// NewPublicTCPHandler creates a public protocol handler backed by store.
func NewPublicTCPHandler(engine *core.Engine, logger *slog.Logger) *PublicTCPHandler {
	if logger == nil {
		logger = &slog.Logger{}
	}
	return &PublicTCPHandler{logger: logger, engine: engine}
}

// ServeConn implements tcpserver.Handler.
func (h *PublicTCPHandler) ServeConn(ctx context.Context, conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		parsed_req, err := resp.Parse(reader)
		if err != nil {
			if errors.Is(err, io.EOF) || ctx.Err() != nil {
				return nil
			}
			if writeErr := resp.WriteError(writer, "ERR protocol error"); writeErr != nil {
				return writeErr
			}
			if flushErr := writer.Flush(); flushErr != nil {
				return flushErr
			}
			return fmt.Errorf("read command: %w", err)
		}

		request, err := command.Decode(parsed_req)
		if err != nil {
			if writeErr := resp.WriteError(writer, "ERR "+err.Error()); writeErr != nil {
				return writeErr
			}
			if flushErr := writer.Flush(); flushErr != nil {
				return flushErr
			}
			continue
		}
		if err := h.handleCommand(writer, request); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func (h *PublicTCPHandler) handleCommand(w *bufio.Writer, req command.Request) error {
	switch req.Op {
	case command.OpPing:
		return resp.WriteSimpleString(w, "PONG")
	case command.OpSet:
		if err := h.engine.Set(req.Args[0], req.Args[1]); err != nil {
			return resp.WriteError(w, "ERR internal server error")
		}
		return resp.WriteSimpleString(w, "OK")
	case command.OpGet:
		value, err := h.engine.Get(req.Args[0])
		if err != nil {
			if errors.Is(err, core.ErrKeyNotFound) {
				return resp.WriteBulkString(w, nil)
			}
			return resp.WriteError(w, "ERR internal server error")
		}
		return resp.WriteBulkString(w, value)
	case command.OpDelete:
		deleted, err := h.engine.Delete(req.Args[0])
		if err != nil {
			return resp.WriteError(w, "ERR internal server error")
		}
		if deleted {
			return resp.WriteInteger(w, 1)
		}
		return resp.WriteInteger(w, 0)
	case command.OpUnknown:
		return resp.WriteError(w, "ERR unknown or unsupported command")
	default:
		return resp.WriteError(w, "ERR internal server error")
	}
}
