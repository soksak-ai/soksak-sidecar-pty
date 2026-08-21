package main

import (
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
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
	first, err := value.attachRenderer()
	if err != nil {
		t.Fatal(err)
	}
	written := uint64(ptycontract.HighWatermark)
	if !value.shouldPause(written) {
		t.Fatal("an attached renderer above the high watermark did not pause the reader")
	}
	if _, err := value.attachRenderer(); err == nil {
		t.Fatal("a second renderer attached to the same ACK coordinate")
	}
	value.detachRenderer(first)
	if value.shouldPause(written) {
		t.Fatal("a detached renderer kept the reader paused")
	}
}

func TestSnapshotLeaseReplacesThePreviousRendererGeneration(t *testing.T) {
	value := &session{ring: newRing(ptycontract.HighWatermark), resume: make(chan struct{}, 1)}
	previous, err := value.attachRenderer()
	if err != nil {
		t.Fatal(err)
	}
	replacement := value.replaceRenderer()
	if replacement == previous {
		t.Fatal("snapshot lease reused the previous renderer generation")
	}
	value.detachRenderer(previous)
	if !value.rendererIsAttached() {
		t.Fatal("previous renderer detach removed the replacement")
	}
	value.detachRenderer(replacement)
	if value.rendererIsAttached() {
		t.Fatal("replacement renderer remained attached after its detach")
	}
}
