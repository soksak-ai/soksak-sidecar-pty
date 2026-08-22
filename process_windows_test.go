//go:build windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsEnvironmentBlockIsSortedAndDoubleTerminated(t *testing.T) {
	block, err := windowsEnvironmentBlock([]string{"z=last", "A=first"})
	if err != nil {
		t.Fatal(err)
	}
	if len(block) < 2 || block[len(block)-1] != 0 || block[len(block)-2] != 0 {
		t.Fatalf("environment block is not double terminated: %v", block)
	}
	text := string(runesWithoutNUL(block))
	if !strings.HasPrefix(text, "A=first") {
		t.Fatalf("environment block is not case-insensitively sorted: %q", text)
	}
}

func TestEmptyWindowsEnvironmentBlockIsDoubleTerminated(t *testing.T) {
	block, err := windowsEnvironmentBlock(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(block) != 2 || block[0] != 0 || block[1] != 0 {
		t.Fatalf("empty environment block is not double terminated: %v", block)
	}
}

func TestConPTYRoundTripResizeAndTermination(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	process, err := startSessionProcess(
		filepath.Join(system, "cmd.exe"), "", os.Environ(), 80, 24,
	)
	if err != nil {
		t.Fatal(err)
	}
	p := process.(*conPTYProcess)
	defer p.Close()

	if p.PID() == 0 {
		t.Fatal("ConPTY shell has no process id")
	}
	if err := p.Resize(100, 40); err != nil {
		t.Fatalf("resize ConPTY: %v", err)
	}
	marker := []byte("SOKSAK-CONPTY-ROUNDTRIP")
	if _, err := p.Write([]byte("echo SOKSAK-CONPTY-ROUNDTRIP\r\n")); err != nil {
		t.Fatal(err)
	}
	read := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		var output bytes.Buffer
		for !bytes.Contains(output.Bytes(), marker) {
			count, err := p.Read(buffer)
			if count > 0 {
				output.Write(buffer[:count])
			}
			if err != nil {
				read <- err
				return
			}
		}
		read <- nil
	}()
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ConPTY shell did not echo the marker")
	}

	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	if err := windows.QueryInformationJobObject(
		p.job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)), nil,
	); err != nil {
		t.Fatal(err)
	}
	if limits.BasicLimitInformation.LimitFlags&windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE == 0 {
		t.Fatal("ConPTY shell job does not terminate descendants on close")
	}

	if err := p.Terminate(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestConPTYNaturalExitReleasesTheReader(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	process, err := startSessionProcess(
		filepath.Join(system, "cmd.exe"), "", os.Environ(), 80, 24,
	)
	if err != nil {
		t.Fatal(err)
	}
	p := process.(*conPTYProcess)
	defer p.Close()
	exitCommand := append([]byte("exit"), 13, 10)
	if _, err := p.Write(exitCommand); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- p.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ConPTY shell did not exit")
	}
	read := make(chan error, 1)
	go func() {
		_, err := p.Read(make([]byte, 1))
		read <- err
	}()
	select {
	case <-read:
	case <-time.After(2 * time.Second):
		t.Fatal("ConPTY output stayed open after the shell exited")
	}
}

func runesWithoutNUL(values []uint16) []rune {
	result := make([]rune, 0, len(values))
	for _, value := range values {
		if value != 0 {
			result = append(result, rune(value))
		}
	}
	return result
}
