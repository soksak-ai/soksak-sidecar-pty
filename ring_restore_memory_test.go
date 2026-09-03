package main

import (
	"bytes"
	"runtime"
	"testing"
)

// A restored ring holds no more than it can serve.
//
// The retained tail is a reslice of whatever buffer it was given, and a reslice does not copy — so
// the whole buffer stays reachable through it for the life of the session. Measured before this was
// fixed: 7.39 MiB pinned per restored session to serve 1 MB, which is ~536 MiB at 64 sessions for
// 64 MB of reachable scrollback.
//
// The heap is measured rather than the slice, because a reslice from the front lowers cap while the
// allocation underneath it stays whole: the length was always right and the memory was not.
func TestARestoredRingDoesNotPinWhatItTrimmed(t *testing.T) {
	const capacity = 1 << 10
	const stored = 1 << 20
	const rings = 32

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	held := make([]*ring, 0, rings)
	for index := 0; index < rings; index++ {
		// A fresh buffer per ring, dropped here: what stays reachable is whatever restore kept a
		// reference into.
		body := bytes.Repeat([]byte("x"), stored)
		one := newRing(capacity)
		one.restore(body, uint64(len(body)))
		held = append(held, one)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	retained := after.HeapAlloc - before.HeapAlloc

	if kept := len(held[0].bytes); kept != capacity {
		t.Fatalf("kept %d bytes, want the capacity %d", kept, capacity)
	}
	// What the rings can serve is rings*capacity. Anything near rings*stored is the trimmed output
	// still being held.
	if retained > 8*rings*capacity {
		t.Fatalf("%d rings serving %d bytes hold %d bytes of heap: %d bytes per session are pinned",
			rings, rings*capacity, retained, retained/rings)
	}
	runtime.KeepAlive(held)
}

// The coordinates survive the trim.
//
// A sequence is a coordinate into one session's output and a consumer holds one across a restart.
// Keeping less of the tail must not move where the kept bytes claim to be.
func TestATrimmedRestoreKeepsItsCoordinates(t *testing.T) {
	const capacity = 1 << 10
	held := newRing(capacity)
	stored := bytes.Repeat([]byte("x"), 64<<10)
	const reached = 1 << 20

	held.restore(stored, reached)

	held.mu.Lock()
	floor, live := held.floor, held.floor+uint64(len(held.bytes))
	held.mu.Unlock()

	if live != reached {
		t.Fatalf("the live edge is %d, the session reached %d", live, reached)
	}
	if floor != reached-capacity {
		t.Fatalf("the floor is %d, want %d — the kept bytes moved", floor, reached-capacity)
	}
}
