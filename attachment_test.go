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
	// A session has one renderer: the last to attach. The one it replaced cannot detach it.
	second, err := value.attachRenderer()
	if err != nil {
		t.Fatalf("the next renderer was refused: %v", err)
	}
	value.detachRenderer(first)
	if !value.shouldPause(written) {
		t.Fatal("the renderer that is there stopped owning backpressure when the one it replaced left")
	}
	value.detachRenderer(second)
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

func TestExplicitDetachAllowsReplacementWithoutStaleStreamClearingIt(t *testing.T) {
	value := &session{ring: newRing(ptycontract.HighWatermark), resume: make(chan struct{}, 1)}
	first, err := value.attachRenderer()
	if err != nil {
		t.Fatal(err)
	}
	if !value.detachActiveRenderer() {
		t.Fatal("active renderer was not detached")
	}
	second, err := value.attachRenderer()
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("replacement renderer reused the stale generation")
	}
	value.detachRenderer(first)
	if !value.rendererIsAttached() {
		t.Fatal("stale stream detach removed the replacement renderer")
	}
	value.detachRenderer(second)
	if value.rendererIsAttached() {
		t.Fatal("replacement renderer remained attached")
	}
}

func TestAcknowledgementCannotAdvancePastSessionOutput(t *testing.T) {
	value := &session{ring: newRing(ptycontract.HighWatermark), resume: make(chan struct{}, 1), written: 306}
	value.ack(612)
	acked, _ := value.ring.state()
	if acked != value.written {
		t.Fatalf("acknowledged=%d written=%d", acked, value.written)
	}
	if value.shouldPause(value.written) {
		t.Fatal("an acknowledgement ahead of output underflowed into a paused reader")
	}
}
