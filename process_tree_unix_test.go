//go:build !windows

package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnixProcessTreeReaderSkipsAProcessThatExitsDuringEventMaterialization(t *testing.T) {
	reader := unixProcessTreeReader{
		readProcessTable: func() ([]byte, error) {
			return []byte("100 1 root\n101 100 short-lived\n102 101 survivor\n"), nil
		},
		readProcessCWD: func(pid uint32) (string, error) {
			if pid == 101 {
				return "", ErrProcessExitedDuringSnapshot
			}
			if pid == 102 {
				return "/work/survivor", nil
			}
			return "", errors.New("unexpected pid")
		},
	}
	entries, err := reader.Descendants(100)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].PID != 102 || entries[0].ParentPID != 101 ||
		entries[0].CWD != "/work/survivor" {
		t.Fatalf("entries=%+v, want the surviving owned grandchild only", entries)
	}
}

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
