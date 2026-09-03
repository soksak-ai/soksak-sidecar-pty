package main

import (
	"os"
	"sync"
	"testing"
)

// A close is not undone by a record write that read before it.
//
// A record writer reads the record, changes one field and writes it back. A close beside it deletes
// the record and both segments — and then the writer publishes what it read, recreating the record
// of a session that is over. Next start restores it: a new shell against no output.
//
// The window is ordinary rather than rare. The shell's last child exiting is what triggers the
// process monitor's reconcile, and a shell exiting is what reaps the session; the two writes are
// the same event seen from two places.
func TestAClosedSessionIsNotRecreatedByARecordWrite(t *testing.T) {
	held, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}

	recreated := 0
	for round := uint64(1); round <= 200; round++ {
		if err := held.create(sessionRecord{Session: round, StartedAtUnixMs: 1}); err != nil {
			t.Fatalf("creating: %v", err)
		}
		var group sync.WaitGroup
		group.Add(2)
		go func() { defer group.Done(); _ = held.setForeground(round, "vim", "/tmp") }()
		go func() { defer group.Done(); _ = held.remove(round) }()
		group.Wait()

		if _, err := os.Stat(held.recordPath(round)); err == nil {
			recreated++
		}
	}
	if recreated > 0 {
		t.Fatalf("%d of 200 closes left a record behind: a record write beside the close recreated it",
			recreated)
	}
}

// A close that fails part-way leaves no output behind a record it already deleted.
//
// The record is what list() enumerates, so a record deleted before its segments makes those
// segments unreachable: nothing names them and S4-6 forbids a sweep at start. The segments go
// first, and the record — the one thing that makes them findable — goes last.
func TestACloseDropsTheOutputBeforeTheRecord(t *testing.T) {
	held, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 3
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := held.append(id, []byte("output"), 6); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := held.remove(id); err != nil {
		t.Fatalf("removing: %v", err)
	}
	for segment := 0; segment < 2; segment++ {
		if _, err := os.Stat(held.segmentPath(id, segment)); err == nil {
			t.Fatalf("segment %d outlived the record that named it", segment)
		}
	}
}
