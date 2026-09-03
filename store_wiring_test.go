package main

import (
	"testing"
	"time"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// A session that opened leaves a record before it produces anything. Everything a crash preserves
// starts at that write, so a session that opened and crashed at once is still recreatable.
func TestOpeningASessionWritesItsCreationFacts(t *testing.T) {
	home := t.TempDir()
	reg := newRegistry("/bin/sh")
	store, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	reg.attachStore(store)

	value, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.close(value.id) })

	record, err := store.read(value.id)
	if err != nil {
		t.Fatalf("no record for the session that opened: %v", err)
	}
	if record.CWD == "" || record.Command == "" || record.Cols == 0 {
		t.Fatalf("the record is short of the creation facts: %+v", record)
	}
	if len(record.Environment) == 0 {
		t.Fatal("the record holds no environment, so a recreated shell takes this daemon's")
	}
	if record.EndedAtUnixMs != nil {
		t.Fatal("a session that is running is marked ended")
	}
}

// Output is appended as it arrives. What a restore replays is what the shell produced, and a
// session killed between its creation and its stop still has that.
func TestOutputReachesTheStoreWhileTheSessionRuns(t *testing.T) {
	home := t.TempDir()
	reg := newRegistry("/bin/sh")
	store, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	reg.attachStore(store)

	value, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.close(value.id) })

	if err := value.write([]byte("echo stored-marker\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := store.output(value.id, 2*outputSegmentBound)
		if err == nil && len(output) > 0 && contains(output, "stored-marker") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the shell's output never reached the store")
}

// Shutdown is the stop write. It is the point a power cycle recovers from, and the mark it leaves
// is what separates a clean stop from a crash.
func TestShutdownMarksEveryRecordCleanlyEnded(t *testing.T) {
	home := t.TempDir()
	reg := newRegistry("/bin/sh")
	store, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	reg.attachStore(store)

	value, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	reg.shutdown()

	record, err := store.read(value.id)
	if err != nil {
		t.Fatalf("shutdown removed the record: %v", err)
	}
	if record.EndedAtUnixMs == nil {
		t.Fatal("shutdown left the record unmarked, so a restore reads it as a crash")
	}
}

func openRequest(t *testing.T) ptycontract.Open {
	t.Helper()
	return ptycontract.Open{
		PaneID: "pane-store", WindowLabel: "w-store", Shell: "/bin/sh",
		CWD: t.TempDir(), Cols: 80, Rows: 24,
		Environment: ptycontract.Environment{Variables: map[string]string{"STORE_TEST": "1"}},
	}
}

func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
