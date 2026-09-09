package wal

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type WalOptions struct {
	maxFileSizeMB uint64        // max size of open segment File before it's rotated
	maxFileCount  uint          // max no of wal Files to be kept in the wal folder. Older ones can be deleted at any time
	alwaysSync    bool          // always fsync when a wal entry is written. Expensive but highly durable. Either alwaysSync takes higher priority on syncDuration
	syncInterval  time.Duration // time internal between two fsync calls of the same segment. Discarded if alwaysSync is set to true
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
