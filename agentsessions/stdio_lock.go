package agentsessions

import (
	"context"
	"sync"
)

// stdioLock is a zero-value usable input mutex with cancellable acquisition.
// No goroutine survives a canceled wait to acquire the lock later.
type stdioLock struct {
	once  sync.Once
	token chan struct{}
}

func (m *stdioLock) init() {
	m.once.Do(func() {
		m.token = make(chan struct{}, 1)
		m.token <- struct{}{}
	})
}

func (m *stdioLock) Lock()   { m.init(); <-m.token }
func (m *stdioLock) Unlock() { m.token <- struct{}{} }
func (m *stdioLock) LockContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.init()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-m.token:
		if err := ctx.Err(); err != nil {
			m.Unlock()
			return err
		}
		return nil
	}
}
