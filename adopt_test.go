package main

import (
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// An open that finds its pane already served attaches the observer it was
// handed; the observer starts at the session's current coordinates and sees
// every byte pumped after that.
func TestAdoptObserverAttachesToTheRunningSessionAtItsCurrentCoordinates(t *testing.T) {
	value := &session{
		id: 3, generation: 4, paneID: "pane.1", windowLabel: "win",
		ring:           newRing(ptycontract.HighWatermark),
		observers:      make(map[*observer]struct{}),
		observerTokens: make(map[string]*observer),
		displaying:     make(map[*observer]struct{}),
		resume:         make(chan struct{}, 1),
		eventSequence:  7, written: 512,
	}
	prepared := &preparedObserver{
		request:  ptycontract.PrepareObserver{PaneID: "pane.1", WindowLabel: "win", Provider: "engine", Displays: true},
		observer: newObserver(ptycontract.ObserverBufferBytes),
	}
	value.adoptObserver("token-1", prepared)

	event := prepared.observer.next()
	if event.Opened == nil {
		t.Fatalf("adopted observer received %#v, want an opened event", event)
	}
	if event.Opened.Session != 3 || event.Opened.Generation != 4 ||
		event.Opened.EventSequence != 7 || event.Opened.OutputSequence != 512 {
		t.Fatalf("opened = %+v, want session 3 gen 4 at 7/512", *event.Opened)
	}

	value.mu.Lock()
	_, attached := value.observers[prepared.observer]
	_, displaying := value.displaying[prepared.observer]
	bound := value.observerTokens["token-1"] == prepared.observer
	value.mu.Unlock()
	if !attached || !displaying || !bound {
		t.Fatalf("attached=%v displaying=%v bound=%v, want all true", attached, displaying, bound)
	}

	value.mu.Lock()
	for observer := range value.observers {
		observer.publishOutput(ptycontract.OutputObservation{
			EventSequence: 8, FromSequence: 512, ThroughSequence: 516, Bytes: []byte("late"),
		})
	}
	value.mu.Unlock()
	if output := prepared.observer.next(); output.Output == nil || string(output.Output.Bytes) != "late" {
		t.Fatalf("adopted observer missed the pumped bytes: %#v", output)
	}
}
