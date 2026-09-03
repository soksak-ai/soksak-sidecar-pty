package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func creationFacts(id uint64) sessionRecord {
	return sessionRecord{
		Session: id, PaneID: "pane-1", WindowLabel: "w-1",
		CWD: "/tmp", Command: "/bin/sh -l", Environment: []string{"TERM=xterm"},
		Cols: 80, Rows: 24, StartedAtUnixMs: 1,
	}
}

// A record left by a killed owner holds the creation facts and is not marked ended. SESSION.md S4-2
// makes the creation write the floor of what a crash preserves, and S4-3 makes the mark the only
// evidence of which kind of exit happened.
func TestACreatedRecordHoldsTheCreationFactsAndIsNotMarked(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	read, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if read.CWD != "/tmp" || read.Cols != 80 || len(read.Environment) != 1 {
		t.Fatalf("the creation facts did not survive: %+v", read)
	}
	if read.EndedAtUnixMs != nil {
		t.Fatalf("a created record is marked ended: %+v", read)
	}
}

// The stop write is the only one that marks a record. A drain that never reached it leaves the mark
// absent, and a reader that assumed a clean end would report full over a truncated store.
func TestOnlyTheEndMarkSaysTheOwnerStoppedCleanly(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	code := int64(0)
	if err := store.markEnded(7, 99, &code); err != nil {
		t.Fatal(err)
	}
	read, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if read.EndedAtUnixMs == nil || *read.EndedAtUnixMs != 99 {
		t.Fatalf("the end mark is absent after a stop write: %+v", read)
	}
}

// Output is appended as it arrives, and what a restore replays is what the appends left.
func TestAppendedOutputIsReadBackInOrder(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	for _, chunk := range []string{"one", "two", "three"} {
		if err := store.append(7, []byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	output, err := store.output(7)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "onetwothree" {
		t.Fatalf("output read back as %q", output)
	}
}

// The stored output is kept to a bound, and the bound is what a restore's replay cost is derived
// from. Past it the oldest output goes and the newest stays.
func TestStoredOutputStaysWithinItsBoundAndKeepsTheNewest(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 64*1024)
	for i := range chunk {
		chunk[i] = 'a'
	}
	written := 0
	for written < 3*outputSegmentBound {
		if err := store.append(7, chunk); err != nil {
			t.Fatal(err)
		}
		written += len(chunk)
	}
	tail := []byte("THE-NEWEST")
	if err := store.append(7, tail); err != nil {
		t.Fatal(err)
	}
	output, err := store.output(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(output) > 2*outputSegmentBound {
		t.Fatalf("the store holds %d bytes, past the %d bound", len(output), 2*outputSegmentBound)
	}
	if !strings.HasSuffix(string(output), string(tail)) {
		t.Fatal("the newest output is not what the store ends with")
	}
}

// A record's name states its format version, so a record in an older shape is not found rather than
// found and refused. No reader for an older shape can exist to be written.
func TestARecordInAnOlderFormatVersionIsNotFound(t *testing.T) {
	store := newStoreAt(t)
	older := filepath.Join(store.dir, "v0-7.json")
	if err := os.WriteFile(older, []byte(`{"session":7}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(7); err == nil {
		t.Fatal("a v0 record was read by the v1 reader")
	}
	ids, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("the v1 listing found %v", ids)
	}
}

// A record states the session id it is for, and one whose id does not match its path is refused
// rather than repaired.
func TestARecordWhoseIdDoesNotMatchItsPathIsRefused(t *testing.T) {
	store := newStoreAt(t)
	if err := os.WriteFile(filepath.Join(store.dir, "v1-7.json"),
		[]byte(`{"session":8,"cwd":"/tmp"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.read(7); err == nil {
		t.Fatal("a record naming session 8 was read as session 7")
	}
}

func newStoreAt(t *testing.T) *store {
	t.Helper()
	value, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.close() })
	return value
}

// One session's write never waits on another's. The store holds a lock per session, not one for
// all of them: a shell producing output would otherwise pause every other shell for the length of
// a disk write.
func TestOneSessionsWriteDoesNotWaitOnAnother(t *testing.T) {
	store := newStoreAt(t)
	for _, id := range []uint64{7, 8} {
		if err := store.create(creationFacts(id)); err != nil {
			t.Fatal(err)
		}
		if err := store.append(id, []byte("open")); err != nil {
			t.Fatal(err)
		}
	}

	held := make(chan struct{})
	release := make(chan struct{})
	go func() {
		writer := store.writerFor(7)
		writer.mu.Lock()
		close(held)
		<-release
		writer.mu.Unlock()
	}()
	<-held
	defer close(release)

	done := make(chan error, 1)
	go func() { done <- store.append(8, []byte("through")) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a write for session 8 waited on session 7's lock")
	}
}

// Two sessions writing at once leave two records, each holding only its own output.
func TestConcurrentSessionsDoNotMixTheirOutput(t *testing.T) {
	store := newStoreAt(t)
	for _, id := range []uint64{7, 8} {
		if err := store.create(creationFacts(id)); err != nil {
			t.Fatal(err)
		}
	}
	var group sync.WaitGroup
	for _, pair := range []struct {
		id   uint64
		mark string
	}{{7, "seven"}, {8, "eight"}} {
		group.Add(1)
		go func(id uint64, mark string) {
			defer group.Done()
			for i := 0; i < 200; i++ {
				if err := store.append(id, []byte(mark)); err != nil {
					t.Error(err)
					return
				}
			}
		}(pair.id, pair.mark)
	}
	group.Wait()

	for _, pair := range []struct {
		id    uint64
		mine  string
		other string
	}{{7, "seven", "eight"}, {8, "eight", "seven"}} {
		output, err := store.output(pair.id)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(output), pair.other) {
			t.Fatalf("session %d's record holds %q", pair.id, pair.other)
		}
		if strings.Count(string(output), pair.mine) != 200 {
			t.Fatalf("session %d kept %d of its 200 writes", pair.id,
				strings.Count(string(output), pair.mine))
		}
	}
}

// The modes are the second of the two parts a terminal owner stores. They are written when they
// change rather than on a cadence, and a record with none restores a screen whose modes are the
// defaults.
func TestTheModesAreStoredBesideTheOutput(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	if err := store.setModes(7, []byte("v1 0 1 0 1 0 1 0 1 0 1 0 1 0")); err != nil {
		t.Fatal(err)
	}
	record, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if string(record.Modes) != "v1 0 1 0 1 0 1 0 1 0 1 0 1 0" {
		t.Fatalf("the modes came back as %q", record.Modes)
	}
}

// A record written before any mode changed holds none, and a restore from it applies nothing rather
// than applying a guess.
func TestARecordWithNoModeChangeHoldsNone(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	record, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Modes) != 0 {
		t.Fatalf("a record nothing set modes on holds %q", record.Modes)
	}
}

// A screen read as history is not the work continued. What was running is what a continuation
// starts again, and the login shell this daemon started is not it.
func TestTheProgramThatWasRunningIsRecorded(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	if err := store.setForeground(7, "vim /etc/hosts", "/etc"); err != nil {
		t.Fatal(err)
	}
	record, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if record.Foreground != "vim /etc/hosts" {
		t.Fatalf("the record names %q as what was running", record.Foreground)
	}
	if record.ForegroundCWD != "/etc" {
		t.Fatalf("the record names %q as where it ran", record.ForegroundCWD)
	}
	if record.Command == record.Foreground {
		t.Fatal("the login shell and the program that was running are one field")
	}
}

// A session where nothing but the shell ran records no program. Offering the shell as a program to
// start again would offer what a restore already did.
func TestASessionRunningOnlyItsShellRecordsNoProgram(t *testing.T) {
	store := newStoreAt(t)
	if err := store.create(creationFacts(7)); err != nil {
		t.Fatal(err)
	}
	if err := store.setForeground(7, "", ""); err != nil {
		t.Fatal(err)
	}
	record, err := store.read(7)
	if err != nil {
		t.Fatal(err)
	}
	if record.Foreground != "" {
		t.Fatalf("a session running only its shell recorded %q", record.Foreground)
	}
}
