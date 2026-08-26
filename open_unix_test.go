//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The shell starts in the requested directory and sees the session variable it was given.
func TestAShellStartsInTheRequestedDirectoryWithSessionVariables(t *testing.T) {
	directory, err := os.MkdirTemp("/tmp", "ptycwd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	environment := applyEnvironment(
		[][2]string{{"PATH", "/usr/bin:/bin"}, {"TERM", "dumb"}},
		map[string]string{"SOKSAK_CALLER_PANE": "w1#pane-1"},
		nil,
	)
	process, err := startSessionProcess("/bin/sh", directory, environment, 80, 24)
	if err != nil {
		t.Fatalf("starting a shell in %s: %v", directory, err)
	}
	t.Cleanup(func() {
		_ = process.Terminate()
		_ = process.Close()
		_ = process.Wait()
	})
	if _, err := process.Write([]byte("printf 'CWD=%s PANE=%s\\n' \"$(pwd -P)\" \"$SOKSAK_CALLER_PANE\"\n")); err != nil {
		t.Fatal(err)
	}
	want := "CWD=" + resolved + " PANE=w1#pane-1"
	seen := make(chan string, 1)
	go func() {
		var out strings.Builder
		buffer := make([]byte, 4096)
		for !strings.Contains(out.String(), want) {
			count, err := process.Read(buffer)
			if count > 0 {
				out.Write(buffer[:count])
			}
			if err != nil {
				break
			}
		}
		seen <- out.String()
	}()
	select {
	case out := <-seen:
		if !strings.Contains(out, want) {
			t.Fatalf("the shell did not report %q. What arrived:\n%s", want, out)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("the shell did not report %q within the deadline", want)
	}
}
