package main

import (
	"encoding/json"
	"testing"
	"time"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func TestProcessInventoryReportsOnlyExplicitlyOwnedPTYSessions(t *testing.T) {
	window, pane := "window-1", "pane-1"
	started := time.UnixMilli(1_700_000_000_000)
	registry := newRegistry("/bin/sh")
	registry.now = func() time.Time { return started }
	value := &session{
		id: 7, paneID: pane, windowLabel: window, command: "/bin/zsh -l", startedAt: started,
		process: &resizeProcess{}, ring: newRing(16), observers: map[*observer]struct{}{},
		observerTokens: map[string]*observer{}, displaying: map[*observer]struct{}{}, now: func() time.Time { return started },
	}
	registry.sessions[value.id] = value
	registry.processRevision = 3
	d := &daemon{registry: registry, identity: "soksak-sidecar-pty"}
	code, raw, err := d.processInventory(nil)
	if err != nil || code != "" {
		t.Fatalf("code=%q err=%v", code, err)
	}
	inventory, ok := raw.(ptycontract.ProcessInventory)
	if !ok || inventory.Revision != 3 || len(inventory.Processes) != 1 {
		t.Fatalf("inventory=%#v", raw)
	}
	process := inventory.Processes[0]
	if process.Owner != d.identity || process.Window == nil || *process.Window != window || process.Pane == nil || *process.Pane != pane || process.State != "running" {
		t.Fatalf("process=%+v", process)
	}
}

// A request the contract refuses never reaches the registry, so no shell is started for it.
func TestOpenRefusesInvalidRequestsBeforeStartingAShell(t *testing.T) {
	d := &daemon{registry: newRegistry("/bin/sh")}
	refused := map[string]ptycontract.Open{
		"relative cwd": {PaneID: "pane", Cols: 80, Rows: 24, CWD: "relative/dir"},
		"foreign variable": {
			PaneID: "pane", Cols: 80, Rows: 24,
			Environment: ptycontract.SessionVariables(map[string]string{"PATH": "/bin"}),
		},
	}
	for name, request := range refused {
		raw, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		code, _, err := d.open(map[string]json.RawMessage{"request": raw})
		if err == nil || code != "ARGUMENT" {
			t.Errorf("%s: code=%q err=%v, want ARGUMENT", name, code, err)
		}
	}
	if sessions := d.registry.list(); len(sessions) != 0 {
		t.Fatalf("a refused open left sessions behind: %+v", sessions)
	}
}
