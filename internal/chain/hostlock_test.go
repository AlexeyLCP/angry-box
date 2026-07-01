package chain

// hostlock_test.go pins the per-host serialization contract of withHostLock,
// which is the single chokepoint all deploy entry points (CLI, web, auto-apply)
// must pass through so that two concurrent applies targeting the same node
// cannot interleave their SSH backup->write->restart sequences and corrupt
// the rollback chain (CTO-review C2).

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithHostLock_SerializesSameHost(t *testing.T) {
	var inFlight int32
	var maxInFlight int32
	var done int32

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			<-start
			withHostLock("node-X", func() struct{} {
				cur := atomic.AddInt32(&inFlight, 1)
				// Track the high-water mark of concurrent executions.
				for {
					m := atomic.LoadInt32(&maxInFlight)
					if cur <= m || atomic.CompareAndSwapInt32(&maxInFlight, m, cur) {
						break
					}
				}
				// Hold the lock briefly so a lack of serialization would be caught.
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inFlight, -1)
				atomic.AddInt32(&done, 1)
				return struct{}{}
			})
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&maxInFlight); got != 1 {
		t.Fatalf("withHostLock must serialize same-host access; saw %d concurrent executions", got)
	}
	if got := atomic.LoadInt32(&done); got != workers {
		t.Fatalf("all workers must run, got %d/%d", got, workers)
	}
}

func TestWithHostLock_DifferentHostsRunConcurrently(t *testing.T) {
	// Independent hosts must NOT be serialized against each other, otherwise
	// unrelated nodes would block needlessly.
	var concurrent int32
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	for _, id := range []string{"node-A", "node-B"} {
		go func(hostID string) {
			defer wg.Done()
			<-start
			withHostLock(hostID, func() struct{} {
				atomic.AddInt32(&concurrent, 1)
				time.Sleep(20 * time.Millisecond)
				return struct{}{}
			})
		}(id)
	}
	close(start)
	wg.Wait()

	// Both should have been able to run at the same time (>=2 concurrent).
	if got := atomic.LoadInt32(&concurrent); got < 2 {
		t.Fatalf("different hosts must run concurrently; only %d entered the critical section", got)
	}
}

func TestWithHostLock_ReturnsFuncValue(t *testing.T) {
	// The wrapper must propagate the inner function's return value, since the
	// applier relies on the error coming back from the locked section.
	got := withHostLock("node-R", func() error {
		return errSentinel
	})
	if got != errSentinel {
		t.Fatalf("withHostLock must return the inner func's error, got %v", got)
	}
}

var errSentinel = &hostLockTestErr{"sentinel"}

type hostLockTestErr struct{ msg string }

func (e *hostLockTestErr) Error() string { return e.msg }