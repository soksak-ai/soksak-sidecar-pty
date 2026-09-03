package main

import (
	"strings"
	"testing"
)

// The stored output ends where the session reached, after a crash as after a stop.
//
// A sequence is a coordinate into one session's output and a consumer holds one across a restart.
// If the restored ring's live edge is behind where the session actually got to, the bytes between
// are relabelled: a consumer inside the window is told ModeResumed and served bytes carrying
// coordinates that belong to earlier output. It draws from a place it never asked for and the mode
// says the resume was clean.
//
// Measured before this was fixed: 145 chunks of 64 KiB, two rotations, then ~1 MiB into the current
// segment with no stop write — the ring came back 1,048,576 bytes short, and that much terminal
// output was silently skipped.
func TestStoredOutputEndsWhereTheSessionReached(t *testing.T) {
	held, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 5
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1}); err != nil {
		t.Fatalf("creating: %v", err)
	}

	chunk := []byte(strings.Repeat("x", 64<<10))
	reached := uint64(0)
	// Past two rotations and well into the third segment, with no stop write: this is the crash
	// shape, where the record holds whatever the last rotation left.
	for reached < uint64(2*outputSegmentBound+(1<<20)) {
		reached += uint64(len(chunk))
		if err := held.append(id, chunk, reached); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}

	_, through, err := held.output(id, 2*outputSegmentBound)
	if err != nil {
		t.Fatalf("reading the output: %v", err)
	}
	if through != reached {
		t.Fatalf("the stored output ends at %d, the session reached %d: %d bytes are relabelled",
			through, reached, reached-through)
	}
}

// A stop does not move the coordinate the rotations recorded.
func TestAStopKeepsTheCoordinateTheOutputEndsAt(t *testing.T) {
	held, err := newStore(t.TempDir())
	if err != nil {
		t.Fatalf("preparing the store: %v", err)
	}
	const id = 6
	if err := held.create(sessionRecord{Session: id, StartedAtUnixMs: 1}); err != nil {
		t.Fatalf("creating: %v", err)
	}
	chunk := []byte(strings.Repeat("y", 64<<10))
	reached := uint64(0)
	for reached < uint64(outputSegmentBound+(1<<19)) {
		reached += uint64(len(chunk))
		if err := held.append(id, chunk, reached); err != nil {
			t.Fatalf("appending: %v", err)
		}
	}
	if err := held.markEnded(id, 2, nil, reached); err != nil {
		t.Fatalf("stopping: %v", err)
	}

	_, through, err := held.output(id, 2*outputSegmentBound)
	if err != nil {
		t.Fatalf("reading the output: %v", err)
	}
	if through != reached {
		t.Fatalf("after a stop the output ends at %d, the session reached %d", through, reached)
	}
}
