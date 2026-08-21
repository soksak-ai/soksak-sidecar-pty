package main

import (
	"strings"
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func TestLeasePreservesItsSourceCursorUntilConsumed(t *testing.T) {
	ring := newRing(4)
	ring.write([]byte("abcd"))
	if err := ring.lease("lease", 2); err != nil {
		t.Fatal(err)
	}
	ring.write([]byte("efgh"))
	if floor, _, _ := ring.snapshot(); floor != 2 {
		t.Fatalf("lease cursor was evicted: floor=%d", floor)
	}
	at, err := ring.consumeLease("lease")
	if err != nil || at != 2 {
		t.Fatalf("consume = %d, %v", at, err)
	}
	if _, err := ring.consumeLease("lease"); err == nil {
		t.Fatal("a consumed lease was accepted twice")
	}
}

func TestLeaseBreaksWhenItsBoundedRetentionBudgetIsExceeded(t *testing.T) {
	ring := newRing(4)
	ring.write([]byte("abcd"))
	if err := ring.lease("lease", 4); err != nil {
		t.Fatal(err)
	}
	ring.write(make([]byte, ptycontract.LeaseBufferBytes+1))
	_, err := ring.consumeLease("lease")
	if err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("over-budget lease error = %v", err)
	}
}
