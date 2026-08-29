//go:build !windows

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessTreeReaderReportsLiveDescendantAndCWD(t *testing.T) {
	command := exec.Command("/bin/sh", "-c", "sleep 3 & wait")
	command.Dir = t.TempDir()
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
	})
	time.Sleep(100 * time.Millisecond)
	entries, err := (unixProcessTreeReader{}).Descendants(uint32(command.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(command.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Command, "sleep 3") && entry.CWD == canonical {
			return
		}
	}
	t.Fatalf("descendants=%+v, want sleep child with cwd %q", entries, canonical)
}
