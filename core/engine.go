package core

import (
	"bytes"
	"errors"
	"fmt"
	"sync"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/utils"
)

var ErrKeyNotFound = errors.New("key not found")

// Engine is the co-ordinator which co-ordinates the storage mechanism
type Engine struct {
	store    db.MemStore
	walStore wal.Wal
	mu       sync.RWMutex
}

func NewEngine(store db.MemStore, walStore wal.Wal) *Engine {
	return &Engine{
		store:    store,
		walStore: walStore,
	}
}

// Set acknowleges writes when write to both memory and wal suceeds
func (e *Engine) Set(key []byte, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.store.Set(key, value); err != nil {
		return err
	}

	buf := bytes.NewBuffer([]byte{})

	if err := utils.LengthEncodeBytes([][]byte{key, value}, buf); err != nil {
		return err
	}

	if err := e.walStore.Write(wal.OpSet, buf.Bytes()); err != nil {
		e.store.Delete(key)
		return err
	}

	return nil

}

func (e *Engine) Get(key []byte) (value []byte, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	val, found := e.store.Get(key)
	if !found {
		return []byte{}, ErrKeyNotFound
	}
	return val, nil
}

func (e *Engine) Delete(key []byte) (deleted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, found := e.store.Get(key)
	if !found {
		return false, ErrKeyNotFound
	}

	buf := bytes.NewBuffer([]byte{})

	if err := utils.LengthEncodeBytes([][]byte{key}, buf); err != nil {
		return false, err
	}

	if err := e.walStore.Write(wal.OpDelete, buf.Bytes()); err != nil {
		return false, err
	}

	return e.store.Delete(key), nil

}

// Recover wipes the store and re-creates the store from walStore
// returns the number of wal records processed and error if any
func (e *Engine) Recover() (uint64, error) {
	e.store.Clear()

	count, err := e.walStore.Replay(func(we wal.WalEntry) error {
		switch we.OpType {
		case wal.OpSet:
			res, err := utils.DecodeLengthEncodedBytes(we.Data)
			if err != nil {
				return err
			}

			if len(res) < 2 {
				return fmt.Errorf("set expects atleast two arguments but got %d", len(res))
			}

			key := res[0]
			value := res[1]

			return e.store.Set(key, value)

		case wal.OpDelete:
			res, err := utils.DecodeLengthEncodedBytes(we.Data)
			if err != nil {
				return err
			}

			if len(res) != 1 {
				return fmt.Errorf("delete expects exactly one argument but got %d", len(res))
			}

			e.store.Delete(res[0])
			return nil
		default:
			return fmt.Errorf("unknown op type in wal %d", we.OpType)
		}
	})

	return count, err
}

func (e *Engine) Close() error {
	return e.walStore.Close()
}
