package main

import (
	"os"
	"strings"
	"testing"
)

// A restore starts no program of its own.
//
// The record names what was in front when the session was last seen, so that a person can be
// offered it (S6-3). Running it instead would take a decision that is theirs: the program may have
// been what crashed the session, may prompt, may write files, and the person has not asked for it
// again. What comes back is the shell.
func TestARestoreStartsNoProgramOfItsOwn(t *testing.T) {
	home := t.TempDir()
	held, err := newStore(home)
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 21
	if err := held.create(sessionRecord{
		Session: id, StartedAtUnixMs: 1, Cols: 80, Rows: 24, Command: "/bin/sh",
	}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	// A program was in front when this session was last seen, and the record says so.
	if err := held.setForeground(id, "vim /etc/passwd", "/tmp"); err != nil {
		t.Fatalf("recording the foreground: %v", err)
	}
	if err := held.markEnded(id, 2, nil, 0); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	reg := newRegistry("/bin/sh")
	reg.attachStore(held)
	report := reg.restore()
	defer reg.shutdown()
	if len(report) != 1 {
		t.Fatalf("restored %d sessions, want 1", len(report))
	}

	// The recorded program is still on the record — it is an offer, not an instruction.
	after, err := held.read(id)
	if err != nil {
		t.Fatalf("re-reading: %v", err)
	}
	if after.Foreground != "vim /etc/passwd" {
		t.Fatalf("the offer was lost: %q", after.Foreground)
	}
	// And nothing started it: the session's own process tree is the shell alone.
	value, err := reg.get(id)
	if err != nil {
		t.Fatalf("the session is not in the registry: %v", err)
	}
	if value.command != "" && strings.Contains(value.command, "vim") {
		t.Fatalf("the restore ran the recorded program: %q", value.command)
	}
}

// A record that is not whole is not read as one.
//
// A reader that took half a record would stand a session up on fields that were never written
// together — a segment number from one write beside a coordinate from another. S6-1 answers failed
// and keeps the record, because removing it throws away the only evidence and a fixed reader may
// succeed later.
func TestAPartialRecordIsNotRead(t *testing.T) {
	home := t.TempDir()
	held, err := newStore(home)
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 22
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1, Cols: 80, Rows: 24}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	whole, err := os.ReadFile(held.recordPath(id))
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	// Half a record, which is what a write into the file itself would leave behind.
	if err := os.WriteFile(held.recordPath(id), whole[:len(whole)/2], 0o600); err != nil {
		t.Fatalf("truncating: %v", err)
	}

	if _, err := held.read(id); err == nil {
		t.Fatal("half a record was read as a whole one")
	}

	reg := newRegistry("/bin/sh")
	reg.attachStore(held)
	report := reg.restore()
	defer reg.shutdown()
	if len(report) != 1 || report[0].Outcome != restoreFailed {
		t.Fatalf("restored %+v, want one %s", report, restoreFailed)
	}
	if report[0].Reason == "" {
		t.Fatal("a failed restore names no reason")
	}
	// The record stays. Removing it discards the only evidence of what was lost.
	if _, err := os.Stat(held.recordPath(id)); err != nil {
		t.Fatalf("the failed record was removed: %v", err)
	}
}
