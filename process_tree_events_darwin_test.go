//go:build darwin

package main

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

func TestDarwinProcessObserveStreamsLiveDescendantLifecycleWithoutPolling(t *testing.T) {
	registry := newRegistry("/bin/sh")
	d := &daemon{
		registry: registry, identity: ptycontract.SidecarName,
		processTree: newProcessTreeReader(), processTreeEvents: newProcessTreeEventSource(),
	}
	d.startProcessMonitoring()
	server, client := net.Pipe()
	done := make(chan struct{})
	request := controlwire.Request{ID: "darwin-live", Command: ptycontract.CommandProcessObserve}
	go func() {
		d.processObserve(server, json.NewEncoder(server), request)
		close(done)
	}()
	t.Cleanup(func() {
		registry.shutdown()
		_ = client.Close()
		<-done
	})
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(client)
	initial := readProcessObservationResponse(t, reader)
	if initial.Revision != 0 || len(initial.Processes) != 0 {
		t.Fatalf("initial=%+v, want empty revision 0", initial)
	}

	value, err := registry.open(ptycontract.Open{
		PaneID: "pane-live", WindowLabel: "window-live", Shell: "/bin/sh",
		CWD: t.TempDir(), Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	revision := uint64(1)
	rootStarted := readProcessEvent(t, reader)
	if rootStarted.Revision != revision || rootStarted.Kind != ptycontract.ProcessStarted ||
		rootStarted.Process.ID != "pty-session-1" {
		t.Fatalf("root started=%+v", rootStarted)
	}

	if err := value.write([]byte("sleep 30\n")); err != nil {
		t.Fatal(err)
	}
	var child ptycontract.Process
	for child.ID == "" {
		event := readProcessEvent(t, reader)
		revision++
		if event.Revision != revision {
			t.Fatalf("event revision=%d after %d", event.Revision, revision-1)
		}
		if event.Process.ID != rootStarted.Process.ID && event.Process.State == "running" &&
			strings.Contains(event.Process.Command, "sleep 30") {
			child = event.Process
		}
	}
	if err := syscall.Kill(int(child.PID), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	for {
		event := readProcessEvent(t, reader)
		revision++
		if event.Revision != revision {
			t.Fatalf("event revision=%d after %d", event.Revision, revision-1)
		}
		if event.Process.ID == child.ID && event.Kind == ptycontract.ProcessEnded {
			if event.Process.EndedAtUnixMs == nil {
				t.Fatalf("child ended without timestamp: %+v", event)
			}
			break
		}
	}
	if err := registry.close(value.id); err != nil {
		t.Fatal(err)
	}
	rootEnded := readProcessEvent(t, reader)
	revision++
	if rootEnded.Revision != revision || rootEnded.Kind != ptycontract.ProcessEnded ||
		rootEnded.Process.ID != rootStarted.Process.ID {
		t.Fatalf("root ended=%+v after revision %d", rootEnded, revision-1)
	}
}

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
