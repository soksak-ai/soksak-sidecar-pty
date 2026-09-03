package main

import (
	"fmt"
	"os"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// The outcomes a start reports for one session. A stop marked the record, so its state is what the
// stop wrote; an unmarked record ends wherever the last append reached and nothing states where it
// was meant to end.
const (
	restoreFull     = "full"
	restoreDegraded = "degraded"
	restoreFailed   = "failed"
)

// restoreOutcome is what a start reports per session in the store.
type restoreOutcome struct {
	Session uint64
	Outcome string
	// Reason is what stopped a failed restore. Empty otherwise.
	Reason string
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
		if outcome.Outcome == restoreFailed {
			// The record stays. It is the only evidence of what was lost, and a later start with a
			// reader that works may stand it up.
			fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: session %d did not restore: %s\n",
				id, outcome.Reason)
		}
	}
	return outcomes
}

func (reg *registry) restoreOne(held *store, id uint64) restoreOutcome {
	record, err := held.read(id)
	if err != nil {
		return restoreOutcome{Session: id, Outcome: restoreFailed, Reason: err.Error()}
	}
	output, err := held.output(id)
	if err != nil {
		return restoreOutcome{Session: id, Outcome: restoreFailed, Reason: err.Error()}
	}

	shell := record.Shell()
	if shell == "" {
		shell = reg.shell
	}
	process, err := startSessionProcess(
		shell, record.CWD, record.Environment, record.Cols, record.Rows)
	if err != nil {
		return restoreOutcome{Session: id, Outcome: restoreFailed, Reason: err.Error()}
	}

	reg.mu.Lock()
	if reg.stopped {
		reg.mu.Unlock()
		_ = process.Terminate()
		_ = process.Close()
		_ = process.Wait()
		return restoreOutcome{Session: id, Outcome: restoreFailed, Reason: "this daemon is shutting down"}
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
	// The output the session produced is what a consumer replays to reach the screen it had. It sits
	// ahead of anything the new shell writes, so an attach from sequence zero reads the whole
	// session and not just what happened after this start.
	value.written = value.ring.write(output)
	reg.sessions[id] = value
	processStarted := reg.processStarted
	reg.mu.Unlock()

	// This session is running again, so its record must read as a crash if this process now dies.
	// Leaving the previous stop's mark would report a clean end this process never made.
	outcome := restoreDegraded
	if record.EndedAtUnixMs != nil {
		outcome = restoreFull
	}
	record.EndedAtUnixMs = nil
	record.ExitCode = nil
	if err := held.write(record); err != nil {
		fmt.Fprintf(os.Stderr,
			"soksak-sidecar-pty: session %d still carries its previous stop mark: %v\n", id, err)
	}

	if processStarted != nil {
		processStarted(value)
	}
	go value.pump()
	return restoreOutcome{Session: id, Outcome: outcome}
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
