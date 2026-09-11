package wal

// InMemWal to be used only for testing.
type InMemWal struct {
}

func NewInMemWal() *InMemWal {
	return &InMemWal{}
}

func (w *InMemWal) Write(op OpType, data []byte) error {
	return nil
}

func (w *InMemWal) Replay(f func(WalEntry) error) error {
	return nil
}
