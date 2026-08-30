package main

import (
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// An observer adopted by a running pane receives the retained prefix before
// later output. Its mirror must not start at the current sequence with no
// content.
func TestAdoptObserverReplaysTheRetainedPrefixBeforeLiveBytes(t *testing.T) {
	value := &session{
		id: 3, generation: 4, paneID: "pane.1", windowLabel: "win",
		ring:           newRing(16),
		observers:      make(map[*observer]struct{}),
		observerTokens: make(map[string]*observer),
		displaying:     make(map[*observer]struct{}),
		resume:         make(chan struct{}, 1),
		eventSequence:  7,
	}
	value.written = value.ring.write([]byte("prompt"))
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
		event.Opened.EventSequence != 7 || event.Opened.OutputSequence != 0 {
		t.Fatalf("opened = %+v, want session 3 gen 4 at retained floor 7/0", *event.Opened)
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
			EventSequence: 8, FromSequence: 6, ThroughSequence: 10, Bytes: []byte("late"),
		})
	}
	value.mu.Unlock()
	if output := prepared.observer.next(); output.Output == nil || string(output.Output.Bytes) != "promptlate" ||
		output.Output.FromSequence != 0 || output.Output.ThroughSequence != 10 {
		t.Fatalf("adopted observer missed the retained prefix: %#v", output)
	}
}

// Two daemon instances never share a generation space: a screen archived under
// one boot's shell must not match a different boot's shell that happened to be
// opened in the same order.
func TestGenerationsDifferAcrossRegistryInstances(t *testing.T) {
	first := newRegistry("/bin/sh")
	second := newRegistry("/bin/sh")
	if first.generation == second.generation {
		t.Fatalf("two registries started at the same generation %d", first.generation)
	}
}
