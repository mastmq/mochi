package mqtt

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestClientsGetByListenerRecursiveRLock is the regression test for the
// deadlock reported upstream as mochi-mqtt/server#488.
//
// GetByListener used to take RLock and then call Len, which takes RLock
// again. sync.RWMutex documents that a reader arriving after a writer is
// blocked waits for that writer, so if a Lock lands between the two
// acquisitions the second RLock waits for the writer, the writer waits for
// the first RLock to be released, and neither ever moves.
//
// The race window is a handful of instructions wide, so this drives it rather
// than trying to hit it exactly: one goroutine scans by listener while
// another churns the map. Pre-fix this wedges within a few hundred
// iterations; post-fix it runs clean.
//
// A deadlock cannot be caught with a plain assertion, because the goroutine
// that hangs never returns to report anything. The work runs in the
// background and the test fails on a deadline instead, which leaves the stuck
// goroutines behind but lets the rest of the package finish and report.
func TestClientsGetByListenerRecursiveRLock(t *testing.T) {
	const (
		clients    = 64
		iterations = 20000
		deadline   = 20 * time.Second
	)

	cl := NewClients()

	for i := 0; i < clients; i++ {
		id := fmt.Sprintf("client-%d", i)
		cl.Add(&Client{ID: id, Net: ClientConnection{Listener: "t1"}})
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		var wg sync.WaitGroup
		wg.Add(2)

		// The reader: Server.Close takes this path through
		// closeListenerClients when a listener is shut down.
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				cl.GetByListener("t1")
			}
		}()

		// The writer: attachClient takes this path when a connection arrives
		// for an id that is already known.
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				id := fmt.Sprintf("client-%d", i%clients)
				cl.Delete(id)
				cl.Add(&Client{ID: id, Net: ClientConnection{Listener: "t1"}})
			}
		}()

		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(deadline):
		t.Fatalf("deadlocked: GetByListener and Delete made no progress in %s, "+
			"which is the recursive RLock in GetByListener (upstream #488)", deadline)
	}
}
