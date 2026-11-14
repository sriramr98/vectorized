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
var ErrEngineClosed = errors.New("engine already closed")
var ErrEngineUnhealthy = errors.New("engine is unhealthy and inconsistent. Please restart the server")

type EngineState int

const (
	Ready EngineState = iota
	Closed
	Unhealthy // this state occurs when wal write goes through but memtable writes fail which leaves the DB in an inconsistent state, so we stop accepting writes until server restarts and inconsistency resolved.
)

// Engine is the co-ordinator which co-ordinates the storage mechanism
type Engine struct {
	store        db.MutableMemTable
	storeCreator db.MutableStoreCreator
	walStore     wal.Wal
	mu           sync.RWMutex
	// reuse writeBuf during Set to reduce new buffer allocations
	writeBuf     *bytes.Buffer
	frozenStores []db.ReadOnlyMemTable
	state        EngineState
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

	rec := db.Record{
		Key:  string(key),
		Data: value,

		//temporaril set 0 for size calculation. Later set LSN from wal
		Lsn: 0,
	}

	return e.apply(&rec, wal.OpSet, false)
}

func (e *Engine) Get(key []byte) (value []byte, err error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if e.state == Closed {
		return nil, ErrEngineClosed
	}

	val, found := e.store.Get(string(key))
	if !found {
		val, found = e.searchFrozenStores(string(key))
		if !found {
			return nil, ErrKeyNotFound
		}
	}

	if val.Deleted {
		return nil, ErrKeyNotFound
	}

	return val.Data, nil
}

func (e *Engine) Delete(key []byte) (deleted bool, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec := db.Record{
		Key:     string(key),
		Deleted: true,
		Lsn:     0, // Actual LSN set in e.apply
	}

	if err := e.apply(&rec, wal.OpDelete, false); err != nil {
		return false, err
	}

	return true, nil
}

// Recover wipes the store and re-creates the store from walStore
// returns the number of wal records processed and error if any
//
//	can be called on a closed engine to restart
func (e *Engine) Recover() (uint64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.clear(); err != nil {
		return 0, err
	}

	count, err := e.walStore.Replay(func(we wal.WalEntry) error {
		var rec db.Record

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

			rec = db.Record{
				Key:  string(key),
				Data: value,
				Lsn:  we.LSN,
			}

		case wal.OpDelete:
			res, err := utils.DecodeLengthEncodedBytes(we.Data)
			if err != nil {
				return err
			}

			if len(res) != 1 {
				return fmt.Errorf("delete expects exactly one argument but got %d", len(res))
			}
			rec = db.Record{
				Key:     string(res[0]),
				Deleted: true,
				Lsn:     we.LSN,
			}
		default:
			return fmt.Errorf("unknown op type in wal %d", we.OpType)
		}

		return e.apply(&rec, we.OpType, true)
	})

	// we open irrespecitve of error since Recover can be partially recovered state as well.
	e.state = Ready

	return count, err
}

func (e *Engine) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.walStore.Close(); err != nil {
		return err
	}

	e.state = Closed
	return nil
}

// thread-unsafe clear
func (e *Engine) clear() error {
	e.frozenStores = nil
	return e.store.Clear()
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

// apply internallly applies a DEL/SET operation in the engine. Expects caller to lock the engine properly.
func (e *Engine) apply(rec *db.Record, op wal.OpType, skipWal bool) error {
	if e.state == Closed {
		return ErrEngineClosed
	}

	if e.state == Unhealthy {
		return ErrEngineUnhealthy
	}

	e.writeBuf.Reset()

	if err := e.store.CanAccomodate(*rec); err != nil {
		if errors.Is(err, db.ErrRecordDoesntFit) {
			e.rotateMemTable()
		} else {
			return err
		}
	}

	if !skipWal {
		lsn, err := e.writeToWal(*rec, op)
		if err != nil {
			return err
		}

		rec.Lsn = lsn
	}

	if err := e.store.Apply(*rec); err != nil {
		// wal record is written by this time. Wal is immutable, so the write is technically written.
		// but since it's not commited, future reads fail. This makes the db in an inconsistent state just for this key
		e.state = Unhealthy
		return fmt.Errorf("unable to set key %s but commited to wal", rec.Key)
	}

	return nil
}

func (e *Engine) writeToWal(rec db.Record, op wal.OpType) (uint64, error) {
	switch op {
	case wal.OpDelete:
		if err := utils.LengthEncodeBytes([][]byte{[]byte(rec.Key)}, e.writeBuf); err != nil {
			return 0, err
		}
	case wal.OpSet:
		if err := utils.LengthEncodeBytes([][]byte{[]byte(rec.Key), rec.Data}, e.writeBuf); err != nil {
			return 0, err
		}
	}

	return e.walStore.Write(op, e.writeBuf.Bytes())
}
