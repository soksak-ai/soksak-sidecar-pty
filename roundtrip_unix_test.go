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

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
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
	runtimeRoot, err := os.MkdirTemp("/tmp", "ptr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })
	binary := filepath.Join(t.TempDir(), "soksak-sidecar-pty")
	build := exec.Command("go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the daemon: %v\n%s", err, out)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	daemon := exec.Command(binary, "-home", home, "-runtime", runtimeRoot, "-shell", shell)
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
	var announced controlwire.Announcement
	if err := json.Unmarshal(line, &announced); err != nil {
		t.Fatalf("the first line is not an announcement: %q", line)
	}
	if announced.Protocol == nil || *announced.Protocol != controlwire.Protocol {
		t.Fatalf("the announcement names protocol %v, this build speaks %d", announced.Protocol, controlwire.Protocol)
	}
	if announced.Socket == nil || *announced.Socket != ptycontract.ControlSocketPath(runtimeRoot, false) {
		t.Fatalf("the announcement names %v, the contract derives %q", announced.Socket, ptycontract.ControlSocketPath(runtimeRoot, false))
	}

	token, err := os.ReadFile(ptycontract.TokenPath(runtimeRoot))
	if err != nil {
		t.Fatalf("reading the token the daemon issued: %v", err)
	}

	control, err := net.Dial("unix", *announced.Socket)
	if err != nil {
		t.Fatalf("connecting to the announced socket: %v", err)
	}
	defer func() { _ = control.Close() }()
	send := json.NewEncoder(control)
	read := bufio.NewReader(control)

	if err := send.Encode(request("hello", controlwire.HelloCommand, map[string]any{
		"protocol": controlwire.Protocol, "token": string(token),
	})); err != nil {
		t.Fatal(err)
	}
	if answer := next(t, read); !answer.Ok {
		t.Fatalf("the greeting was refused: %s", answer.Error)
	}

	if err := send.Encode(request("open", ptycontract.CommandOpen, map[string]any{
		"request": ptycontract.Open{
			PaneID: "pane-1", Cols: 80, Rows: 24, Shell: shell,
			Environment: [][2]string{{"PATH", os.Getenv("PATH")}},
			WindowLabel: "w1",
		},
	})); err != nil {
		t.Fatal(err)
	}
	opened := next(t, read)
	if !opened.Ok {
		t.Fatalf("opening a session was refused: %s", opened.Error)
	}
	var session ptycontract.Opened
	if err := decodeAnswer(opened, &session); err != nil {
		t.Fatal(err)
	}
	if session.ShellPID <= 0 {
		t.Fatalf("the answer names no shell process: %+v", session)
	}

	// A second connection to the one socket, greeted like any other, then turned into a stream.
	stream, err := net.Dial("unix", *announced.Socket)
	if err != nil {
		t.Fatalf("connecting for a stream: %v", err)
	}
	defer func() { _ = stream.Close() }()
	streamSend := json.NewEncoder(stream)
	streamReader := bufio.NewReader(stream)
	if err := streamSend.Encode(request("hello", controlwire.HelloCommand, map[string]any{
		"protocol": controlwire.Protocol, "token": string(token),
	})); err != nil {
		t.Fatal(err)
	}
	if answer := next(t, streamReader); !answer.Ok {
		t.Fatalf("the stream connection's greeting was refused: %s", answer.Error)
	}

	from := uint64(0)
	if err := streamSend.Encode(request("attach", ptycontract.CommandAttach, map[string]any{
		"request": ptycontract.Attach{Session: session.Session, FromSeq: &from},
	})); err != nil {
		t.Fatal(err)
	}
	ackLine, err := streamReader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("the attach answered nothing: %v", err)
	}
	var ackResponse controlwire.Response
	if err := json.Unmarshal(ackLine, &ackResponse); err != nil {
		t.Fatalf("the stream answer is not a response: %q", ackLine)
	}
	if !ackResponse.Ok {
		t.Fatalf("attaching was refused: %s", ackResponse.Error)
	}
	var attached ptycontract.Attached
	if err := decodeAnswer(ackResponse, &attached); err != nil {
		t.Fatal(err)
	}
	if attached.Session != session.Session {
		t.Fatalf("the stream answer names session %d, this stream is session %d", attached.Session, session.Session)
	}

	marker := "SOKSAK-PTY-ROUNDTRIP-OK"
	if err := send.Encode(request("write", ptycontract.CommandWrite, map[string]any{
		"request": ptycontract.Write{
			Session:    session.Session,
			DataBase64: base64.StdEncoding.EncodeToString([]byte("echo " + marker + "\n")),
		},
	})); err != nil {
		t.Fatal(err)
	}
	if answer := next(t, read); !answer.Ok {
		t.Fatalf("writing to the session was refused: %s", answer.Error)
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
	// Wait is the event. The daemon reaps its sessions before it returns, so by the time its own
	// process has been waited for, a shell that is still there is one it failed to end — no clock is
	// involved and no moment has to be guessed.
	shellPID := session.ShellPID
	_ = daemon.Process.Signal(os.Interrupt)
	if _, err := daemon.Process.Wait(); err != nil {
		t.Fatalf("waiting for the daemon to end: %v", err)
	}
	if err := syscallKill(shellPID); err == nil {
		t.Fatalf("shell %d is still running after the daemon ended", shellPID)
	}
}

func next(t *testing.T, reader *bufio.Reader) controlwire.Response {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("reading an answer: %v", err)
	}
	var answer controlwire.Response
	if err := json.Unmarshal(line, &answer); err != nil {
		t.Fatalf("an answer is not an envelope: %q", line)
	}
	return answer
}

// request builds one envelope. Arguments are encoded here rather than by the caller so a shape
// mismatch fails at this line rather than as a refusal the caller has to interpret.
func request(id, command string, args map[string]any) controlwire.Request {
	encoded := make(map[string]json.RawMessage, len(args))
	for name, value := range args {
		raw, err := json.Marshal(value)
		if err != nil {
			panic(err)
		}
		encoded[name] = raw
	}
	return controlwire.Request{ID: id, Command: command, Args: encoded}
}

// decodeAnswer reads a result out of the answer shape a generic caller parses.
func decodeAnswer(response controlwire.Response, target any) error {
	raw, err := json.Marshal(response.Result)
	if err != nil {
		return err
	}
	var answer struct {
		Code string          `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return err
	}
	return json.Unmarshal(answer.Data, target)
}
