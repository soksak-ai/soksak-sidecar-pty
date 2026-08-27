package main

import (
	"io"
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

type resizeProcess struct{ cols, rows uint16 }

func (process *resizeProcess) Read([]byte) (int, error)       { return 0, io.EOF }
func (process *resizeProcess) Write(data []byte) (int, error) { return len(data), nil }
func (process *resizeProcess) Resize(cols, rows uint16) error {
	process.cols, process.rows = cols, rows
	return nil
}
func (*resizeProcess) PID() uint32      { return 1 }
func (*resizeProcess) Terminate() error { return nil }
func (*resizeProcess) Wait() error      { return nil }
func (*resizeProcess) Close() error     { return nil }

func TestObserverReceivesAbsoluteOutputRangesWithoutAffectingRendererAck(t *testing.T) {
	observer := newObserver(64)
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence:   1,
		FromSequence:    900_000,
		ThroughSequence: 900_004,
		Bytes:           []byte("aaaa"),
	})

	event := observer.next()
	if event.Output == nil {
		t.Fatal("observer received no output event")
	}
	if event.Output.FromSequence != 900_000 || event.Output.ThroughSequence != 900_004 {
		t.Fatalf("observer range = %d..%d", event.Output.FromSequence, event.Output.ThroughSequence)
	}
	if observer.gapCount() != 0 {
		t.Fatal("an observer that kept up reported a gap")
	}
}

func TestObserverCoalescesAdjacentQueuedOutputRanges(t *testing.T) {
	observer := newObserver(64)
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 1, FromSequence: 0, ThroughSequence: 4, Bytes: []byte("aaaa"),
	})
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 2, FromSequence: 4, ThroughSequence: 8, Bytes: []byte("bbbb"),
	})

	event := observer.next()
	if event.Output == nil || event.Output.EventSequence != 2 ||
		event.Output.FromSequence != 0 || event.Output.ThroughSequence != 8 ||
		string(event.Output.Bytes) != "aaaabbbb" {
		t.Fatalf("coalesced observer event = %#v", event)
	}
}

func TestSlowObserverReportsTheDroppedSourceRange(t *testing.T) {
	observer := newObserver(4)
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 1, FromSequence: 0, ThroughSequence: 4, Bytes: []byte("aaaa"),
	})
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 2, FromSequence: 4, ThroughSequence: 8, Bytes: []byte("bbbb"),
	})
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 3, FromSequence: 8, ThroughSequence: 10, Bytes: []byte("cc"),
	})

	first := observer.next()
	if first.Output == nil || string(first.Output.Bytes) != "aaaa" {
		t.Fatalf("first observer event = %#v", first)
	}
	gap := observer.next()
	if gap.Gap == nil || gap.Gap.FromSequence != 4 || gap.Gap.ThroughSequence != 10 {
		t.Fatalf("gap event = %#v", gap)
	}
	if observer.gapCount() != 1 {
		t.Fatalf("gap count = %d", observer.gapCount())
	}
}

func TestObserverStartsWithTheRetainedPrefix(t *testing.T) {
	session := &session{
		id: 7, generation: 3, ring: newRing(16), observers: make(map[*observer]struct{}),
	}
	session.written = session.ring.write([]byte("prompt"))
	session.eventSequence = 1

	observer, receipt, err := session.observe("", false)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.StartOutputSequence != 0 {
		t.Fatalf("start output sequence = %d", receipt.StartOutputSequence)
	}
	event := observer.next()
	if event.Output == nil || string(event.Output.Bytes) != "prompt" {
		t.Fatalf("retained event = %#v", event)
	}
}

func TestPreparedObserverReceivesOpenedBeforeOutput(t *testing.T) {
	observer := newObserver(1024)
	observer.publishOpened(ptycontract.OpenedObservation{
		Session: 7, Generation: 3, EventSequence: 0, OutputSequence: 0,
	})
	observer.publishOutput(ptycontract.OutputObservation{
		EventSequence: 1, FromSequence: 0, ThroughSequence: 1, Bytes: []byte("x"),
	})
	first := observer.next()
	if first.Opened == nil || first.Opened.Session != 7 {
		t.Fatalf("first event = %#v", first)
	}
	second := observer.next()
	if second.Output == nil || string(second.Output.Bytes) != "x" {
		t.Fatalf("second event = %#v", second)
	}
}

func TestSessionStatusReportsInitialAndAppliedSizes(t *testing.T) {
	process := &resizeProcess{cols: 80, rows: 24}
	value := &session{
		id: 7, paneID: "pane", generation: 1, process: process, ring: newRing(16),
		observers: make(map[*observer]struct{}), resume: make(chan struct{}, 1),
		cols: 80, rows: 24,
	}
	registry := &registry{sessions: map[uint64]*session{7: value}}
	initial := registry.list()[0]
	if initial.Cols != 80 || initial.Rows != 24 || initial.EventSequence != 0 {
		t.Fatalf("initial status = %+v", initial)
	}
	if err := value.resize(54, 20); err != nil {
		t.Fatal(err)
	}
	applied := registry.list()[0]
	if applied.Cols != 54 || applied.Rows != 20 || applied.EventSequence != 1 {
		t.Fatalf("applied status = %+v", applied)
	}
}
