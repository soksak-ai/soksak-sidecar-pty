package main

import (
	"testing"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

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

	observer, receipt, err := session.observe("")
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
