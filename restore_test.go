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
		output, _, err := value.output(id, 2*outputSegmentBound)
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

// Closing ends the session and removes its record. A session the record outlived is what a restore
// stands back up, so a record left behind is a session that comes back after it was closed.
func TestClosingRemovesTheRecord(t *testing.T) {
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

	result := reg.closeSession(sessionText(opened.id))
	if !result.Closed || !result.Held {
		t.Fatalf("closing a held session answered %+v", result)
	}
	if _, err := value.read(opened.id); err == nil {
		t.Fatal("the record outlived the session it was for")
	}

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)
	if outcomes := second.restore(); len(outcomes) != 0 {
		t.Fatalf("a closed session came back: %+v", outcomes)
	}
}

// A close of a session this daemon never held is not a failure. The outcome the caller wanted is the
// outcome it has, and the answer separates that from ending a running one.
func TestClosingASessionThisDaemonNeverHeldIsNotAFailure(t *testing.T) {
	reg := newRegistry("/bin/sh")
	value, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.close() })
	reg.attachStore(value)

	result := reg.closeSession("404")
	if !result.Closed {
		t.Fatalf("a close with nothing to end answered %+v", result)
	}
	if result.Held {
		t.Fatal("a session this daemon never held is reported as held")
	}
}

// The recorded modes are a record, not output. Put in the ring they would be replayed and drawn as
// the characters they are, on top of the screen they were meant to restore.
func TestRecordedModesAreNotReplayedAsOutput(t *testing.T) {
	home := t.TempDir()
	first := newRegistry("/bin/sh")
	firstStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	first.attachStore(firstStore)
	opened, err := first.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := opened.id
	if err := firstStore.setModes(id, []byte("v1 1 1 0 0 0 0 0 0 0 0 1 1 0")); err != nil {
		t.Fatal(err)
	}
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

	back, held := second.byID(id)
	if !held {
		t.Fatal("the session did not restore")
	}
	if strings.Contains(string(replayOf(t, back)), "v1 1 1") {
		t.Fatal("the mode record was replayed as output")
	}
	record, err := secondStore.read(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Modes) == 0 {
		t.Fatal("restoring dropped the recorded modes")
	}
}

// A sequence is a coordinate into this session's output, and a consumer holds one across a restart.
// A restore that started the coordinate over hands the same number to a different byte — no error,
// and the consumer draws output from a place it never asked for.
//
// COMPONENT-HANDOFF H4 states it for a process replacement: a ring that restarted at zero stops
// output without an error. A restore is the same coordinate and the same consumer.
func TestARestoreContinuesTheSequenceRatherThanRestartingIt(t *testing.T) {
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

	// Past the retained bound, so the stored output is a tail and not the whole session.
	chunk := make([]byte, 64*1024)
	for i := range chunk {
		chunk[i] = 'a'
	}
	for written := 0; written < 3*outputSegmentBound; written += len(chunk) {
		value.mu.Lock()
		value.written = value.ring.write(chunk)
		through := value.written
		value.mu.Unlock()
		if err := firstStore.append(id, chunk, through); err != nil {
			t.Fatal(err)
		}
	}
	value.mu.Lock()
	before := value.written
	value.mu.Unlock()
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

	back, held := second.byID(id)
	if !held {
		t.Fatal("the session did not restore")
	}
	back.mu.Lock()
	after := back.written
	back.mu.Unlock()
	if after < before {
		t.Fatalf("the coordinate went backwards: before=%d after=%d", before, after)
	}

	// The retained output ends where the session ended, so a consumer asking for the last byte the
	// session produced is asking inside the ring rather than past it.
	floor, through, _ := back.ring.snapshot()
	if through < before {
		t.Fatalf("the ring ends at %d, before the %d the session reached", through, before)
	}
	if floor > before {
		t.Fatalf("the ring starts at %d, past the %d the session reached", floor, before)
	}
}

// An id a restored session holds is never issued again while that session lives.
//
// A restore registers under the id the session had and a new open counts up from this instance's
// seed. The two spaces are drawn apart, but nothing stopped them from meeting: a collision would
// have the map entry for a live session replaced by a new one, and the restored shell would be
// running with nothing addressing it.
func TestAnIssuedIdNeverLandsOnARestoredOne(t *testing.T) {
	reg := newRegistry("/bin/sh")
	value, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = value.close() })
	reg.attachStore(value)

	// A real session stands where the next open would otherwise land, the way a restored one does.
	standing, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.shutdown() })
	taken := standing.id
	reg.mu.Lock()
	reg.next = taken - 1
	reg.mu.Unlock()

	opened, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	if opened.id == taken {
		t.Fatalf("a new session took the id %d another session holds", taken)
	}
	held, present := reg.byID(taken)
	if !present || held != standing {
		t.Fatal("the session that held the id was replaced")
	}
}

// A session that ended takes its record with it, whichever way it ended.
//
// Only reg.close removed a record. A shell exiting reaps, and the abandon sweep ends a session the
// same way, and neither touched the store — so the next start found the record and spawned a brand
// new shell for a session the person had ended. S3-1: a closed session is not recoverable.
func TestAnEndedSessionTakesItsRecord(t *testing.T) {
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
	if _, err := firstStore.read(id); err != nil {
		t.Fatal(err)
	}

	// The shell exits. Nothing else happens: no close command, no shutdown.
	value.reap(true)

	if _, err := firstStore.read(id); err == nil {
		t.Fatal("the record outlived the session that ended")
	}
	_ = firstStore.close()

	second := newRegistry("/bin/sh")
	secondStore, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = secondStore.close() })
	second.attachStore(secondStore)
	t.Cleanup(func() { second.shutdown() })
	if outcomes := second.restore(); len(outcomes) != 0 {
		t.Fatalf("a start stood an ended session back up: %+v", outcomes)
	}
}

// The abandon sweep ends sessions, so it takes their records too.
func TestTheAbandonSweepTakesTheRecord(t *testing.T) {
	home := t.TempDir()
	reg := newRegistry("/bin/sh")
	store, err := newStore(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.close() })
	reg.attachStore(store)
	reg.abandonAfter = time.Nanosecond
	value, err := reg.open(openRequest(t))
	if err != nil {
		t.Fatal(err)
	}
	id := value.id
	// The abandon clock starts when nothing is showing the session. A pane that went away is what
	// puts it there; the test stamps it directly rather than staging a renderer.
	value.mu.Lock()
	value.detachedAt = time.Now().Add(-time.Second)
	value.writtenAt = time.Time{}
	value.mu.Unlock()

	if ended := reg.endAbandoned(); ended != 1 {
		t.Fatalf("the sweep ended %d sessions", ended)
	}
	if _, err := store.read(id); err == nil {
		t.Fatal("the record outlived the session the sweep ended")
	}
}
