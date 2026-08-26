package main

import (
	"testing"
	"time"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func abandonRequest(paneID string) ptycontract.Open {
	return ptycontract.Open{PaneID: paneID, Cols: 80, Rows: 24, Shell: "/bin/sh"}
}

// A session nothing is attached to is a session no pane is showing. It is kept for a while so a
// view that unmounts can mount again and reattach; past that it is what a run that went away left
// behind, and it ends rather than holding a shell nobody can reach.
func TestASessionNothingReattachesToEnds(t *testing.T) {
	registry := newRegistry("/bin/sh")
	t.Cleanup(func() { registry.shutdown() })
	clock := time.Unix(1_700_000_000, 0)
	registry.now = func() time.Time { return clock }
	registry.abandonAfter = 2 * time.Minute

	held, err := registry.open(abandonRequest("pane-held"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.attachRenderer(); err != nil {
		t.Fatal(err)
	}
	gone, err := registry.open(abandonRequest("pane-gone"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gone.attachRenderer(); err != nil {
		t.Fatal(err)
	}
	gone.detachActiveRenderer()

	clock = clock.Add(119 * time.Second)
	if ended := registry.endAbandoned(); ended != 0 {
		t.Fatalf("a session inside the window ended: %d", ended)
	}
	clock = clock.Add(2 * time.Second)
	if ended := registry.endAbandoned(); ended != 1 {
		t.Fatalf("ended=%d, want 1", ended)
	}
	if _, err := registry.get(gone.id); err == nil {
		t.Fatal("the abandoned session is still listed")
	}
	if _, err := registry.get(held.id); err != nil {
		t.Fatalf("the attached session was ended: %v", err)
	}
}

// A session that reattaches inside the window is not abandoned, and the window starts over when it
// detaches again.
func TestReattachingClearsTheAbandonWindow(t *testing.T) {
	registry := newRegistry("/bin/sh")
	t.Cleanup(func() { registry.shutdown() })
	clock := time.Unix(1_700_000_000, 0)
	registry.now = func() time.Time { return clock }
	registry.abandonAfter = time.Minute

	value, err := registry.open(abandonRequest("pane-back"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.attachRenderer(); err != nil {
		t.Fatal(err)
	}
	value.detachActiveRenderer()
	clock = clock.Add(59 * time.Second)
	if _, err := value.attachRenderer(); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(2 * time.Minute)
	if ended := registry.endAbandoned(); ended != 0 {
		t.Fatalf("an attached session ended: %d", ended)
	}
	value.detachActiveRenderer()
	clock = clock.Add(2 * time.Minute)
	if ended := registry.endAbandoned(); ended != 1 {
		t.Fatalf("ended=%d, want 1", ended)
	}
}
