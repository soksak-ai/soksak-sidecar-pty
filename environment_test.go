package main

import (
	"slices"
	"strings"
	"testing"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func count(entries []string, prefix string) int {
	total := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry, prefix) {
			total++
		}
	}
	return total
}

// Session variables ride on the inherited environment and replace a stale value under their name.
func TestSessionVariablesRideOnTheInheritedEnvironment(t *testing.T) {
	t.Setenv("SOKSAK_CALLER_PANE", "stale")
	t.Setenv("SOKSAK_PTY_TEST_INHERITED", "kept")
	result := sessionEnvironment(
		ptycontract.SessionVariables(map[string]string{"SOKSAK_CALLER_PANE": "w1#pane-1"}), nil,
	)
	if !slices.Contains(result, "SOKSAK_CALLER_PANE=w1#pane-1") {
		t.Fatalf("session variable is absent: %v", result)
	}
	if slices.Contains(result, "SOKSAK_CALLER_PANE=stale") {
		t.Fatalf("stale inherited value survived: %v", result)
	}
	if count(result, "SOKSAK_CALLER_PANE=") != 1 {
		t.Fatalf("the variable appears more than once: %v", result)
	}
	if !slices.Contains(result, "SOKSAK_PTY_TEST_INHERITED=kept") {
		t.Fatalf("the inherited environment was not kept: %v", result)
	}
}

// A replacement environment is whole, session variables still land on it, and defaults fill only
// what nothing named.
func TestReplacementEnvironmentIsWholeAndStillTakesSessionVariables(t *testing.T) {
	result := applyEnvironment(
		[][2]string{{"PATH", "/bin"}, {"SOKSAK_CALLER_PANE", "old"}},
		map[string]string{"SOKSAK_CALLER_PANE": "p"},
		[]string{"LANG"},
	)
	if result[0] != "PATH=/bin" || result[1] != "SOKSAK_CALLER_PANE=p" {
		t.Fatalf("order or values changed: %v", result)
	}
	if count(result, "SOKSAK_CALLER_PANE=") != 1 {
		t.Fatalf("the replaced value survived: %v", result)
	}
	if !slices.Contains(result, "TERM=xterm-256color") {
		t.Fatalf("tty defaults were not applied: %v", result)
	}
	if count(result, "LANG=") != 0 {
		t.Fatalf("a dropped default was applied: %v", result)
	}
	if count(result, "PATH=") != 1 {
		t.Fatalf("PATH is not exactly the replacement: %v", result)
	}
}
