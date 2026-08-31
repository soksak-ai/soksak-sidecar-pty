package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

type mutableProcessTreeReader struct {
	mu      sync.Mutex
	entries []processTreeEntry
}

func (reader *mutableProcessTreeReader) Descendants(uint32) ([]processTreeEntry, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return append([]processTreeEntry(nil), reader.entries...), nil
}

func (reader *mutableProcessTreeReader) set(entries ...processTreeEntry) {
	reader.mu.Lock()
	reader.entries = append([]processTreeEntry(nil), entries...)
	reader.mu.Unlock()
}

type controlledProcessTreeEvents struct {
	mu           sync.Mutex
	callback     func()
	watch        *controlledProcessTreeWatch
	supportedErr error
}

func (source *controlledProcessTreeEvents) Supported() error { return source.supportedErr }

func (source *controlledProcessTreeEvents) Observe(_ uint32, callback func()) (processTreeWatch, error) {
	if source.supportedErr != nil {
		return nil, source.supportedErr
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	source.callback = callback
	source.watch = &controlledProcessTreeWatch{}
	return source.watch, nil
}

func (source *controlledProcessTreeEvents) signal() {
	source.mu.Lock()
	callback := source.callback
	source.mu.Unlock()
	if callback != nil {
		callback()
	}
}

type controlledProcessTreeWatch struct {
	mu     sync.Mutex
	closed bool
}

func (watch *controlledProcessTreeWatch) Sync(entries []processTreeEntry) ([]processTreeEntry, error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.closed {
		return nil, errors.New("watch is closed")
	}
	return append([]processTreeEntry(nil), entries...), nil
}

func (watch *controlledProcessTreeWatch) Close() error {
	watch.mu.Lock()
	watch.closed = true
	watch.mu.Unlock()
	return nil
}

func (watch *controlledProcessTreeWatch) isClosed() bool {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	return watch.closed
}

func processTestSession(id uint64) *session {
	return &session{
		id: id, paneID: "pane-1", windowLabel: "window-1", cwd: "/work",
		command: "/bin/zsh -l", startedAt: time.UnixMilli(1_700_000_000_000),
		process: &resizeProcess{}, ring: newRing(16), observers: map[*observer]struct{}{},
		observerTokens: map[string]*observer{}, displaying: map[*observer]struct{}{},
		resume: make(chan struct{}, 1),
	}
}

// One deterministic stimulus represents one native process-table notification. The owner must
// reduce each public record delta exactly once; a repeated snapshot is not a revision.
func TestProcessObserveEmitsGapFreeDescendantStartedUpdatedEndedAndClosesWatch(t *testing.T) {
	registry := newRegistry("/bin/sh")
	value := processTestSession(7)
	registry.sessions[value.id] = value
	tree := &mutableProcessTreeReader{}
	events := &controlledProcessTreeEvents{}
	d := &daemon{
		registry: registry, identity: componentID,
		processTree: tree, processTreeEvents: events,
	}
	d.startProcessMonitoring()

	server, client := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	done := make(chan struct{})
	request := controlwire.Request{ID: "observe-1", Command: ptycontract.CommandProcessObserve}
	go func() {
		d.processObserve(server, json.NewEncoder(server), request)
		close(done)
	}()
	reader := bufio.NewReader(client)
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))

	initial := readProcessObservationResponse(t, reader)
	if initial.Revision != 1 || len(initial.Processes) != 1 || initial.Processes[0].ID != "pty-session-7" {
		t.Fatalf("initial=%+v, want root at revision 1", initial)
	}

	child := processTreeEntry{PID: 22, ParentPID: 1, Command: "worker --first", CWD: "/work/one"}
	tree.set(child)
	events.signal()
	started := readProcessEvent(t, reader)

	child.Command = "worker --second"
	child.CWD = "/work/two"
	tree.set(child)
	events.signal()
	updated := readProcessEvent(t, reader)

	// The exact same snapshot is not a state change and must not consume a revision.
	events.signal()
	_, raw, err := d.processInventory(nil)
	if err != nil {
		t.Fatal(err)
	}
	unchanged := raw.(ptycontract.ProcessInventory)
	if unchanged.Revision != 3 {
		t.Fatalf("unchanged snapshot revision=%d, want 3", unchanged.Revision)
	}

	tree.set()
	events.signal()
	ended := readProcessEvent(t, reader)

	remaining := processTreeEntry{PID: 23, ParentPID: 1, Command: "worker --remaining", CWD: "/work/remaining"}
	tree.set(remaining)
	events.signal()
	remainingStarted := readProcessEvent(t, reader)

	if err := registry.close(value.id); err != nil {
		t.Fatal(err)
	}
	rootEnded := readProcessEvent(t, reader)
	remainingEnded := readProcessEvent(t, reader)

	want := []struct {
		event    ptycontract.ProcessEvent
		revision uint64
		kind     string
		id       string
		state    string
	}{
		{started, 2, ptycontract.ProcessStarted, "pty-session-7-process-22", "running"},
		{updated, 3, ptycontract.ProcessUpdated, "pty-session-7-process-22", "running"},
		{ended, 4, ptycontract.ProcessEnded, "pty-session-7-process-22", "ended"},
		{remainingStarted, 5, ptycontract.ProcessStarted, "pty-session-7-process-23", "running"},
		{rootEnded, 6, ptycontract.ProcessEnded, "pty-session-7", "ended"},
		{remainingEnded, 7, ptycontract.ProcessEnded, "pty-session-7-process-23", "ended"},
	}
	for _, item := range want {
		if item.event.Revision != item.revision || item.event.Kind != item.kind ||
			item.event.Process.ID != item.id || item.event.Process.State != item.state {
			t.Errorf("event=%+v, want revision=%d kind=%s id=%s state=%s",
				item.event, item.revision, item.kind, item.id, item.state)
		}
	}
	if started.Process.CWD != "/work/one" || updated.Process.CWD != "/work/two" ||
		updated.Process.Command != "worker --second" || ended.Process.EndedAtUnixMs == nil ||
		remainingEnded.Process.EndedAtUnixMs == nil {
		t.Fatalf("descendant payloads: started=%+v updated=%+v ended=%+v close-ended=%+v",
			started, updated, ended, remainingEnded)
	}
	if events.watch == nil || !events.watch.isClosed() {
		t.Fatal("session close did not close its native process watch")
	}
	_, raw, err = d.processInventory(nil)
	if err != nil {
		t.Fatal(err)
	}
	final := raw.(ptycontract.ProcessInventory)
	if final.Revision != 7 || len(final.Processes) != 0 {
		t.Fatalf("final=%+v, want empty revision 7 inventory", final)
	}

	_ = client.Close()
	<-done
}

func TestProcessObserveRefusesNamedUnsupportedEventSource(t *testing.T) {
	d := &daemon{
		registry: newRegistry("/bin/sh"), identity: componentID,
		processTreeEvents: &controlledProcessTreeEvents{supportedErr: ErrProcessObservationUnsupported},
	}
	server, client := net.Pipe()
	done := make(chan struct{})
	request := controlwire.Request{ID: "unsupported-1", Command: ptycontract.CommandProcessObserve}
	go func() {
		d.processObserve(server, json.NewEncoder(server), request)
		close(done)
	}()
	defer func() {
		_ = client.Close()
		<-done
	}()
	var response struct {
		ID     string `json:"id"`
		Ok     bool   `json:"ok"`
		Result struct {
			Code string `json:"code"`
		} `json:"result"`
	}
	if err := json.NewDecoder(client).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.ID != request.ID || response.Ok || response.Result.Code != ProcessObserveUnsupportedCode {
		t.Fatalf("unsupported response=%+v", response)
	}
}

func readProcessObservationResponse(t *testing.T, reader *bufio.Reader) ptycontract.ProcessInventory {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Ok     bool `json:"ok"`
		Result struct {
			Code string                       `json:"code"`
			Data ptycontract.ProcessInventory `json:"data"`
		} `json:"result"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(line, &response); err != nil || !response.Ok || response.Result.Code != "OK" {
		t.Fatalf("initial=%s response=%+v err=%v", line, response, err)
	}
	return response.Result.Data
}

func readProcessEvent(t *testing.T, reader *bufio.Reader) ptycontract.ProcessEvent {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var event ptycontract.ProcessEvent
	if err := json.Unmarshal(line, &event); err != nil {
		t.Fatal(err)
	}
	return event
}
