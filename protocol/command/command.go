// Package command converts validated wire requests into database operations.
package command

import (
	"fmt"
	"strings"
)

// ArityError reports a supported command with the wrong number of arguments.
type ArityError struct {
	Name     string
	Expected int
	Actual   int
}

func (e *ArityError) Error() string {
	return fmt.Sprintf("wrong number of arguments for '%s' command", e.Name)
}

type Op int

const (
	OpUnknown Op = iota
	OpGet
	OpSet
	OpDelete
	OpPing
)

type Request struct {
	Op   Op
	Args [][]byte
}

// Decodes a valid Resp budy and parses into a Request
func Decode(arguments [][]byte) (Request, error) {
	if len(arguments) == 0 {
		return Request{}, &ArityError{Name: "command", Expected: 1, Actual: 0}
	}

	switch strings.ToUpper(string(arguments[0])) {
	case "PING":
		if err := requireArgumentCount("PING", arguments, 1); err != nil {
			return Request{}, err
		}
		return Request{Op: OpPing, Args: [][]byte{}}, nil
	case "SET":
		if err := requireArgumentCount("SET", arguments, 3); err != nil {
			return Request{}, err
		}
		return Request{Op: OpSet, Args: cloned(arguments[1:])}, nil
	case "GET":
		if err := requireArgumentCount("GET", arguments, 2); err != nil {
			return Request{}, err
		}
		return Request{Op: OpGet, Args: cloned(arguments[1:])}, nil
	case "DEL":
		if err := requireArgumentCount("DEL", arguments, 2); err != nil {
			return Request{}, err
		}
		return Request{Op: OpDelete, Args: cloned(arguments[1:])}, nil
	default:
		return Request{}, nil
	}
}

func requireArgumentCount(name string, arguments [][]byte, expected int) error {
	if len(arguments) != expected {
		return &ArityError{Name: name, Expected: expected, Actual: len(arguments)}
	}
	return nil
}

func cloned(args [][]byte) [][]byte {
	res := [][]byte{}
	for _, arg := range args {
		res = append(res, clone(arg))
	}

	return res
}

func clone(value []byte) []byte {
	return append([]byte(nil), value...)
}
