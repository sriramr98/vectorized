// Package command converts validated wire requests into database operations.
package command

import (
	"fmt"
	"strings"

	"github.com/sriramr98/vectorized/protocol/resp"
)

// Command is a validated database operation.
type Command interface {
	command()
}

// Ping checks that the server is reachable.
type Ping struct{}

// Set stores Value under Key.
type Set struct {
	Key   []byte
	Value []byte
}

// Get reads Key.
type Get struct {
	Key []byte
}

// Delete removes Key.
type Delete struct {
	Key []byte
}

// Unknown represents a well-formed command not supported by this server.
type Unknown struct{}

func (Ping) command()    {}
func (Set) command()     {}
func (Get) command()     {}
func (Delete) command()  {}
func (Unknown) command() {}

// ArityError reports a supported command with the wrong number of arguments.
type ArityError struct {
	Name     string
	Expected int
	Actual   int
}

func (e *ArityError) Error() string {
	return fmt.Sprintf("wrong number of arguments for '%s' command", e.Name)
}

// Decode recognizes and validates one RESP request. Returned commands own
// their key and value bytes, so they remain safe if the request is reused.
func Decode(request resp.Request) (Command, error) {
	arguments := request.Arguments
	if len(arguments) == 0 {
		return nil, &ArityError{Name: "command", Expected: 1, Actual: 0}
	}

	switch strings.ToUpper(string(arguments[0])) {
	case "PING":
		if err := requireArgumentCount("PING", arguments, 1); err != nil {
			return nil, err
		}
		return Ping{}, nil
	case "SET":
		if err := requireArgumentCount("SET", arguments, 3); err != nil {
			return nil, err
		}
		return Set{Key: clone(arguments[1]), Value: clone(arguments[2])}, nil
	case "GET":
		if err := requireArgumentCount("GET", arguments, 2); err != nil {
			return nil, err
		}
		return Get{Key: clone(arguments[1])}, nil
	case "DEL":
		if err := requireArgumentCount("DEL", arguments, 2); err != nil {
			return nil, err
		}
		return Delete{Key: clone(arguments[1])}, nil
	default:
		return Unknown{}, nil
	}
}

func requireArgumentCount(name string, arguments [][]byte, expected int) error {
	if len(arguments) != expected {
		return &ArityError{Name: name, Expected: expected, Actual: len(arguments)}
	}
	return nil
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
