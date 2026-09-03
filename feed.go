package main

import (
	"fmt"
	"os"
	"sync"
	"time"
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

// drainDeadline is how long a stop waits for the queue to reach the store.
//
// Long enough for a slow disk to finish a queue this small, short enough that a store which has
// stopped answering does not hold the stop. What waiting longer would buy is the tail of one
// record; what it costs is every session after this one in the stop's order.
const drainDeadline = 2 * time.Second

// close stops the feed and waits for what it already accepted to reach the store.
func (feed *storeFeed) close() { feed.closeWithin(drainDeadline) }

// closeWithin stops the feed and waits up to a bound for what it already accepted.
//
// The stop write follows this and records how far the session got, so a drain that finished means
// the stored output reaches that coordinate. A drain that did not is reported and the stop is
// written anyway: the owner did stop on purpose, and a restore compares the recorded coordinate
// against what the output reaches and answers degraded — which is the true state, where refusing
// the mark would claim this owner crashed.
//
// Bounded because the stop walks every session on one goroutine. An unbounded wait stops at the
// first session whose store is not answering, and no session after it gets a stop write at all.
func (feed *storeFeed) closeWithin(within time.Duration) {
	feed.mu.Lock()
	if feed.closed {
		feed.mu.Unlock()
		return
	}
	feed.closed = true
	feed.ready.Broadcast()
	feed.mu.Unlock()

	drained := make(chan struct{})
	go func() { feed.drained.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(within):
		feed.mu.Lock()
		left := feed.queued
		feed.mu.Unlock()
		fmt.Fprintf(os.Stderr,
			"soksak-sidecar-pty: session %d stopped with %d bytes still on the way to its record\n",
			feed.name, left)
	}
}
