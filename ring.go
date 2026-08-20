package main

import (
	"sync"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

// ring is one session's output, held so a client that arrives late can still read what it missed.
//
// A shell outlives an application generation, which is the whole reason this process exists. What
// the shell printed while nobody was attached has to be somewhere, and this is where.
//
// Every byte carries a sequence that never restarts for the life of the session. A client says
// where it got to and is answered from there — or told the ring no longer holds it, which is a
// different answer from "there is nothing", and a client that cannot tell those apart repaints a
// blank screen and reports a session with no history.
type ring struct {
	mu sync.Mutex
	// bytes is the retained tail. floor is the sequence its first byte carries, so
	// floor+len(bytes) is the sequence the next byte written will carry.
	bytes []byte
	floor uint64
	// capacity is how much tail is kept. It bounds memory per session, and it is the window a
	// reattaching client can be served from — a client further back than this is restarted rather
	// than served bytes that no longer line up with what it already drew.
	capacity int
	// acked is how far the attached client says it has taken. The reader pauses above the high
	// watermark and resumes at the low one.
	acked uint64
	// waiters are readers parked until either more bytes arrive or the session ends.
	waiters []chan struct{}
	ended   bool
}

func newRing(capacity int) *ring {
	if capacity <= 0 {
		capacity = ptycontract.HighWatermark
	}
	return &ring{capacity: capacity}
}

// write appends output and answers the sequence the next byte will carry.
func (r *ring) write(data []byte) uint64 {
	r.mu.Lock()
	r.bytes = append(r.bytes, data...)
	if overflow := len(r.bytes) - r.capacity; overflow > 0 {
		r.bytes = r.bytes[overflow:]
		r.floor += uint64(overflow)
	}
	next := r.floor + uint64(len(r.bytes))
	r.wakeLocked()
	r.mu.Unlock()
	return next
}

// end releases every parked reader. A reader that stays parked on a dead session is a client that
// waits for a byte nobody will write, and nothing about that state states itself.
func (r *ring) end() {
	r.mu.Lock()
	r.ended = true
	r.wakeLocked()
	r.mu.Unlock()
}

func (r *ring) wakeLocked() {
	for _, waiter := range r.waiters {
		close(waiter)
	}
	r.waiters = nil
}

// resolve answers where a client asking for `from` will actually be served, and how.
//
// A nil `from` is a client with no history: it starts at the live edge rather than replaying
// everything the ring holds, because it has drawn none of it.
func (r *ring) resolve(from *uint64) (uint64, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	live := r.floor + uint64(len(r.bytes))
	if from == nil {
		return live, ptycontract.ModeResumed
	}
	switch {
	case *from < r.floor:
		return r.floor, ptycontract.ModeRestarted
	case *from > live:
		// Ahead of anything written. Answering from the live edge would silently drop the
		// difference; restarting states that what the client holds is not this session's.
		return r.floor, ptycontract.ModeRestarted
	default:
		return *from, ptycontract.ModeResumed
	}
}

// read answers the bytes from `at`, blocking until there are some or the session ends.
//
// It returns the bytes and the sequence after them. A zero-length answer means the session ended.
func (r *ring) read(at uint64) ([]byte, uint64) {
	for {
		r.mu.Lock()
		live := r.floor + uint64(len(r.bytes))
		if at < r.floor {
			at = r.floor
		}
		if at < live {
			out := make([]byte, live-at)
			copy(out, r.bytes[at-r.floor:])
			r.mu.Unlock()
			return out, live
		}
		if r.ended {
			r.mu.Unlock()
			return nil, at
		}
		waiter := make(chan struct{})
		r.waiters = append(r.waiters, waiter)
		r.mu.Unlock()
		<-waiter
	}
}

// ack records how far the client has taken, and reports whether the reader may run.
func (r *ring) ack(bytes uint64) {
	r.mu.Lock()
	if bytes > r.acked {
		r.acked = bytes
	}
	r.mu.Unlock()
}

// paused reports whether the reader should hold off.
//
// The measure is unacked bytes, not ring occupancy: a client that is keeping up leaves the reader
// running however much the ring holds, and a client that has stopped taking output stops the reader
// however little it holds.
func (r *ring) paused(written uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	outstanding := written - r.acked
	if outstanding >= ptycontract.HighWatermark {
		return true
	}
	return false
}

// resumed reports whether a paused reader may run again. The gap between this and paused is the
// window slack, and it exists so acks still in flight keep the pipe moving.
func (r *ring) resumed(written uint64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return written-r.acked <= ptycontract.LowWatermark
}
