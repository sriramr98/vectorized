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
	store    db.MutableMemTable
	walStore wal.Wal
	mu       sync.RWMutex
	// reuse writeBuf during Set to reduce new buffer allocations
	writeBuf *bytes.Buffer
}

func NewEngine(store db.MutableMemTable, walStore wal.Wal) *Engine {
	return &Engine{
		store:    store,
		walStore: walStore,
		writeBuf: bytes.NewBuffer([]byte{}),
	}
}

// Set acknowleges writes when write to both memory and wal suceeds
func (e *Engine) Set(key []byte, value []byte) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.writeBuf.Reset()

	if err := utils.LengthEncodeBytes([][]byte{key, value}, e.writeBuf); err != nil {
		return err
	}

	lsn, err := e.walStore.Write(wal.OpSet, e.writeBuf.Bytes())
	if err != nil {
		return err
	}

	if err := e.store.Apply(db.Record{
		Key:  string(key),
		Data: value,
		Lsn:  lsn,
	}); err != nil {
		// wal record is written by this time. Wal is immutable, so the write is technically written.
		// but since it's not commited, future reads fail. This makes the db in an inconsistent state just for this key
		return fmt.Errorf("unable to set key %s but commited to wal", key)
	}

	return nil

}

func (e *Engine) Get(key []byte) (value []byte, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	val, found := e.store.Get(string(key))
	if !found {
		return []byte{}, ErrKeyNotFound
	}
	return val.Data, nil
}

func (e *Engine) Delete(key []byte) (deleted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, found := e.store.Get(string(key))
	if !found {
		return false, nil
	}

	buf := bytes.NewBuffer([]byte{})

	if err := utils.LengthEncodeBytes([][]byte{key}, buf); err != nil {
		return false, err
	}

	if _, err := e.walStore.Write(wal.OpDelete, buf.Bytes()); err != nil {
		return false, err
	}

	return e.store.Delete(string(key)), nil

}

// Recover wipes the store and re-creates the store from walStore
// returns the number of wal records processed and error if any
func (e *Engine) Recover() (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newStore := db.NewMemoryStore()

	count, err := e.walStore.Replay(func(we wal.WalEntry) error {
		switch we.OpType {
		case wal.OpSet:
			res, err := utils.DecodeLengthEncodedBytes(we.Data)
			if err != nil {
				return err
			}

			if len(res) != 2 {
				return fmt.Errorf("set expects two arguments but got %d", len(res))
			}

			key := res[0]
			value := res[1]

			return newStore.Apply(db.Record{
				Key:  string(key),
				Data: value,
				Lsn:  we.LSN,
			})

		case wal.OpDelete:
			res, err := utils.DecodeLengthEncodedBytes(we.Data)
			if err != nil {
				return err
			}

			if len(res) != 1 {
				return fmt.Errorf("delete expects exactly one argument but got %d", len(res))
			}

			//TODO: Do we need tombstone when we recover?
			newStore.Delete(string(res[0]))
			return nil
		default:
			return fmt.Errorf("unknown op type in wal %d", we.OpType)
		}
	})

	if err == nil {
		e.store.Clear()
		e.store = newStore
	}

	return count, err
}

func (e *Engine) Close() error {
	return e.walStore.Close()
}
