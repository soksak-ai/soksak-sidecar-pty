package main

import (
	"sort"
	"sync"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

const processEventBuffer = 64

// processInventoryState is the one revision owner. A revision exists only when one public process
// record changes. Snapshots and stream subscriptions share this lock so the first stream event is
// always the snapshot revision plus one, unless a bounded observer has demonstrably fallen behind.
type processInventoryState struct {
	mu        sync.Mutex
	revision  uint64
	sessions  map[uint64]map[string]ptycontract.Process
	processes map[string]ptycontract.Process
	observers map[*processEventSubscription]struct{}
}

type processEventSubscription struct {
	events chan ptycontract.ProcessEvent
}

func newProcessInventoryState() *processInventoryState {
	return &processInventoryState{
		sessions:  make(map[uint64]map[string]ptycontract.Process),
		processes: make(map[string]ptycontract.Process),
		observers: make(map[*processEventSubscription]struct{}),
	}
}

// replaceSession reduces one complete owned-session snapshot. Missing records become ended events;
// ended records are not retained in inventory because the snapshot describes what is running now.
func (state *processInventoryState) replaceSession(
	sessionID uint64,
	next []ptycontract.Process,
	endedAtUnixMs int64,
) []ptycontract.ProcessEvent {
	state.mu.Lock()
	defer state.mu.Unlock()

	previous := state.sessions[sessionID]
	if previous == nil {
		previous = make(map[string]ptycontract.Process)
	}
	nextByID := make(map[string]ptycontract.Process, len(next))
	for _, process := range next {
		nextByID[process.ID] = cloneProcess(process)
	}

	nextIDs := sortedProcessIDs(nextByID)
	endedIDs := make([]string, 0, len(previous))
	for id := range previous {
		if _, remains := nextByID[id]; !remains {
			endedIDs = append(endedIDs, id)
		}
	}
	sort.Strings(endedIDs)

	changes := make([]ptycontract.ProcessEvent, 0, len(nextIDs)+len(endedIDs))
	for _, id := range nextIDs {
		process := nextByID[id]
		before, existed := previous[id]
		kind := ptycontract.ProcessStarted
		if existed {
			if sameProcessRecord(before, process) {
				continue
			}
			kind = ptycontract.ProcessUpdated
		}
		changes = append(changes, state.publishLocked(kind, process))
		state.processes[id] = cloneProcess(process)
	}
	for _, id := range endedIDs {
		process := cloneProcess(previous[id])
		process.State = "ended"
		endedAt := endedAtUnixMs
		process.EndedAtUnixMs = &endedAt
		changes = append(changes, state.publishLocked(ptycontract.ProcessEnded, process))
		delete(state.processes, id)
	}

	if len(nextByID) == 0 {
		delete(state.sessions, sessionID)
	} else {
		state.sessions[sessionID] = nextByID
	}
	return changes
}

func (state *processInventoryState) publishLocked(kind string, process ptycontract.Process) ptycontract.ProcessEvent {
	state.revision++
	event := ptycontract.ProcessEvent{
		Revision: state.revision,
		Kind:     kind,
		Process:  cloneProcess(process),
	}
	for observer := range state.observers {
		select {
		case observer.events <- event:
		default:
			// Ending the bounded stream makes loss observable even when this is the final event. A peer
			// reconnects for a fresh atomic snapshot; the owner never blocks other process changes.
			delete(state.observers, observer)
			close(observer.events)
		}
	}
	return event
}

func (state *processInventoryState) snapshot() ptycontract.ProcessInventory {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.snapshotLocked()
}

func (state *processInventoryState) snapshotLocked() ptycontract.ProcessInventory {
	ids := sortedProcessIDs(state.processes)
	processes := make([]ptycontract.Process, 0, len(ids))
	for _, id := range ids {
		processes = append(processes, cloneProcess(state.processes[id]))
	}
	return ptycontract.ProcessInventory{Revision: state.revision, Processes: processes}
}

func (state *processInventoryState) observe() (
	ptycontract.ProcessInventory,
	<-chan ptycontract.ProcessEvent,
	func(),
) {
	state.mu.Lock()
	subscription := &processEventSubscription{events: make(chan ptycontract.ProcessEvent, processEventBuffer)}
	state.observers[subscription] = struct{}{}
	snapshot := state.snapshotLocked()
	state.mu.Unlock()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			state.mu.Lock()
			if _, exists := state.observers[subscription]; exists {
				delete(state.observers, subscription)
				close(subscription.events)
			}
			state.mu.Unlock()
		})
	}
	return snapshot, subscription.events, stop
}

func sortedProcessIDs[T any](records map[string]T) []string {
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sameProcessRecord(left, right ptycontract.Process) bool {
	return left.ID == right.ID && left.Owner == right.Owner &&
		sameOptionalString(left.Window, right.Window) && sameOptionalString(left.Pane, right.Pane) &&
		left.CWD == right.CWD && left.PID == right.PID && left.ParentPID == right.ParentPID &&
		left.Command == right.Command && left.State == right.State &&
		left.StartedAtUnixMs == right.StartedAtUnixMs &&
		sameOptionalInt64(left.EndedAtUnixMs, right.EndedAtUnixMs)
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneProcess(process ptycontract.Process) ptycontract.Process {
	process.Window = cloneOptionalString(process.Window)
	process.Pane = cloneOptionalString(process.Pane)
	if process.EndedAtUnixMs != nil {
		endedAt := *process.EndedAtUnixMs
		process.EndedAtUnixMs = &endedAt
	}
	return process
}

func cloneOptionalString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
