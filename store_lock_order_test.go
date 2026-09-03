package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// Two record writers never wait on each other's lock.
//
// The two locks are the writer's, which guards one session's open segment file, and the record's,
// which guards one session's record. Taking them in opposite orders in two places is a deadlock,
// and it needs no stress to reach: one rotation beside one stop is enough.
//
// What it costs in the product: SIGTERM arrives while a shell is producing output. The stop walks
// the sessions on one goroutine, and the first session whose pump is crossing the segment bound
// hangs it — so no session after that one gets a stop write either, the daemon has to be killed,
// and every record comes back unmarked. A stop that hangs costs more than the write it was
// protecting.
func TestARotationAndAStopDoNotWaitOnEachOther(t *testing.T) {
	held, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 7

	if err = held.create(sessionRecord{Session: id, StartedAtUnixMs: 1}); err != nil {
		t.Fatalf("creating the record: %v", err)
	}
	// Fill the segment to its bound, so the next append is the one that rotates.
	chunk := []byte(strings.Repeat("x", 1<<16))
	written := uint64(0)
	for written < uint64(outputSegmentBound) {
		if err := held.append(id, chunk, written+uint64(len(chunk))); err != nil {
			t.Fatalf("filling the segment: %v", err)
		}
		written += uint64(len(chunk))
	}

	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		_ = held.append(id, chunk, written+uint64(len(chunk)))
	}()
	go func() {
		defer group.Done()
		_ = held.markEnded(id, 2, nil, written)
	}()

	settled := make(chan struct{})
	go func() { group.Wait(); close(settled) }()
	select {
	case <-settled:
	case <-time.After(10 * time.Second):
		t.Fatal("a rotation and a stop deadlocked: each holds the lock the other waits on")
	}
}

// One order for the two locks, stated where it can be checked.
//
// The rule: a holder of the record's lock never takes the writer's. Nothing enforces it at compile
// time, and the deadlock above is what breaking it costs, so the shape is measured in the source.
//
// The record's lock guards one session's record file, and the writer's guards its open segment.
// They protect different things and either can be wanted while the other is held — so which of the
// two comes first is decided once, here, rather than at each call site.
func TestNoHolderOfTheRecordLockTakesTheWriterLock(t *testing.T) {
	body, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	var offenders []string
	holding := false
	function := ""
	for number, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			function, holding = trimmed, false
		}
		if strings.Contains(trimmed, "lock.Lock()") {
			holding = true
		}
		if holding && strings.Contains(trimmed, "lock.Unlock()") && !strings.Contains(trimmed, "defer") {
			holding = false
		}
		if holding && strings.Contains(trimmed, "writer.mu.Lock()") {
			offenders = append(offenders,
				fmt.Sprintf("store.go:%d in %s", number+1, function))
		}
	}
	if len(offenders) > 0 {
		t.Errorf("these take the writer's lock while holding the record's:\n%s\n"+
			"Take the writer's lock first, release it, then take the record's.",
			strings.Join(offenders, "\n"))
	}
}

// The stop write is the one that reaches the platter.
//
// S4-5 splits the two: an ordinary record write goes as far as the operating system, because a
// process exit is what it has to survive; the stop write is forced down, because a power cycle is
// what it has to survive and page cache does not.
//
// Measured in the source because a test cannot observe an fsync. What it can observe is which path
// each writer takes, and the failure it prevents is the one that was here: markEnded synced the
// output segment and then wrote the record through the unsynced path, so the end mark and the
// coordinate — the whole evidence of a clean stop — were exactly the bytes a power cycle loses.
func TestOnlyTheStopWriteIsForcedToThePlatter(t *testing.T) {
	body, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(body)

	if !strings.Contains(source, "func (s *store) writeDurable(") {
		t.Fatal("there is no durable write for the stop to take")
	}
	// The durable path forces the staged file and the directory the rename lands in. A rename left
	// in page cache leaves the record under its staged name, which list() does not look for.
	for _, required := range []string{"staged.Sync()", "func (s *store) syncDir()", "dir.Sync()"} {
		if !strings.Contains(source, required) {
			t.Errorf("the durable write does not force %s", required)
		}
	}

	var durable []string
	function := ""
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func (s *store)") {
			function = trimmed
		}
		if strings.Contains(trimmed, "s.writeDurable(") && !strings.HasPrefix(function, "func (s *store) writeDurable(") {
			durable = append(durable, function)
		}
	}
	if len(durable) != 1 || !strings.HasPrefix(durable[0], "func (s *store) markEnded(") {
		t.Errorf("the durable write is taken by %v, want the stop write alone", durable)
	}
}
