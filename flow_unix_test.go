//go:build !windows

package main

import (
	"testing"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

// A paused reader is readable, and that is the whole reason this exists.
//
// Measured 2026-08-20 on a running build: a session had stopped producing output while the program
// in it was alive, in the foreground, on its own tty, with the process tree correct. Every reading
// available said the session was healthy, and the one reading that separates "the reader is holding
// off" from "the program is hung" did not exist — so the cause stayed unmeasured and the only
// honest answer was that it could not be told.
//
// The ring is exercised directly here. What is asserted is that the state a caller reads moves when
// flow control moves, in both directions, so a status showing paused is a fact and not an inference
// somebody has to make from two numbers.
func TestAPausedReaderSaysSoAndSaysWhenItResumes(t *testing.T) {
	r := newRing(4 * ptycontract.HighWatermark)

	// Nothing written, nothing acked: not paused, and nothing retained.
	if acked, retained := r.state(); acked != 0 || retained != 0 {
		t.Fatalf("a fresh ring reports acked=%d retained=%d", acked, retained)
	}
	if r.paused(0) {
		t.Fatal("a ring with nothing outstanding reports the reader paused")
	}

	// Past the high watermark with no ack, the reader holds off.
	written := r.write(make([]byte, ptycontract.HighWatermark))
	if !r.paused(written) {
		t.Fatalf("%d bytes are outstanding against a %d high watermark and the reader is not paused",
			written, ptycontract.HighWatermark)
	}
	if _, retained := r.state(); retained != int(written) {
		t.Fatalf("the ring holds %d of %d written", retained, written)
	}

	// An ack that does not reach the low mark does not release it. The gap between the marks is
	// what keeps acks in flight from restarting the reader on every one of them.
	r.ack(written - ptycontract.LowWatermark - 1)
	if r.resumed(written) {
		t.Fatal("the reader resumed above the low watermark, so the window slack does nothing")
	}

	// At the low mark it may run again, and what a caller reads moves with it.
	r.ack(written - ptycontract.LowWatermark)
	if !r.resumed(written) {
		t.Fatal("the reader did not resume at the low watermark")
	}
	acked, _ := r.state()
	if acked != written-ptycontract.LowWatermark {
		t.Fatalf("the ack a caller reads is %d and %d was acked", acked, written-ptycontract.LowWatermark)
	}
}

// Written and acked are both reported, because their difference alone hides which fault it is.
//
// A client that never acked at all and one that acked and then stopped produce the same difference
// at the moment the reader pauses, and they are different faults: the first never wired its acks,
// the second stopped taking output. A status carrying only the gap makes them one.
func TestBothSidesOfTheGapAreReadable(t *testing.T) {
	r := newRing(4 * ptycontract.HighWatermark)
	written := r.write(make([]byte, 1024))
	r.ack(400)

	acked, retained := r.state()
	if acked != 400 {
		t.Fatalf("acked reads %d", acked)
	}
	if written != 1024 || retained != 1024 {
		t.Fatalf("written reads %d and the ring holds %d", written, retained)
	}
}
