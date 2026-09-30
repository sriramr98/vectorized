package wal

// InMemWal to be used only for testing.
type InMemWal struct {
	nextLSN uint64
}

func NewInMemWal() *InMemWal {
	return &InMemWal{}
}

func (w *InMemWal) Write(op OpType, data []byte) (uint64, error) {
	w.nextLSN++
	return w.nextLSN, nil
}

func (w *InMemWal) Replay(f func(WalEntry) error) (uint64, error) {
	return 0, nil
}

func (w *InMemWal) Close() error {
	return nil
}
