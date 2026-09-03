package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// A record outlives the process that wrote it, so an owner that never swept would grow its store by
// every session that ever ran.
func TestStartRemovesEveryRecordTheIndexDoesNotName(t *testing.T) {
	store := newStoreAt(t)
	for _, id := range []uint64{7, 8, 9} {
		if err := store.create(creationFacts(id)); err != nil {
			t.Fatal(err)
		}
		if err := store.append(id, []byte("out")); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.sweep(map[uint64]bool{8: true})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("the sweep removed %d records, not the two the index does not name", removed)
	}
	if _, err := store.read(8); err != nil {
		t.Fatalf("the sweep removed the record the index names: %v", err)
	}
	for _, id := range []uint64{7, 9} {
		if _, err := store.read(id); err == nil {
			t.Fatalf("record %d survived the sweep", id)
		}
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
