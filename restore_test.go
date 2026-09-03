package main

import (
	"strings"
	"testing"
	"time"

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// A session outlives the process that held it. The record a stop left is read at the next start and
// the session comes back under the id it had, with the output it produced.
func TestASessionSurvivesItsOwnerProcessExiting(t *testing.T) {
	home := t.TempDir()
	first := newRegistry("/bin/sh")
	firstStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	first.attachStore(firstStore)

	value, err := first.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := value.id
	if err := value.write([]byte("echo survives-marker\n")); err != nil {
		t.Fatal(err)
	}
	waitForStored(t, firstStore, id, "survives-marker")
	first.shutdown()
	_ = firstStore.close()

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)

	outcomes := second.restore()
	t.Cleanup(func() { second.shutdown() })

	if len(outcomes) != 1 {
		t.Fatalf("the start restored %d sessions, not the one the record names", len(outcomes))
	}
	if outcomes[0].Session != sessionText(id) {
		t.Fatalf("the session came back as %s, not %d", outcomes[0].Session, id)
	}
	if outcomes[0].Outcome != restoreFull {
		t.Fatalf("a record a stop marked restored as %q", outcomes[0].Outcome)
	}
	back, held := second.byID(id)
	if !held {
		t.Fatal("the restored session is not in the registry")
	}
	if !strings.Contains(string(replayOf(t, back)), "survives-marker") {
		t.Fatal("the restored session replays none of the output it produced")
	}
}

// A record no stop marked is what a crash left. It restores, and it restores as degraded: the
// output ends wherever the last append reached and nothing states where that was meant to be.
func TestARecordNoStopMarkedRestoresAsDegraded(t *testing.T) {
	home := t.TempDir()
	first := newRegistry("/bin/sh")
	firstStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	first.attachStore(firstStore)
	value, err := first.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := value.id
	// No shutdown: the process is gone and the record keeps no mark.
	_ = firstStore.close()

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)
	outcomes := second.restore()
	t.Cleanup(func() { second.shutdown() })

	if len(outcomes) != 1 || outcomes[0].Session != sessionText(id) {
		t.Fatalf("the crash record did not restore: %+v", outcomes)
	}
	if outcomes[0].Outcome != restoreDegraded {
		t.Fatalf("an unmarked record restored as %q", outcomes[0].Outcome)
	}
}

// A restored session is running again, so its record must read as a crash if this process now dies.
// Leaving the previous stop's mark would report a clean end this process never made.
func TestRestoringClearsThePreviousStopMark(t *testing.T) {
	home := t.TempDir()
	first := newRegistry("/bin/sh")
	firstStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	first.attachStore(firstStore)
	value, err := first.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := value.id
	first.shutdown()
	_ = firstStore.close()

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)
	second.restore()
	t.Cleanup(func() { second.shutdown() })

	record, err := secondStore.read(id)
	if err != nil {
		t.Fatal(err)
	}
	if record.EndedAtUnixMs != nil {
		t.Fatal("a running session's record still carries the previous stop's mark")
	}
}

func waitForStored(t *testing.T, value *store, id uint64, marker string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		output, err := value.output(id)
		if err == nil && strings.Contains(string(output), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%q never reached the store", marker)
}

func replayOf(t *testing.T, value *session) []byte {
	t.Helper()
	_, through, data := value.ring.snapshot()
	_ = through
	return data
}

var _ = ptycontract.Open{}

// A caller holding an index asks what became of each session in it. A session this daemon restored
// answers with how it restored; one it holds no record for answers lost.
func TestTheReportAnswersForEverySessionTheCallerNames(t *testing.T) {
	home := t.TempDir()
	first := newRegistry("/bin/sh")
	firstStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	first.attachStore(firstStore)
	value, err := first.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	restored := value.id
	first.shutdown()
	_ = firstStore.close()

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)
	second.restore()
	t.Cleanup(func() { second.shutdown() })

	gone := restored + 1
	report := second.restoreReport([]string{sessionText(restored), sessionText(gone)})
	if !report.Complete {
		t.Fatal("a daemon that read its store through reports the read as unfinished")
	}
	byID := map[string]string{}
	for _, outcome := range report.Sessions {
		byID[outcome.Session] = outcome.Outcome
	}
	if byID[sessionText(restored)] != controlwire.SessionFull {
		t.Fatalf("the restored session reports %q", byID[sessionText(restored)])
	}
	if byID[sessionText(gone)] != controlwire.SessionLost {
		t.Fatalf("a session with no record reports %q, not lost", byID[sessionText(gone)])
	}
}

// A caller with no index of its own names nothing and reads every session this daemon knows of.
func TestAnEmptyRequestReportsEverySessionTheDaemonKnows(t *testing.T) {
	home := t.TempDir()
	reg := newRegistry("/bin/sh")
	value, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.close() })
	reg.attachStore(value)
	opened, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.shutdown() })

	report := reg.restoreReport(nil)
	if len(report.Sessions) != 1 || report.Sessions[0].Session != sessionText(opened.id) {
		t.Fatalf("an empty request reported %+v", report.Sessions)
	}
	// This session was opened here rather than restored, so nothing about a previous process is
	// claimed for it.
	if report.Sessions[0].Outcome != controlwire.SessionFull {
		t.Fatalf("a session this daemon opened reports %q", report.Sessions[0].Outcome)
	}
}
