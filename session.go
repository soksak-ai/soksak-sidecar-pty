package main

import (
	"fmt"
	"sync"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

type sessionProcess interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows uint16) error
	PID() uint32
	Terminate() error
	Wait() error
	Close() error
}

// session is one shell with a tty, its output ring, and what the caller told this daemon about it.
//
// PaneID and WindowLabel are opaque here. This daemon never resolves them, never groups by them and
// never decides anything from them — they travel back out so the caller can match a session to
// whatever it drew. A daemon that read them would be deciding what a pane is.
type session struct {
	id          uint64
	paneID      string
	windowLabel string
	generation  uint64

	process        sessionProcess
	ring           *ring
	observers      map[*observer]struct{}
	observerTokens map[string]*observer

	mu      sync.Mutex
	written uint64
	closed  bool
	// paused is what the reader is doing, recorded rather than derived. A caller deriving it from
	// written and acked would be applying the watermark rule a second time, and two applications of
	// one rule are two answers waiting to disagree.
	paused           bool
	rendererAttached bool
	// resume releases the reader when a paused client has acked back down to the low mark.
	resume        chan struct{}
	eventSequence uint64
}

type registry struct {
	mu         sync.Mutex
	next       uint64
	generation uint64
	sessions   map[uint64]*session
	shell      string
	stopped    bool
}

func newRegistry(shell string) *registry {
	return &registry{sessions: make(map[uint64]*session), shell: shell}
}

// open starts a shell and returns its session.
//
// The shell, the environment and the working directory all arrive from the caller. Reading them
// here would tie a session to whatever launched this daemon, and this process outlives the one that
// launched it — that is the reason it is a process at all.
func (reg *registry) open(request ptycontract.Open) (*session, error) {
	return reg.openWithObserver(request, nil)
}

func (reg *registry) openWithObserver(
	request ptycontract.Open,
	prepared *preparedObserver,
) (*session, error) {
	if request.Cols == 0 || request.Rows == 0 {
		return nil, fmt.Errorf("a session needs a size: cols=%d rows=%d", request.Cols, request.Rows)
	}
	shell := request.Shell
	if shell == "" {
		shell = reg.shell
	}
	if shell == "" {
		return nil, fmt.Errorf("no shell was named and this daemon derives none")
	}

	process, err := startSessionProcess(
		shell, request.CWD,
		sessionEnvironment(request.Environment, request.EnvironmentDrop),
		request.Cols, request.Rows,
	)
	if err != nil {
		return nil, fmt.Errorf("open a session for pane %s: %w", request.PaneID, err)
	}

	reg.mu.Lock()
	if reg.stopped {
		reg.mu.Unlock()
		_ = process.Terminate()
		_ = process.Close()
		_ = process.Wait()
		return nil, fmt.Errorf("this daemon is shutting down")
	}
	reg.next++
	reg.generation++
	value := &session{
		id:             reg.next,
		paneID:         request.PaneID,
		windowLabel:    request.WindowLabel,
		generation:     reg.generation,
		process:        process,
		ring:           newRing(ptycontract.HighWatermark),
		observers:      make(map[*observer]struct{}),
		observerTokens: make(map[string]*observer),
		resume:         make(chan struct{}, 1),
	}
	if prepared != nil {
		value.observers[prepared.observer] = struct{}{}
		value.observerTokens[request.ObserverToken] = prepared.observer
	}
	reg.sessions[value.id] = value
	reg.mu.Unlock()
	if prepared != nil {
		prepared.observer.publishOpened(ptycontract.OpenedObservation{
			Session: value.id, Generation: value.generation,
			EventSequence: 0, OutputSequence: 0,
		})
	}

	go value.pump()
	return value, nil
}

// pump moves bytes off the master into the ring, and holds off while the client is behind.
//
// The pause is on unacked bytes rather than on ring occupancy. A client keeping up leaves this
// running however much the ring holds; a client that stopped reading stops this however little it
// holds. Without it a shell printing faster than anything reads grows this process without bound.
func (value *session) pump() {
	buffer := make([]byte, 32*1024)
	for {
		count, err := value.process.Read(buffer)
		if count > 0 {
			value.mu.Lock()
			from := value.written
			value.written = value.ring.write(buffer[:count])
			value.eventSequence++
			eventSequence := value.eventSequence
			written := value.written
			for observer := range value.observers {
				observer.publishOutput(ptycontract.OutputObservation{
					EventSequence: eventSequence, FromSequence: from,
					ThroughSequence: written, Bytes: buffer[:count],
				})
			}
			value.mu.Unlock()

			for value.shouldPause(written) {
				value.mu.Lock()
				value.paused = true
				value.mu.Unlock()
				<-value.resume
			}
			value.mu.Lock()
			value.paused = false
			value.mu.Unlock()
		}
		if err != nil {
			value.ring.end()
			value.reap()
			return
		}
	}
}

func (value *session) attachRenderer() error {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.rendererAttached {
		return fmt.Errorf("session %d already has an attached renderer", value.id)
	}
	value.rendererAttached = true
	return nil
}

func (value *session) detachRenderer() {
	value.mu.Lock()
	value.rendererAttached = false
	value.mu.Unlock()
	select {
	case value.resume <- struct{}{}:
	default:
	}
}

func (value *session) shouldPause(written uint64) bool {
	value.mu.Lock()
	attached := value.rendererAttached
	value.mu.Unlock()
	return attached && value.ring.paused(written)
}

func (value *session) write(data []byte) error {
	value.mu.Lock()
	closed := value.closed
	value.mu.Unlock()
	if closed {
		return fmt.Errorf("session %d has ended", value.id)
	}
	_, err := value.process.Write(data)
	return err
}

func (value *session) resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return fmt.Errorf("a session cannot be sized to cols=%d rows=%d", cols, rows)
	}
	if err := value.process.Resize(cols, rows); err != nil {
		return err
	}
	value.mu.Lock()
	value.eventSequence++
	event := ptycontract.ResizeObservation{EventSequence: value.eventSequence, Cols: cols, Rows: rows}
	for observer := range value.observers {
		observer.publishResize(event)
	}
	value.mu.Unlock()
	return nil
}

func (value *session) observe(token string) (*observer, ptycontract.Observed, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if token != "" {
		observer := value.observerTokens[token]
		if observer == nil {
			return nil, ptycontract.Observed{}, fmt.Errorf("observer token is not bound to session %d", value.id)
		}
		delete(value.observerTokens, token)
		return observer, ptycontract.Observed{
			Session: value.id, Generation: value.generation,
			StartEventSequence: value.eventSequence, StartOutputSequence: value.written,
		}, nil
	}
	observer := newObserver(ptycontract.ObserverBufferBytes)
	value.observers[observer] = struct{}{}
	floor, through, retained := value.ring.snapshot()
	if floor > 0 {
		observer.mu.Lock()
		observer.publishGapLocked(ptycontract.GapObservation{
			FromEventSequence: 0, ThroughEventSequence: value.eventSequence,
			FromSequence: 0, ThroughSequence: floor,
		})
		observer.mu.Unlock()
	}
	if len(retained) > 0 {
		observer.publishOutput(ptycontract.OutputObservation{
			EventSequence: value.eventSequence, FromSequence: floor,
			ThroughSequence: through, Bytes: retained,
		})
	}
	return observer, ptycontract.Observed{
		Session: value.id, Generation: value.generation,
		StartEventSequence: value.eventSequence, StartOutputSequence: floor,
	}, nil
}

func (value *session) removeObserver(observer *observer) {
	value.mu.Lock()
	delete(value.observers, observer)
	value.mu.Unlock()
	observer.close()
}

// ack records the client's progress and releases the reader when it is far enough back.
func (value *session) ack(bytes uint64) {
	value.ring.ack(bytes)
	value.mu.Lock()
	written := value.written
	value.mu.Unlock()
	if value.ring.resumed(written) {
		select {
		case value.resume <- struct{}{}:
		default:
		}
	}
}

// reap ends the shell and every process it started.
//
// The whole group, not the shell alone: a shell that started a build leaves it running when only
// the shell is signalled, and what is left has no terminal, no parent watching it, and no way for
// anyone to find it again.
func (value *session) reap() {
	value.mu.Lock()
	if value.closed {
		value.mu.Unlock()
		return
	}
	value.closed = true
	value.eventSequence++
	end := ptycontract.EndObservation{EventSequence: value.eventSequence}
	for observer := range value.observers {
		observer.publishEnd(end)
	}
	value.mu.Unlock()

	_ = value.process.Terminate()
	_ = value.process.Close()
	_ = value.process.Wait()
	value.ring.end()
	select {
	case value.resume <- struct{}{}:
	default:
	}
}

func (reg *registry) get(id uint64) (*session, error) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	value := reg.sessions[id]
	if value == nil {
		return nil, fmt.Errorf("no session %d in this daemon", id)
	}
	return value, nil
}

func (reg *registry) close(id uint64) error {
	reg.mu.Lock()
	value := reg.sessions[id]
	delete(reg.sessions, id)
	reg.mu.Unlock()
	if value == nil {
		return fmt.Errorf("no session %d in this daemon", id)
	}
	value.reap()
	return nil
}

// byPane answers the session a pane holds, and whether it holds one.
//
// The pane id is the caller's coordinate and this daemon compares it as a string. Two panes with
// one id would be the caller's fault and it would show here as one of them attaching to the other's
// shell, so the first match is not a guess: ids are unique or the caller has already lost track.
func (reg *registry) byPane(paneID string) (*session, bool) {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	for _, value := range reg.sessions {
		if value.paneID == paneID {
			return value, true
		}
	}
	return nil, false
}

func (reg *registry) list() []ptycontract.Info {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]ptycontract.Info, 0, len(reg.sessions))
	for _, value := range reg.sessions {
		label := value.windowLabel
		var window *string
		if label != "" {
			window = &label
		}
		pid := value.process.PID()
		value.mu.Lock()
		written, paused := value.written, value.paused
		value.mu.Unlock()
		acked, retained := value.ring.state()
		out = append(out, ptycontract.Info{
			Session:     value.id,
			PaneID:      value.paneID,
			ShellPID:    pid,
			Generation:  value.generation,
			WindowLabel: window,
			Written:     written,
			Acked:       acked,
			Paused:      paused,
			Retained:    retained,
		})
	}
	return out
}

// shutdown ends every session. Only what this daemon started is ended: a process it adopted is one
// whose arguments it never chose and whose work it cannot know.
func (reg *registry) shutdown() int {
	reg.mu.Lock()
	reg.stopped = true
	values := make([]*session, 0, len(reg.sessions))
	for id, value := range reg.sessions {
		values = append(values, value)
		delete(reg.sessions, id)
	}
	reg.mu.Unlock()
	for _, value := range values {
		value.reap()
	}
	return len(values)
}
