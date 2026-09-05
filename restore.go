package main

import (
	"fmt"
	"os"
	"sort"
	"strconv"

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// The outcomes are the envelope's. A second set of names here would be a second answer to what a
// start ended in, and the two would disagree the first time one of them changed.
const (
	restoreFull     = controlwire.SessionFull
	restoreDegraded = controlwire.SessionDegraded
	restoreFailed   = controlwire.SessionFailed
)

type restoreOutcome = controlwire.SessionOutcome

// shellBoundary is what separates the output of a session's previous shell from its next one.
var shellBoundary = []byte("\r\n")

// sessionText is how a session id travels on the envelope. Decimal, and the envelope reads nothing
// out of it.
func sessionText(id uint64) string { return strconv.FormatUint(id, 10) }

// sessionNumber reads back what sessionText wrote. A caller naming something else names no session
// this daemon has.
func sessionNumber(text string) (uint64, bool) {
	id, err := strconv.ParseUint(text, 10, 64)
	return id, err == nil
}

// restore reads this owner's records and stands each session back up.
//
// A session comes back under the id it had. Nothing else would do: the id is the only name a
// reference to this session holds, and an id issued fresh would leave every one of them pointing at
// nothing. The id space is seeded per instance, so a restored id and a newly issued one do not meet.
//
// The shell is new. A process is not stored and no start returns one, so what comes back is a shell
// started from the creation facts with the session's output ahead of it.
func (reg *registry) restore() []restoreOutcome {
	held := reg.sessionStore()
	if held == nil {
		reg.markStoreRead()
		return nil
	}
	ids, err := held.list()
	if err != nil {
		fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: the store could not be read: %v\n", err)
		return nil
	}

	outcomes := make([]restoreOutcome, 0, len(ids))
	for _, id := range ids {
		outcome := reg.restoreOne(held, id)
		outcomes = append(outcomes, outcome)
		reg.recordOutcome(id, outcome)
		if outcome.Outcome == restoreFailed {
			// The record stays. It is the only evidence of what was lost, and a later start with a
			// reader that works may stand it up.
			fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: session %d did not restore: %s\n",
				id, outcome.Reason)
		}
	}
	reg.markStoreRead()
	return outcomes
}

// markStoreRead states that every record was looked at. Until it runs, a report is unfinished and a
// caller must not count anything lost from it.
func (reg *registry) markStoreRead() {
	reg.mu.Lock()
	reg.readStore = true
	reg.mu.Unlock()
}

func (reg *registry) restoreOne(held *store, id uint64) restoreOutcome {
	record, err := held.read(id)
	if err != nil {
		return restoreOutcome{Session: sessionText(id), Outcome: restoreFailed, Reason: err.Error()}
	}
	// Only what the ring will keep. Reading both segments whole hands over up to 8 MiB so that the
	// ring can drop 7 of them, on the start path the launcher waits on.
	output, through, err := held.output(id, ptycontract.HighWatermark)
	if err != nil {
		return restoreOutcome{Session: sessionText(id), Outcome: restoreFailed, Reason: err.Error()}
	}

	shell := record.Shell()
	if shell == "" {
		shell = reg.shell
	}
	process, err := startSessionProcess(
		shell, record.CWD, record.Environment, record.Cols, record.Rows)
	if err != nil {
		return restoreOutcome{Session: sessionText(id), Outcome: restoreFailed, Reason: err.Error()}
	}

	reg.mu.Lock()
	if reg.stopped {
		reg.mu.Unlock()
		_ = process.Terminate()
		_ = process.Close()
		_ = process.Wait()
		return restoreOutcome{Session: sessionText(id), Outcome: restoreFailed, Reason: "this daemon is shutting down"}
	}
	reg.generation++
	value := &session{
		id:             id,
		paneID:         record.PaneID,
		windowLabel:    record.WindowLabel,
		cwd:            record.CWD,
		command:        record.Command,
		startedAt:      reg.now(),
		generation:     reg.generation,
		process:        process,
		ring:           newRing(ptycontract.HighWatermark),
		observers:      make(map[*observer]struct{}),
		observerTokens: make(map[string]*observer),
		displaying:     make(map[*observer]struct{}),
		now:            reg.now,
		resume:         make(chan struct{}, 1),
		cols:           record.Cols,
		rows:           record.Rows,
		environment:    record.Environment,
		processEnded:   reg.processEnded,
		store:          held,
	}
	// A restored session feeds its store the same way a new one does: offered, never written from
	// the pump.
	value.feed = value.storeFeedFor()
	// The recorded modes do not go in here. They are a record the consumer reads with pty.modes and
	// applies to a fresh mirror before it replays; put in the ring they would be replayed as output
	// and drawn as the characters they are.
	//
	// The output the session produced is what a consumer replays to reach the screen it had, and it
	// stands at the coordinate the session left it at.
	//
	// A ring that started over would hand a coordinate a consumer already holds to a different byte
	// — no error, and the consumer draws output from a place it never asked for. The store answers
	// where its bytes end, and the retained bytes end there.
	value.ring.restore(output, through)
	_, live, _ := value.ring.snapshot()
	value.written = live
	// The new shell writes after the output the previous one left, and that output ends where the
	// session was cut: on a prompt, most of the time. Drawn straight on, the new shell's first prompt
	// began on the old prompt's row, and zsh marked the join with its end-of-line mark and a row of
	// spaces that wrapped — every restart added one wrapped row, and a narrower window reflowed each
	// into a staircase (measured 2026-09-05). The two shells are separated by a line break, recorded
	// like any output: replayed now and stored for the next restart, the join is drawn the same way
	// every time. Output that already ended a line needs none.
	if len(output) > 0 && output[len(output)-1] != '\n' {
		value.written = value.ring.write(shellBoundary)
		value.feed.offer(shellBoundary, value.written)
	}
	reg.sessions[id] = value
	processStarted := reg.processStarted
	reg.mu.Unlock()

	// This session is running again, so its record must read as a crash if this process now dies.
	// Leaving the previous stop's mark would report a clean end this process never made.
	//
	// S6-1 defines full as marked cleanly ended *and its state restored*. The mark alone was the
	// whole test, so a record whose segments were lost — an interrupted close, an external cleanup,
	// a partial copy of the home — came back full with nothing in it and the attached consumer
	// resumed against a session with no history.
	//
	// The stop recorded where the session had reached. Output that no longer reaches it is state
	// the record says existed and does not, which is degraded: the session is here and some of its
	// state is not.
	outcome := restoreDegraded
	if record.EndedAtUnixMs != nil && through >= record.EndedThrough {
		outcome = restoreFull
	}
	if err := held.clearStopMark(id); err != nil {
		fmt.Fprintf(os.Stderr,
			"soksak-sidecar-pty: session %d still carries its previous stop mark: %v\n", id, err)
	}

	if processStarted != nil {
		processStarted(value)
	}
	go value.pump()
	return restoreOutcome{Session: sessionText(id), Outcome: outcome}
}

// Shell is what a recreated session starts. The record holds the command line the session ran under
// rather than the binary alone, so the binary is its first field.
func (record sessionRecord) Shell() string {
	for index := 0; index < len(record.Command); index++ {
		if record.Command[index] == ' ' {
			return record.Command[:index]
		}
	}
	return record.Command
}

// byID returns one session this registry holds.
func (reg *registry) byID(id uint64) (*session, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	value, held := reg.sessions[id]
	return value, held
}

// recordOutcome keeps what a start ended in for one session, so a caller reads the same answer
// however long after the start it asks.
func (reg *registry) recordOutcome(id uint64, outcome restoreOutcome) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	if reg.outcomes == nil {
		reg.outcomes = make(map[uint64]restoreOutcome)
	}
	reg.outcomes[id] = outcome
}

// restoreReport answers what became of each session the caller names. An empty list asks for every
// session this daemon knows of, which is what a caller with no index of its own reads.
//
// A session this daemon holds but has no start outcome for was opened here rather than restored.
// Nothing about a previous process is claimed for it: its state is what this process wrote.
func (reg *registry) restoreReport(named []string) controlwire.SessionReport {
	reg.mu.Lock()
	complete := reg.readStore
	if len(named) == 0 {
		seen := make(map[uint64]bool, len(reg.sessions)+len(reg.outcomes))
		for id := range reg.sessions {
			named = append(named, sessionText(id))
			seen[id] = true
		}
		for id := range reg.outcomes {
			if !seen[id] {
				named = append(named, sessionText(id))
			}
		}
	}
	outcomes := make([]controlwire.SessionOutcome, 0, len(named))
	for _, text := range named {
		// A name this daemon could never have issued names no session it has, which is the same
		// answer as one it issued and no longer holds.
		id, numeric := sessionNumber(text)
		if numeric {
			if outcome, held := reg.outcomes[id]; held {
				outcomes = append(outcomes, outcome)
				continue
			}
			if _, held := reg.sessions[id]; held {
				outcomes = append(outcomes, controlwire.SessionOutcome{
					Session: text, Outcome: controlwire.SessionFull,
				})
				continue
			}
		}
		outcomes = append(outcomes, controlwire.SessionOutcome{
			Session: text, Outcome: controlwire.SessionLost,
		})
	}
	reg.mu.Unlock()
	sort.Slice(outcomes, func(left, right int) bool {
		return outcomes[left].Session < outcomes[right].Session
	})
	return controlwire.SessionReport{Complete: complete, Sessions: outcomes}
}

// forgetOutcome drops what a start ended in for a session that no longer exists. A closed session
// reported by a later listing is one a caller goes looking for and does not find.
func (reg *registry) forgetOutcome(id uint64) {
	reg.mu.Lock()
	delete(reg.outcomes, id)
	reg.mu.Unlock()
}

// closeSession ends one session named the way the envelope names it.
//
// A session this daemon never held is not a failure: the outcome the caller wanted is the outcome
// it has. The answer separates that from ending a running one, which a caller reconciling an index
// reads differently even though both leave no record.
func (reg *registry) closeSession(text string) controlwire.SessionCloseResult {
	result := controlwire.SessionCloseResult{Session: text, Closed: true}
	id, numeric := sessionNumber(text)
	if !numeric {
		return result
	}
	reg.mu.Lock()
	_, held := reg.sessions[id]
	reg.mu.Unlock()
	result.Held = held
	if err := reg.close(id); err != nil && held {
		result.Closed = false
	}
	return result
}
