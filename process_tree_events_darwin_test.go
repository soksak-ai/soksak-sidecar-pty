//go:build darwin

package main

import (
	"bufio"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The child is created only after the root pid has been registered with kqueue. Pipes provide the
// test synchronization; timeout channels are failure bounds, not the observation mechanism.
func TestDarwinProcessTreeEventsSignalForkAndExitWithoutPolling(t *testing.T) {
	command := exec.Command("/bin/sh")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	changes := make(chan struct{}, 8)
	source := newProcessTreeEventSource()
	if err := source.Supported(); err != nil {
		t.Fatal(err)
	}
	watch, err := source.Observe(uint32(command.Process.Pid), func() { changes <- struct{}{} })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watch.Close() })

	if _, err := io.WriteString(stdin, "sleep 30 & printf '%s\\n' $!\n"); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	childPID, err := strconv.ParseUint(strings.TrimSpace(line), 10, 32)
	if err != nil {
		t.Fatalf("child pid line=%q: %v", line, err)
	}
	waitForNativeProcessChange(t, changes, "fork")

	entries, err := (unixProcessTreeReader{}).Descendants(uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	tracked, err := watch.Sync(entries)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range tracked {
		if entry.PID == uint32(childPID) {
			found = true
		}
	}
	if !found {
		t.Fatalf("tracked descendants=%+v, want pid %d", tracked, childPID)
	}

	if err := syscall.Kill(int(childPID), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitForNativeProcessChange(t, changes, "exit")
}

func waitForNativeProcessChange(t *testing.T, changes <-chan struct{}, kind string) {
	t.Helper()
	select {
	case <-changes:
	case <-time.After(2 * time.Second):
		t.Fatalf("no kqueue %s notification", kind)
	}
}
