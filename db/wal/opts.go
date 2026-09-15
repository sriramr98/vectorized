package wal

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type WalOptions struct {
	MaxFileSizeMB uint64 // maximum segment size before rotation
}

func (w WalOptions) Validate() error {
	if w.MaxFileSizeMB == 0 {
		return fmt.Errorf("%w: MaxFileSizeMB must be greater than zero", ErrInvalidOptions)
	}

	return nil
}

var DefaultWalOpts = WalOptions{
	MaxFileSizeMB: 64,
}

type WalFileName string

func NewWalFileName(idx uint64) string {
	return fmt.Sprintf("%s__%d", FILE_PREFIX, idx)
}

// Parse the name, validates and returns the index
func (name WalFileName) Parse() (int, error) {
	parts := strings.Split(string(name), "__")
	if len(parts) != 2 {
		return 0, ErrUnknownFileName
	}

	if parts[0] != FILE_PREFIX {
		return 0, ErrUnknownFileName
	}

	idx, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, err
	}

	if idx < 0 {
		return 0, errors.New("Error un-parseable wal index")
	}

	return idx, nil
}
