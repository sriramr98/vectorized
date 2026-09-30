package core

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/sriramr98/vectorized/db"
	"github.com/sriramr98/vectorized/db/wal"
	"github.com/sriramr98/vectorized/utils"
)

var ErrKeyNotFound = errors.New("key not found")

// Engine is the co-ordinator which co-ordinates the storage mechanism
type Engine struct {
	store        db.MutableMemTable
	storeCreator db.MutableStoreCreator
	walStore     wal.Wal
	mu           sync.RWMutex
	// reuse writeBuf during Set to reduce new buffer allocations
	writeBuf     *bytes.Buffer
	frozenStores []db.ReadOnlyMemTable
}

func NewEngine(creator db.MutableStoreCreator, walStore wal.Wal) *Engine {
	return &Engine{
		storeCreator: creator,
		walStore:     walStore,
		writeBuf:     bytes.NewBuffer([]byte{}),
		store:        creator(),
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

		if errors.Is(err, db.ErrMemTableMaxSize) {
			e.rotateMemTable()
			err = e.store.Apply(db.Record{
				Key:  string(key),
				Data: value,
				Lsn:  lsn,
			})
		}

		if err != nil {
			// wal record is written by this time. Wal is immutable, so the write is technically written.
			// but since it's not commited, future reads fail. This makes the db in an inconsistent state just for this key
			return fmt.Errorf("unable to set key %s but commited to wal", key)
		}

	}

	return nil

}

func (e *Engine) Get(key []byte) (value []byte, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	val, found := e.store.Get(string(key))
	if !found {
		val, found = e.searchFrozenStores(string(key))
		if !found {
			return []byte{}, ErrKeyNotFound
		}
	}

	if val.Deleted {
		return []byte{}, ErrKeyNotFound
	}

	return val.Data, nil
}

func (e *Engine) Delete(key []byte) (deleted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	buf := bytes.NewBuffer([]byte{})

	if err := utils.LengthEncodeBytes([][]byte{key}, buf); err != nil {
		return false, err
	}

	lsn, err := e.walStore.Write(wal.OpDelete, buf.Bytes())
	if err != nil {
		return false, err
	}

	if err = e.store.Apply(db.Record{
		Key:     string(key),
		Deleted: true,
		Lsn:     lsn,
	}); err != nil {
		return false, err
	}

	return true, nil

}

// Recover wipes the store and re-creates the store from walStore
// returns the number of wal records processed and error if any
func (e *Engine) Recover() (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	newStore := e.storeCreator()

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
			return newStore.Apply(db.Record{
				Key:     string(res[0]),
				Deleted: true,
				Lsn:     we.LSN,
			})
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

func (e *Engine) rotateMemTable() {
	frozen := e.store.Freeze()
	e.frozenStores = append(e.frozenStores, frozen)

	e.store = e.storeCreator()
}

func (e *Engine) searchFrozenStores(key string) (db.Record, bool) {

	// loop store in reverse since most recent data is in latest store
	for _, s := range slices.Backward(e.frozenStores) {

		rec, found := s.Get(key)
		if !found {
			continue
		}

		if rec.Deleted {
			return db.Record{}, false
		}

		return rec, true
	}

	return db.Record{}, false
}
