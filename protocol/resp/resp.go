// Package resp implements the small RESP2 subset used by Vectorized clients.
package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

const (
	maxArguments = 1_024
	maxBulkBytes = 64 << 20
)

// ProtocolError means a request cannot be safely parsed.
type ProtocolError struct {
	message string
}

func (e *ProtocolError) Error() string {
	return e.message
}

// ReadCommand reads one RESP array containing bulk-string command arguments.
// Other RESP types are intentionally unsupported for now.
//
// ex. For input SET abc 123 2x -> output ["SET", "abc", "123", "2x"].
func ReadCommand(reader *bufio.Reader) ([][]byte, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if prefix != '*' {
		return nil, protocolError("expected array")
	}

	argumentCount, err := readLength(reader, "array length", maxArguments)
	if err != nil {
		return nil, err
	}
	if argumentCount == 0 {
		return nil, protocolError("command array is empty")
	}

	arguments := make([][]byte, argumentCount)
	for i := range arguments {
		prefix, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		if prefix != '$' {
			return nil, protocolError("expected bulk string")
		}

		length, err := readLength(reader, "bulk string length", maxBulkBytes)
		if err != nil {
			return nil, err
		}
		argument := make([]byte, length)
		if _, err := io.ReadFull(reader, argument); err != nil {
			return nil, err
		}
		if err := expectCRLF(reader); err != nil {
			return nil, err
		}
		arguments[i] = argument
	}

	return arguments, nil
}

// WriteSimpleString writes a RESP simple string. Callers must pass a value
// without CR or LF characters.
func WriteSimpleString(writer *bufio.Writer, value string) error {
	_, err := writer.WriteString("+" + value + "\r\n")
	return err
}

// WriteError writes a RESP error. Callers must pass a value without CR or LF
// characters.
func WriteError(writer *bufio.Writer, value string) error {
	_, err := writer.WriteString("-" + value + "\r\n")
	return err
}

func readLength(reader *bufio.Reader, name string, maximum int) (int, error) {
	line, err := readLine(reader)
	if err != nil {
		return 0, err
	}

	value, err := strconv.ParseInt(string(line), 10, 64)
	if err != nil || value < 0 || value > int64(maximum) {
		return 0, protocolError("invalid " + name)
	}
	return int(value), nil
}

func readLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return nil, protocolError("line is too long")
		}
		return nil, err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return nil, protocolError("expected CRLF")
	}
	return line[:len(line)-2], nil
}

func expectCRLF(reader *bufio.Reader) error {
	terminator := make([]byte, 2)
	if _, err := io.ReadFull(reader, terminator); err != nil {
		return err
	}
	if terminator[0] != '\r' || terminator[1] != '\n' {
		return protocolError("expected bulk string terminator")
	}
	return nil
}

func protocolError(message string) error {
	return &ProtocolError{message: fmt.Sprintf("RESP protocol error: %s", message)}
}
