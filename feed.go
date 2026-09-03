package main

import (
	"fmt"
	"os"
	"sync"
)

// feedDepth is how much output the store may fall behind by before bytes are dropped.
//
// Two full read buffers. Enough to ride out one slow write without touching the shell, and small
// enough that a store which has stopped answering is noticed in the output rather than in memory.
const feedDepth = 2 * 32768

// storeFeed carries one session's output to its store without the shell waiting on it.
//
// S4-5: "The writer never blocks the shell. It is a subscriber to the same output every other
// subscriber reads, and a subscriber that cannot keep up loses bytes loudly rather than pausing the
// session that feeds it." The observers already work this way. The store did not — it was called
// inline from the pump, under the lock the pump holds, and a write could perform seven filesystem
// operations there. A hung network home stopped the pump, the pty buffer filled, and the shell
// blocked writing to its own terminal.
//
// A full queue drops rather than waits. What that costs is a hole in the stored output, which a
// restore reports as degraded — the shell not running costs the session.
type storeFeed struct {
	sink func(data []byte, through uint64) error
	name uint64

	mu      sync.Mutex
	ready   sync.Cond
	pending []chunk
	queued  int
	closed  bool
	drained sync.WaitGroup
}

type chunk struct {
	data    []byte
	through uint64
}

func newStoreFeed(name uint64, sink func(data []byte, through uint64) error) *storeFeed {
	feed := &storeFeed{sink: sink, name: name}
	feed.ready.L = &feed.mu
	feed.drained.Add(1)
	go feed.run()
	return feed
}

// offer hands the store one chunk and returns at once.
//
// The bytes are copied because the caller reuses its read buffer. A queue with no room for them
// drops them and says so: this is the loud loss S4-5 asks for, and it is the whole reason this does
// not block.
func (feed *storeFeed) offer(data []byte, through uint64) {
	feed.mu.Lock()
	defer feed.mu.Unlock()
	if feed.closed {
		return
	}
	if feed.queued+len(data) > feedDepth {
		fmt.Fprintf(os.Stderr,
			"soksak-sidecar-pty: session %d lost %d bytes of its record: the store is behind\n",
			feed.name, len(data))
		return
	}
	feed.pending = append(feed.pending, chunk{data: append([]byte(nil), data...), through: through})
	feed.queued += len(data)
	feed.ready.Signal()
}

func (feed *storeFeed) run() {
	defer feed.drained.Done()
	for {
		feed.mu.Lock()
		for len(feed.pending) == 0 && !feed.closed {
			feed.ready.Wait()
		}
		if len(feed.pending) == 0 {
			feed.mu.Unlock()
			return
		}
		next := feed.pending[0]
		feed.pending = feed.pending[1:]
		feed.queued -= len(next.data)
		feed.mu.Unlock()

		if err := feed.sink(next.data, next.through); err != nil {
			fmt.Fprintf(os.Stderr,
				"soksak-sidecar-pty: session %d lost %d bytes of its record: %v\n",
				feed.name, len(next.data), err)
		}
	}
}

// close stops the feed and waits for what it already accepted to reach the store.
//
// The stop write follows this, and it records how far the session got. Writing it while chunks were
// still queued would claim output the store does not hold.
func (feed *storeFeed) close() {
	feed.mu.Lock()
	if feed.closed {
		feed.mu.Unlock()
		return
	}
	feed.closed = true
	feed.ready.Broadcast()
	feed.mu.Unlock()
	feed.drained.Wait()
}
