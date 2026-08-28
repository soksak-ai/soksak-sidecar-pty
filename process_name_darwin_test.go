//go:build darwin && cgo

package main

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestDarwinDaemonPublishesTheAcceptedProcessLabel(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "pty-name-home")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	runtimeRoot, err := os.MkdirTemp("/tmp", "pty-name-runtime")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeRoot) })

	binary := filepath.Join(t.TempDir(), "soksak-sidecar-pty")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building PTY daemon: %v\n%s", err, output)
	}
	daemon := exec.Command(binary, "-home", home, "-runtime", runtimeRoot, "-shell", "/bin/sh")
	daemon.Env = append(os.Environ(), "SOKSAK_PROCESS_LABEL=soksakv3")
	stdout, err := daemon.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = daemon.Process.Signal(os.Interrupt)
		_, _ = daemon.Process.Wait()
	})
	if _, err := bufio.NewReader(stdout).ReadBytes('\n'); err != nil {
		t.Fatalf("PTY daemon did not announce readiness: %v", err)
	}

	name, err := currentDarwinProcessName(daemon.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if name != "soksakv3-pty" {
		t.Fatalf("Darwin process name = %q, want project and Sidecar role %q", name, "soksakv3-pty")
	}
}
