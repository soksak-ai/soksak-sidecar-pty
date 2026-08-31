//go:build !windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func TestSessionTerminationStopsTheShellProcessGroup(t *testing.T) {
	process, err := startSessionProcess("/bin/sh", t.TempDir(), []string{"PATH=/usr/bin:/bin"}, 80, 24)
	if err != nil {
		t.Fatalf("starting shell: %v", err)
	}
	defer func() {
		_ = process.Terminate()
		_ = process.Close()
		_ = process.Wait()
	}()

	if _, err := process.Write([]byte("sleep 30 & child=$!; printf 'PTY_CHILD=%s\n' \"$child\"; wait\n")); err != nil {
		t.Fatalf("writing child command: %v", err)
	}
	childPID, err := readProcessMarker(process, "PTY_CHILD=")
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Terminate(); err != nil {
		t.Fatalf("terminating process group: %v", err)
	}
	_ = process.Close()
	if err := process.Wait(); err != nil && !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("waiting for shell: %v", err)
	}
	if err := syscall.Kill(childPID, 0); err == nil {
		t.Fatalf("child process %d remains after process-group termination", childPID)
	}
}

func readProcessMarker(process sessionProcess, marker string) (int, error) {
	deadline := time.Now().Add(5 * time.Second)
	var output strings.Builder
	buffer := make([]byte, 4096)
	for time.Now().Before(deadline) {
		count, err := process.Read(buffer)
		if count > 0 {
			output.Write(buffer[:count])
			text := output.String()
			for searchStart := 0; ; {
				found := strings.Index(text[searchStart:], marker)
				if found < 0 {
					break
				}
				index := searchStart + found
				line := text[index+len(marker):]
				if end := strings.IndexByte(line, '\n'); end >= 0 {
					if pid, parseErr := strconv.Atoi(strings.TrimSpace(line[:end])); parseErr == nil && pid > 0 {
						return pid, nil
					}
					searchStart = index + len(marker) + end + 1
					continue
				}
				break
			}
		}
		if err != nil {
			return 0, fmt.Errorf("reading process marker: %w; output=%q", err, output.String())
		}
	}
	return 0, fmt.Errorf("process marker %q not received; output=%q", marker, output.String())
}
