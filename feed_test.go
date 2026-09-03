package main

import (
	"sync"
	"testing"
	"time"
)

// A store that has stopped answering does not stop the shell.
//
// S4-5 requires the writer to be a subscriber that loses bytes loudly rather than pausing the
// session feeding it. A sink that never returns stands for a home on a network mount that has gone
// away, which is the case that used to stop the pump and, through it, the shell.
func TestAStalledStoreDoesNotStopTheSession(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)
	feed := newStoreFeed(1, func([]byte, uint64) error {
		<-stuck
		return nil
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for round := 0; round < 200; round++ {
			feed.offer([]byte("a chunk of output"), uint64(round))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session waited on a store that is not answering")
	}
}

// Everything offered reaches the store, in order, while it keeps up.
func TestOfferedOutputReachesTheStoreInOrder(t *testing.T) {
	var mu sync.Mutex
	var seen []uint64
	feed := newStoreFeed(2, func(_ []byte, through uint64) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, through)
		return nil
	})

	for round := uint64(1); round <= 50; round++ {
		feed.offer([]byte("x"), round)
	}
	feed.close()

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 50 {
		t.Fatalf("the store saw %d chunks, want 50", len(seen))
	}
	for index, through := range seen {
		if through != uint64(index+1) {
			t.Fatalf("chunk %d carries coordinate %d: the order changed", index, through)
		}
	}
}

// Closing waits for what was accepted, so a stop write does not claim output the store lacks.
func TestClosingTheFeedWaitsForWhatItAccepted(t *testing.T) {
	var mu sync.Mutex
	landed := 0
	feed := newStoreFeed(3, func([]byte, uint64) error {
		time.Sleep(time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		landed++
		return nil
	})
	for round := uint64(1); round <= 20; round++ {
		feed.offer([]byte("x"), round)
	}
	feed.close()

	mu.Lock()
	defer mu.Unlock()
	if landed != 20 {
		t.Fatalf("%d of 20 chunks reached the store before the stop", landed)
	}
}

// A drain that cannot finish does not hold the stop.
//
// The stop walks every session on one goroutine, so a drain that waits forever stops at the first
// session whose store is not answering and no session after it gets a stop write either — the
// daemon then has to be killed and every record comes back unmarked. That is the failure the lock
// ordering above was fixed for, and an unbounded drain is the same thing through another door.
//
// What the deadline costs is the tail of one record, which a restore reports as degraded because
// the recorded coordinate is past what the stored output reaches. What it buys is a stop that
// finishes.
func TestADrainThatCannotFinishDoesNotHoldTheStop(t *testing.T) {
	stuck := make(chan struct{})
	defer close(stuck)
	feed := newStoreFeed(4, func([]byte, uint64) error {
		<-stuck
		return nil
	})
	feed.offer([]byte("a chunk the store will not take"), 31)

	settled := make(chan struct{})
	go func() { feed.closeWithin(200 * time.Millisecond); close(settled) }()
	select {
	case <-settled:
	case <-time.After(10 * time.Second):
		t.Fatal("the stop waited on a drain that cannot finish")
	}
}

// A drain that finishes inside its deadline is waited for.
func TestADrainInsideItsDeadlineIsWaitedFor(t *testing.T) {
	var mu sync.Mutex
	landed := 0
	feed := newStoreFeed(5, func([]byte, uint64) error {
		time.Sleep(time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		landed++
		return nil
	})
	for round := uint64(1); round <= 20; round++ {
		feed.offer([]byte("x"), round)
	}
	feed.closeWithin(10 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	if landed != 20 {
		t.Fatalf("%d of 20 chunks reached the store before the stop", landed)
	}
}
