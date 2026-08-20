//go:build !windows

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

// A real shell runs, echoes what it was sent, and is gone when the daemon ends.
//
// It builds the binary and runs it, because what is asserted is the thing a caller gets: an
// announcement it can act on, a shell that answers, and no orphan afterwards. A test that called
// the functions directly would pass with the announcement unflushed, with the sockets unbound, and
// with the process group unreaped — every one of which is a way this has actually failed.
func TestAShellRunsAndTheDaemonSaysWhenItIsReady(t *testing.T) {
	// A short home, because the socket derives from it and a unix socket path has a byte limit.
	// The default temporary directory on this platform is long enough on its own to exceed it.
	home, err := os.MkdirTemp("/tmp", "pty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	binary := filepath.Join(t.TempDir(), "soksak-sidecar-pty")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, out)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	daemon := exec.Command(binary, "-home", home, "-shell", shell)
	stdout, pipeErr := daemon.StdoutPipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	daemon.Stderr = os.Stderr
	if err := daemon.Start(); err != nil {
		t.Fatalf("starting the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Signal(os.Interrupt)
		_, _ = daemon.Process.Wait()
	})

	// The readiness rule: the first line, and nothing else.
	line, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("the daemon wrote no announcement: %v", err)
	}
	var announced ptycontract.Announcement
	if err := json.Unmarshal(line, &announced); err != nil {
		t.Fatalf("the first line is not an announcement: %q", line)
	}
	if announced.Version != ptycontract.ProtocolVersion {
		t.Fatalf("the announcement names protocol %d, this build speaks %d", announced.Version, ptycontract.ProtocolVersion)
	}
	if announced.Socket != ptycontract.ControlSocketPath(home) {
		t.Fatalf("the announcement names %q, the contract derives %q", announced.Socket, ptycontract.ControlSocketPath(home))
	}

	token, err := os.ReadFile(ptycontract.TokenPath(home))
	if err != nil {
		t.Fatalf("reading the token the daemon issued: %v", err)
	}

	control, err := net.Dial("unix", announced.Socket)
	if err != nil {
		t.Fatalf("connecting to the announced socket: %v", err)
	}
	defer func() { _ = control.Close() }()
	send := json.NewEncoder(control)
	read := bufio.NewReader(control)

	if err := send.Encode(ptycontract.NewHello(string(token), "test")); err != nil {
		t.Fatal(err)
	}
	if answer := next(t, read); !answer.OK {
		t.Fatalf("the greeting was refused: %s %s", answer.Code, answer.Message)
	}

	if err := send.Encode(ptycontract.CreateOrAttachRequest{
		Op: "open", PaneID: "pane-1", Cols: 80, Rows: 24, Shell: shell,
		Environment: [][2]string{{"PATH", os.Getenv("PATH")}},
		WindowLabel: "w1",
	}); err != nil {
		t.Fatal(err)
	}
	opened := next(t, read)
	if !opened.OK {
		t.Fatalf("opening a session was refused: %s %s", opened.Code, opened.Message)
	}
	var session struct {
		Session  uint64 `json:"session"`
		ShellPID int    `json:"shellPid"`
		Created  bool   `json:"created"`
	}
	if err := opened.DecodeData(&session); err != nil {
		t.Fatal(err)
	}
	if session.ShellPID <= 0 {
		t.Fatalf("the answer names no shell process: %+v", session)
	}

	// The stream socket, from the beginning of the ring.
	stream, err := net.Dial("unix", ptycontract.StreamSocketPath(home))
	if err != nil {
		t.Fatalf("connecting to the stream socket: %v", err)
	}
	defer func() { _ = stream.Close() }()
	from := uint64(0)
	hello := ptycontract.NewHello(string(token), "test-stream")
	hello.Session = &session.Session
	hello.FromSeq = &from
	if err := json.NewEncoder(stream).Encode(hello); err != nil {
		t.Fatal(err)
	}
	streamReader := bufio.NewReader(stream)
	ackLine, err := streamReader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("the stream socket answered no ack: %v", err)
	}
	var ack ptycontract.StreamAck
	if err := json.Unmarshal(ackLine, &ack); err != nil {
		t.Fatalf("the stream ack is not one: %q", ackLine)
	}
	if ack.Session != session.Session {
		t.Fatalf("the stream ack names session %d, this stream is session %d", ack.Session, session.Session)
	}

	marker := "SOKSAK-PTY-ROUNDTRIP-OK"
	if err := send.Encode(ptycontract.WriteRequest{
		Op: "write", Session: session.Session,
		DataBase64: base64.StdEncoding.EncodeToString([]byte("echo " + marker + "\n")),
	}); err != nil {
		t.Fatal(err)
	}
	if answer := next(t, read); !answer.OK {
		t.Fatalf("writing to the session was refused: %s %s", answer.Code, answer.Message)
	}

	if err := stream.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var seen strings.Builder
	buffer := make([]byte, 4096)
	for !strings.Contains(seen.String(), marker) {
		count, err := streamReader.Read(buffer)
		if count > 0 {
			seen.Write(buffer[:count])
		}
		if err != nil {
			t.Fatalf("the shell never echoed %q within the deadline. What arrived:\n%s", marker, seen.String())
		}
	}

	// Shutdown reaps. A shell still running after the daemon has gone is the failure this whole
	// process exists to make visible, and a pid that answers signal 0 is still there.
	shellPID := session.ShellPID
	_ = daemon.Process.Signal(os.Interrupt)
	_, _ = daemon.Process.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscallKill(shellPID); err != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("shell %d is still running after the daemon ended", shellPID)
}

func next(t *testing.T, reader *bufio.Reader) ptycontract.Reply {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading an answer: %v", err)
	}
	var answer ptycontract.Reply
	if err := json.Unmarshal(line, &answer); err != nil {
		t.Fatalf("an answer is not an envelope: %q", line)
	}
	return answer
}
