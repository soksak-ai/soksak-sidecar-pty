//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
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

	if _, err := process.Write([]byte("printf 'PTY_SHELL=%s\n' \"$$\"; sleep 30 >/dev/null 2>&1 & wait\n")); err != nil {
		t.Fatalf("writing child command: %v", err)
	}
	shellPID, err := readProcessMarker(process, "PTY_SHELL=")
	if err != nil {
		t.Fatal(err)
	}
	if descendants := processDescendantPIDs(shellPID); len(descendants) < 1 {
		t.Fatalf("shell process %d did not retain a child: %v", shellPID, descendants)
	}
	if err := process.Terminate(); err != nil {
		t.Fatalf("terminating process group: %v", err)
	}
	_ = process.Close()
	if err := process.Wait(); err != nil && !strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("waiting for shell: %v", err)
	}
	if err := syscall.Kill(shellPID, 0); err == nil {
		t.Fatalf("shell process %d remains after process-group termination", shellPID)
	}
	if descendants := processDescendantPIDs(shellPID); len(descendants) != 0 {
		t.Fatalf("shell process %d retains descendants after termination: %v", shellPID, descendants)
	}
}

func processDescendantPIDs(root int) []int {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	parents := map[int]bool{root: true}
	var descendants []int
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		parent, parentErr := strconv.Atoi(fields[1])
		if pidErr == nil && parentErr == nil && parents[parent] {
			parents[pid] = true
			descendants = append(descendants, pid)
		}
	}
	return descendants
}

func processGroupMembers(groupLeader int) []int {
	output, err := exec.Command("ps", "-o", "pid=", "-g", strconv.Itoa(groupLeader)).Output()
	if err != nil {
		return nil
	}
	var members []int
	for _, line := range strings.Split(string(output), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err == nil && pid > 0 {
			members = append(members, pid)
		}
	}
	return members
}

func readProcessMarker(process sessionProcess, marker string) (int, error) {
	type result struct {
		pid    int
		output string
		err    error
	}
	results := make(chan result, 1)
	go func() {
		var output strings.Builder
		buffer := make([]byte, 4096)
		for {
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
							results <- result{pid: pid, output: output.String()}
							return
						}
						searchStart = index + len(marker) + end + 1
						continue
					}
					break
				}
			}
			if err != nil {
				results <- result{output: output.String(), err: fmt.Errorf("reading process marker: %w", err)}
				return
			}
		}
	}()
	select {
	case found := <-results:
		if found.err != nil {
			return 0, fmt.Errorf("%w; output=%q", found.err, found.output)
		}
		return found.pid, nil
	case <-time.After(5 * time.Second):
		return 0, fmt.Errorf("process marker %q not received within deadline", marker)
	}
}
