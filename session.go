package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"sync"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
	"time"
)

type sessionProcess interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Resize(cols, rows uint16) error
	PID() uint32
	// ForegroundGroup is the process group the terminal is giving the keyboard to. Zero where the
	// platform does not report one, which is a terminal that cannot say rather than a shell with no
	// program in front.
	ForegroundGroup() uint32
	Terminate() error
	Wait() error
	Close() error
}

// processTreeReader reports descendants of one process. It is injected at the daemon
// boundary so the inventory contract can be tested without reading another process's source or
// making a test depend on the host process table.
type processTreeReader interface {
	Descendants(root uint32) ([]processTreeEntry, error)
}

type processTreeEntry struct {
	PID       uint32
	ParentPID uint32
	// GroupID is the process group. A terminal gives the keyboard to one group at a time, so this
	// is what says which of a shell's children is in front rather than merely running.
	GroupID uint32
	Command string
	CWD     string
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
	cwd         string
	command     string
	startedAt   time.Time
	generation  uint64

	process        sessionProcess
	ring           *ring
	observers      map[*observer]struct{}
	observerTokens map[string]*observer
	// displaying is the subset of observers showing this session to someone. A member here is
	// a picture of the shell, and the abandonment judgment treats it exactly as a renderer.
	displaying map[*observer]struct{}

	mu      sync.Mutex
	written uint64
	closed  bool
	// paused is what the reader is doing, recorded rather than derived. A caller deriving it from
	// written and acked would be applying the watermark rule a second time, and two applications of
	// one rule are two answers waiting to disagree.
	paused             bool
	rendererGeneration uint64
	rendererAttached   bool
	// detachedAt is when this session last had no renderer. Zero means one is attached.
	detachedAt time.Time
	// now is the registry's clock, so a detach is stamped by the same clock that judges it.
	now func() time.Time
	// writtenAt is when this session last produced output. A session still writing is working.
	writtenAt time.Time
	// resume releases the reader when a paused client has acked back down to the low mark.
	resume        chan struct{}
	eventSequence uint64
	cols          uint16
	rows          uint16
	processEnded  func(*session, int64)
	// environment is what the caller named. Without it a session recreated from this record starts
	// under whatever environment this daemon inherited, which is not what was asked for.
	environment []string
	// store is where this session's output is appended. Nil while no store is attached.
	store *store
	// feed carries this session's output to the store. The store is a subscriber and never pauses
	// the pump, so output goes through here rather than being written from it (S4-5).
	feed *storeFeed
}

type registry struct {
	mu             sync.Mutex
	next           uint64
	generation     uint64
	sessions       map[uint64]*session
	shell          string
	stopped        bool
	processStarted func(*session)
	processEnded   func(*session, int64)
	// A session with no renderer is kept this long so a view that unmounted can mount again and
	// reattach. Past it the session is what a run that went away left behind, and it ends: nothing
	// can reach that shell, and it holds a process, its output ring and its file descriptors.
	abandonAfter time.Duration
	now          func() time.Time
	// store is where a session's state outlives this process. A registry without one keeps its
	// sessions in memory alone, which is what every test that does not exercise the store wants.
	store *store
	// outcomes is what the start ended in per session, so a caller reads the same answer however
	// long after the start it asks.
	outcomes map[uint64]restoreOutcome
	// readStore states that the store was read through. A caller that took an unfinished report as
	// final would count a session lost this daemon had not looked for yet.
	readStore bool
}

// attachStore gives this registry the place its sessions outlive it in.
func (reg *registry) attachStore(value *store) {
	reg.mu.Lock()
	reg.store = value
	reg.mu.Unlock()
}

// sessionStore reads the store without holding the registry lock through a file operation.
func (reg *registry) sessionStore() *store {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	return reg.store
}

// bindProcessLifecycle installs the one process-ledger boundary before sessions are served. It
// also returns sessions that predate the binding (used by deterministic owner tests and adoption).
func (reg *registry) bindProcessLifecycle(
	started func(*session),
	ended func(*session, int64),
) []*session {
	reg.mu.Lock()
	reg.processStarted = started
	reg.processEnded = ended
	existing := make([]*session, 0, len(reg.sessions))
	for _, value := range reg.sessions {
		value.mu.Lock()
		value.processEnded = ended
		value.mu.Unlock()
		existing = append(existing, value)
	}
	reg.mu.Unlock()
	return existing
}

const defaultAbandonAfter = 30 * time.Minute

// seedCeiling bounds a counter's floor. Both counters travel as JSON numbers, and a generic
// parser holds a JSON number as a float64, which is exact to 2^53. A floor under 2^48 stays exact
// and leaves 2^53-2^48 of counting room, which no daemon reaches.
const seedCeiling = uint64(1) << 48

// randomInstanceSeed draws a floor for one of this instance's counters. Randomness
// rather than the clock: two instances created in the same nanosecond are the
// same seed, and the tests meet exactly that.
func randomInstanceSeed() uint64 {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return uint64(time.Now().UnixNano()) % seedCeiling
	}
	return binary.BigEndian.Uint64(bytes[:]) % seedCeiling
}

func newRegistry(shell string) *registry {
	return &registry{
		sessions: make(map[uint64]*session), shell: shell,
		abandonAfter: defaultAbandonAfter, now: time.Now,
		// Both counters are seeded per daemon instance. A generation counter that
		// started at zero every boot handed the same generation to the same
		// pane on every restart, and a screen archived under it stood back up
		// under a shell it never belonged to. A session id names a record that
		// outlives this process, so a repeated id makes one boot read the
		// previous boot's record as its own.
		generation: randomInstanceSeed(),
		next:       randomInstanceSeed(),
	}
}

// issueLocked answers the next id this instance hands out, skipping any a session already holds.
//
// A restore registers under the id the session had, and this counts up from a seed drawn for this
// instance. The two spaces are drawn apart and nothing stopped them from meeting: a collision would
// replace a live session's map entry, leaving a shell running with nothing addressing it.
//
// The caller holds the registry lock.
func (reg *registry) issueLocked() uint64 {
	for {
		reg.next++
		if _, taken := reg.sessions[reg.next]; !taken {
			return reg.next
		}
	}
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

	// One computation for both the process and the record. Two would be two answers waiting to
	// disagree, and the record is what a recreated shell is started from.
	environment := sessionEnvironment(request.Environment, request.EnvironmentDrop)
	process, err := startSessionProcess(shell, request.CWD, environment, request.Cols, request.Rows)
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
	reg.generation++
	value := &session{
		id:             reg.issueLocked(),
		paneID:         request.PaneID,
		windowLabel:    request.WindowLabel,
		cwd:            request.CWD,
		command:        shell + " -l",
		startedAt:      reg.now(),
		generation:     reg.generation,
		process:        process,
		ring:           newRing(ptycontract.HighWatermark),
		observers:      make(map[*observer]struct{}),
		observerTokens: make(map[string]*observer),
		displaying:     make(map[*observer]struct{}),
		now:            reg.now,
		resume:         make(chan struct{}, 1),
		cols:           request.Cols,
		rows:           request.Rows,
		environment:    environment,
		processEnded:   reg.processEnded,
	}
	if prepared != nil {
		value.observers[prepared.observer] = struct{}{}
		value.observerTokens[request.ObserverToken] = prepared.observer
		if prepared.request.Displays {
			value.displaying[prepared.observer] = struct{}{}
		}
	}
	reg.sessions[value.id] = value
	processStarted := reg.processStarted
	value.store = reg.store
	reg.mu.Unlock()

	// The creation write comes before the pump. Everything a crash preserves starts here, so a
	// session that produced nothing before the crash is still recreatable from it.
	if value.store != nil {
		if err := value.store.create(value.creationFacts()); err != nil {
			fmt.Fprintf(os.Stderr,
				"soksak-sidecar-pty: session %d has no record and will not survive this process: %v\n",
				value.id, err)
			value.store = nil
		} else {
			value.feed = value.storeFeedFor()
		}
	}
	if processStarted != nil {
		processStarted(value)
	}
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
			// The store takes the same bytes and the coordinate they end at. It is a subscriber like
			// any other and never pauses this loop: a write that fails costs the record's tail and
			// is reported, and the shell keeps running.
			//
			// After the ring rather than before, because the coordinate is what the ring answers and
			// a store told a coordinate the ring had not reached would name a byte nobody wrote.
			if value.feed != nil {
				value.feed.offer(buffer[:count], value.written)
			}
			value.writtenAt = value.stamp()
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
			// The shell exited, so this session is over.
			value.reap(true)
			return
		}
	}
}

// attachRenderer makes the caller this session's renderer. A session has one, and it is the last to
// attach: a run that went away without detaching left a mark, and refusing the next attach because
// of it leaves a pane nothing can ever draw again. The one that left holds an older generation, so
// its detach cannot take the one that replaced it.
func (value *session) attachRenderer() (uint64, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	value.rendererGeneration++
	if value.rendererGeneration == 0 {
		value.rendererGeneration = 1
	}
	value.rendererAttached = true
	value.detachedAt = time.Time{}
	return value.rendererGeneration, nil
}

func (value *session) replaceRenderer() uint64 {
	value.mu.Lock()
	value.rendererGeneration++
	if value.rendererGeneration == 0 {
		value.rendererGeneration = 1
	}
	generation := value.rendererGeneration
	value.rendererAttached = true
	value.detachedAt = time.Time{}
	value.mu.Unlock()
	return generation
}

func (value *session) detachRenderer(generation uint64) {
	value.mu.Lock()
	if value.rendererAttached && value.rendererGeneration == generation {
		value.rendererAttached = false
		if !value.presenceLocked() {
			value.detachedAt = value.stamp()
		}
	}
	value.mu.Unlock()
	select {
	case value.resume <- struct{}{}:
	default:
	}
}

func (value *session) detachActiveRenderer() bool {
	value.mu.Lock()
	detached := value.rendererAttached
	value.rendererAttached = false
	if detached && !value.presenceLocked() {
		value.detachedAt = value.stamp()
	}
	value.mu.Unlock()
	if detached {
		select {
		case value.resume <- struct{}{}:
		default:
		}
	}
	return detached
}

func (value *session) stamp() time.Time {
	if value.now == nil {
		return time.Now()
	}
	return value.now()
}

// abandonedSince answers whether this session has had no renderer and produced no output since
// before the given moment. A session with a renderer, one that has never had one, and one still
// writing are all doing work for someone.
func (value *session) abandonedSince(moment time.Time) bool {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.presenceLocked() || value.detachedAt.IsZero() || !value.detachedAt.Before(moment) {
		return false
	}
	return value.writtenAt.IsZero() || value.writtenAt.Before(moment)
}

func (value *session) rendererIsAttached() bool {
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.rendererAttached
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
	value.cols = cols
	value.rows = rows
	event := ptycontract.ResizeObservation{EventSequence: value.eventSequence, Cols: cols, Rows: rows}
	for observer := range value.observers {
		observer.publishResize(event)
	}
	held := value.store
	value.mu.Unlock()
	// The size is session state (SESSION-STATE.md): a restore reapplies it, so the record follows
	// the pty. Written outside the session lock, in the record's own lock like every record write.
	if held != nil {
		if err := held.setSize(value.id, cols, rows); err != nil {
			fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: session %d resized and its record keeps the old size: %v\n", value.id, err)
		}
	}
	return nil
}

func (value *session) observe(token string, displays bool) (*observer, ptycontract.Observed, error) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if token != "" {
		observer := value.observerTokens[token]
		if observer == nil {
			return nil, ptycontract.Observed{}, fmt.Errorf("observer token is not bound to session %d", value.id)
		}
		delete(value.observerTokens, token)
		if displays {
			value.displaying[observer] = struct{}{}
			value.detachedAt = time.Time{}
		}
		return observer, ptycontract.Observed{
			Session: value.id, Generation: value.generation,
			StartEventSequence: value.eventSequence, StartOutputSequence: value.written,
		}, nil
	}
	observer := newObserver(ptycontract.ObserverBufferBytes)
	value.observers[observer] = struct{}{}
	if displays {
		value.displaying[observer] = struct{}{}
		value.detachedAt = time.Time{}
	}
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
	_, showed := value.displaying[observer]
	delete(value.observers, observer)
	delete(value.displaying, observer)
	for token, bound := range value.observerTokens {
		if bound == observer {
			delete(value.observerTokens, token)
		}
	}
	if showed && !value.presenceLocked() && value.detachedAt.IsZero() {
		value.detachedAt = value.stamp()
	}
	value.mu.Unlock()
	observer.close()
}

// presenceLocked answers whether anything is showing this session: an attached renderer or a
// displaying observer. Callers hold value.mu.
func (value *session) presenceLocked() bool {
	return value.rendererAttached || len(value.displaying) > 0
}

// setDisplays flips whether the token-bound observer is showing this session. Gaining a display
// clears the detach stamp exactly as a renderer attach does; losing the last one starts the
// abandonment clock, renderer or none.
func (value *session) setDisplays(token string, displays bool) error {
	value.mu.Lock()
	defer value.mu.Unlock()
	observer := value.observerTokens[token]
	if observer == nil {
		return fmt.Errorf("observer token is not bound to session %d", value.id)
	}
	if displays {
		value.displaying[observer] = struct{}{}
		value.detachedAt = time.Time{}
		return nil
	}
	if _, showed := value.displaying[observer]; showed {
		delete(value.displaying, observer)
		if !value.presenceLocked() {
			value.detachedAt = value.stamp()
		}
	}
	return nil
}

// ack records the client's progress and releases the reader when it is far enough back.
func (value *session) ack(bytes uint64) {
	value.mu.Lock()
	written := value.written
	if bytes > written {
		bytes = written
	}
	value.ring.ack(bytes)
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
// reap ends this session's process.
//
// `ended` states whether the session itself is over. A shell that exited, an abandon sweep and a
// close all end one, and the record goes with it — a record that outlived its session is a session
// a later start stands back up after the person ended it (S3-1).
//
// A daemon stopping is the other case: the session is not over, its record is what a restore reads,
// and taking it would turn every stop into a close.
func (value *session) reap(ended bool) {
	value.mu.Lock()
	if value.closed {
		value.mu.Unlock()
		return
	}
	value.closed = true
	held := value.store
	feed := value.feed
	value.eventSequence++
	end := ptycontract.EndObservation{EventSequence: value.eventSequence}
	for observer := range value.observers {
		observer.publishEnd(end)
	}
	processEnded := value.processEnded
	endedAt := value.stamp().UnixMilli()
	value.mu.Unlock()
	// What the feed already accepted reaches the store before anything writes the record. A stop
	// that ran ahead of the queue would record a coordinate the stored output does not reach.
	if feed != nil {
		feed.close()
	}
	if processEnded != nil {
		processEnded(value, endedAt)
	}

	// The record goes with the session, whichever way it ended. Only an explicit close removed one,
	// so a shell exiting and the abandon sweep both left theirs — and the next start stood a brand
	// new shell up for a session the person had ended. A closed session is not recoverable (S3-1).
	if ended && held != nil {
		if err := held.remove(value.id); err != nil {
			fmt.Fprintf(os.Stderr,
				"soksak-sidecar-pty: session %d ended and its record remains: %v\n", value.id, err)
		}
	}

	_ = value.process.Terminate()
	_ = value.process.Close()
	_ = value.process.Wait()
	value.ring.end()
	select {
	case value.resume <- struct{}{}:
	default:
	}
}

// endAbandoned ends every session no renderer has been attached to for longer than the window, and
// answers how many ended.
func (reg *registry) endAbandoned() int {
	if reg.abandonAfter <= 0 {
		return 0
	}
	deadline := reg.now().Add(-reg.abandonAfter)
	reg.mu.Lock()
	abandoned := make([]*session, 0, len(reg.sessions))
	for id, value := range reg.sessions {
		if value.abandonedSince(deadline) {
			abandoned = append(abandoned, value)
			delete(reg.sessions, id)
		}
	}
	reg.mu.Unlock()
	for _, value := range abandoned {
		// Nothing reattached inside the window, so the session is over.
		value.reap(true)
	}
	return len(abandoned)
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
	held := reg.store
	reg.mu.Unlock()
	// The record goes with the session. One left behind is a session a later start hands back after
	// it was closed, and a closed session is not recoverable.
	if held != nil {
		if err := held.remove(id); err != nil {
			fmt.Fprintf(os.Stderr,
				"soksak-sidecar-pty: session %d closed and its record remains: %v\n", id, err)
		}
	}
	reg.forgetOutcome(id)
	if value == nil {
		return fmt.Errorf("no session %d in this daemon", id)
	}
	value.reap(true)
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
		cols, rows, eventSequence := value.cols, value.rows, value.eventSequence
		value.mu.Unlock()
		acked, retained := value.ring.state()
		out = append(out, ptycontract.Info{
			Session:       value.id,
			PaneID:        value.paneID,
			ShellPID:      pid,
			Generation:    value.generation,
			WindowLabel:   window,
			Written:       written,
			Acked:         acked,
			Paused:        paused,
			Retained:      retained,
			Cols:          cols,
			Rows:          rows,
			EventSequence: eventSequence,
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
	held := reg.store
	reg.mu.Unlock()
	for _, value := range values {
		// The stop write comes before the reap. It is the point a power cycle recovers from, and
		// the mark it leaves is the only evidence that this exit was on purpose.
		if held != nil {
			at := reg.now().UnixMilli()
			value.mu.Lock()
			through := value.written
			value.mu.Unlock()
			if err := held.markEnded(value.id, at, nil, through); err != nil {
				fmt.Fprintf(os.Stderr,
					"soksak-sidecar-pty: session %d stopped without its record marked: %v\n",
					value.id, err)
			}
		}
		// The daemon is stopping and the sessions are not. Their records are what the next start
		// reads, and taking them would turn every stop into a close.
		value.reap(false)
	}
	return len(values)
}

// creationFacts is what an equivalent session is created from.
func (value *session) creationFacts() sessionRecord {
	return sessionRecord{
		Session: value.id, PaneID: value.paneID, WindowLabel: value.windowLabel,
		CWD: value.cwd, Command: value.command, Environment: value.environment,
		Cols: value.cols, Rows: value.rows, StartedAtUnixMs: value.startedAt.UnixMilli(),
	}
}

// observerCounts reports, per session, how many observation consumers the pump
// fans out to. Diagnostic: the counts name where a silent pane lost its feed.
func (reg *registry) observerCounts() []map[string]any {
	reg.mu.Lock()
	defer reg.mu.Unlock()
	out := make([]map[string]any, 0, len(reg.sessions))
	for _, value := range reg.sessions {
		value.mu.Lock()
		out = append(out, map[string]any{
			"session":    value.id,
			"paneId":     value.paneID,
			"observers":  len(value.observers),
			"displaying": len(value.displaying),
			"tokens":     len(value.observerTokens),
			"closed":     value.closed,
		})
		value.mu.Unlock()
	}
	return out
}

// adoptObserver attaches a prepared observer to this running session. Opened
// identifies the retained floor; retained output follows before any live byte.
// Holding the session lock makes that ordering atomic with the pump.
func (value *session) adoptObserver(token string, prepared *preparedObserver) {
	value.mu.Lock()
	value.observers[prepared.observer] = struct{}{}
	value.observerTokens[token] = prepared.observer
	if prepared.request.Displays {
		value.displaying[prepared.observer] = struct{}{}
	}
	eventSequence := value.eventSequence
	generation := value.generation
	floor, through, retained := value.ring.snapshot()
	prepared.observer.publishOpened(ptycontract.OpenedObservation{
		Session: value.id, Generation: generation,
		EventSequence: eventSequence, OutputSequence: floor,
	})
	if floor > 0 {
		prepared.observer.publishGap(ptycontract.GapObservation{
			FromEventSequence: 0, ThroughEventSequence: eventSequence,
			FromSequence: 0, ThroughSequence: floor,
		})
	}
	if len(retained) > 0 {
		prepared.observer.publishOutput(ptycontract.OutputObservation{
			EventSequence: eventSequence, FromSequence: floor,
			ThroughSequence: through, Bytes: retained,
		})
	}
	value.mu.Unlock()
}

// storeFeedFor builds this session's feed over its store.
func (value *session) storeFeedFor() *storeFeed {
	held, id := value.store, value.id
	return newStoreFeed(id, func(data []byte, through uint64) error {
		return held.append(id, data, through)
	})
}
