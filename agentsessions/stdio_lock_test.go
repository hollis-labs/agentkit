package agentsessions

import (
	"context"
	"errors"
	"math/rand/v2"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStdioLockMixedCancellationStress(t *testing.T) {
	var lock stdioLock
	var workers sync.WaitGroup
	var inCritical atomic.Int32
	var overlap atomic.Bool
	for worker := 0; worker < 12; worker++ {
		workers.Add(1)
		go func(seed uint64) {
			defer workers.Done()
			rng := rand.New(rand.NewPCG(seed, 42))
			for step := 0; step < 100; step++ {
				if rng.IntN(3) == 0 {
					lock.Lock()
				} else {
					ctx, cancel := context.WithCancel(context.Background())
					// Mix pre-canceled waiters and cancellation racing acquisition. All
					// cancellation workers are joined, even if acquisition wins the race.
					done := make(chan struct{})
					if rng.IntN(2) == 0 {
						cancel()
						close(done)
					} else {
						go func() { cancel(); close(done) }()
					}
					err := lock.LockContext(ctx)
					<-done
					cancel()
					if err != nil {
						if !errors.Is(err, context.Canceled) {
							t.Errorf("lock error = %v", err)
						}
						continue
					}
				}
				if inCritical.Add(1) != 1 {
					overlap.Store(true)
				}
				runtime.Gosched()
				inCritical.Add(-1)
				lock.Unlock()
			}
		}(uint64(worker + 1))
	}
	finished := make(chan struct{})
	go func() { workers.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("lock workers stuck")
	}
	if overlap.Load() {
		t.Fatal("critical sections overlapped")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lock.LockContext(ctx); err != nil {
		t.Fatalf("lock no longer acquirable: %v", err)
	}
	if inCritical.Load() != 0 {
		t.Error("critical section retained")
	}
	lock.Unlock()
}
