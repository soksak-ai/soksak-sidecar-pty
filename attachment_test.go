package main

import (
	"testing"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

func TestObserverOnlySessionNeverAppliesRendererBackpressure(t *testing.T) {
	value := &session{ring: newRing(ptycontract.HighWatermark), resume: make(chan struct{}, 1)}
	written := uint64(ptycontract.HighWatermark * 2)
	if value.shouldPause(written) {
		t.Fatal("a session without an attached renderer applied renderer backpressure")
	}
}

func TestAttachedRendererOwnsBackpressureUntilDetach(t *testing.T) {
	value := &session{ring: newRing(ptycontract.HighWatermark), resume: make(chan struct{}, 1)}
	if err := value.attachRenderer(); err != nil {
		t.Fatal(err)
	}
	written := uint64(ptycontract.HighWatermark)
	if !value.shouldPause(written) {
		t.Fatal("an attached renderer above the high watermark did not pause the reader")
	}
	if err := value.attachRenderer(); err == nil {
		t.Fatal("a second renderer attached to the same ACK coordinate")
	}
	value.detachRenderer()
	if value.shouldPause(written) {
		t.Fatal("a detached renderer kept the reader paused")
	}
}
