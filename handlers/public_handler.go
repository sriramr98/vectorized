package handlers

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/sriramr98/vectorized/protocol/resp"
)

func PublicTcpHandler(ctx context.Context, conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		arguments, err := resp.ReadCommand(reader)
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

		if err := handleCommand(writer, arguments); err != nil {
			return err
		}
		if err := writer.Flush(); err != nil {
			return err
		}
	}
}

func handleCommand(writer *bufio.Writer, arguments [][]byte) error {
	if len(arguments) == 1 && strings.EqualFold(string(arguments[0]), "PING") {
		return resp.WriteSimpleString(writer, "PONG")
	}

	return resp.WriteError(writer, "ERR unknown or unsupported command")
}
