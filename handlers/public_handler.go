package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/protocol/command"
	"github.com/sriramr98/vectorized/protocol/resp"
)

// PublicTCPHandler serves the RESP protocol over a public client connection.
type PublicTCPHandler struct {
	store  db.Store
	wal    *wal.Wal
	logger *slog.Logger
}

// NewPublicTCPHandler creates a public protocol handler backed by store.
func NewPublicTCPHandler(store db.Store, wal *wal.Wal, logger *slog.Logger) *PublicTCPHandler {
	return &PublicTCPHandler{store: store, wal: wal, logger: logger}
}

// ServeConn implements tcpserver.Handler.
func (h *PublicTCPHandler) ServeConn(ctx context.Context, conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		request, err := resp.ReadRequest(reader)
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

		parsedCommand, err := command.Decode(request)
		if err != nil {
			if writeErr := resp.WriteError(writer, "ERR "+err.Error()); writeErr != nil {
				return writeErr
			}
			if flushErr := writer.Flush(); flushErr != nil {
				return flushErr
			}
			continue
		}
		if err := h.handleCommand(writer, parsedCommand); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func (h *PublicTCPHandler) handleCommand(writer *bufio.Writer, parsed command.Command) error {

	if parsed.WalOpType() != wal.NoOp {
		encdedArg := parsed.LengthEncodedArgs()
		if err := h.wal.Write(encdedArg, parsed.WalOpType()); err != nil {
			h.logger.Error("unable to write op to wal", "error", err)
			return resp.WriteError(writer, "ERR internal server error")
		}
	}

	switch operation := parsed.(type) {
	case command.Ping:
		return resp.WriteSimpleString(writer, "PONG")
	case command.Set:
		if err := h.store.Set(operation.Key, operation.Value); err != nil {
			return resp.WriteError(writer, "ERR internal server error")
		}
		return resp.WriteSimpleString(writer, "OK")
	case command.Get:
		value, found, err := h.store.Get(operation.Key)
		if err != nil {
			return resp.WriteError(writer, "ERR internal server error")
		}
		if !found {
			return resp.WriteBulkString(writer, nil)
		}
		return resp.WriteBulkString(writer, value)
	case command.Delete:
		deleted, err := h.store.Delete(operation.Key)
		if err != nil {
			return resp.WriteError(writer, "ERR internal server error")
		}
		if deleted {
			return resp.WriteInteger(writer, 1)
		}
		return resp.WriteInteger(writer, 0)
	case command.Unknown:
		return resp.WriteError(writer, "ERR unknown or unsupported command")
	default:
		return resp.WriteError(writer, "ERR internal server error")
	}
}
