package main

import (
	"os"
	"testing"
)

// A clean stop over output that is gone is not a full restore.
//
// S6-1 defines full as "the record is marked cleanly ended and its state is restored". The mark was
// the whole test, and the second half was not checked — so a record whose segments were lost to an
// interrupted close, an external cleanup or a partial copy of the home came back full with nothing
// in it, and the attached consumer resumed against a session with no history.
//
// Degraded is what that is: the session is here and some of its state is not.
func TestACleanRecordWithNoOutputIsDegraded(t *testing.T) {
	home := t.TempDir()
	held, err := newStore(home)
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 11
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := held.append(id, []byte("what the shell printed"), 22); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := held.markEnded(id, 2, nil, 22); err != nil {
		t.Fatalf("stopping: %v", err)
	}
	// The segments are gone and the record is not. This is what an interrupted close leaves.
	for segment := 0; segment < 2; segment++ {
		_ = os.Remove(held.segmentPath(id, segment))
	}

	reg := newRegistry("/bin/sh")
	reg.attachStore(held)
	report := reg.restore()
	defer reg.shutdown()

	if len(report) != 1 {
		t.Fatalf("restored %d sessions, want 1", len(report))
	}
	if report[0].Outcome != restoreDegraded {
		t.Fatalf("a clean record with no output restored %q, want %q",
			report[0].Outcome, restoreDegraded)
	}
}

// A clean stop with its output restores full.
func TestACleanRecordWithItsOutputIsFull(t *testing.T) {
	home := t.TempDir()
	held, err := newStore(home)
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 12
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	if err := held.append(id, []byte("what the shell printed"), 22); err != nil {
		t.Fatalf("appending: %v", err)
	}
	if err := held.markEnded(id, 2, nil, 22); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	reg := newRegistry("/bin/sh")
	reg.attachStore(held)
	report := reg.restore()
	defer reg.shutdown()

	if len(report) != 1 || report[0].Outcome != restoreFull {
		t.Fatalf("a clean record with its output restored %+v, want %q", report, restoreFull)
	}
}
