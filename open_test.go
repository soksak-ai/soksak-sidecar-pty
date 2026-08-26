package main

import (
	"encoding/json"
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

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
