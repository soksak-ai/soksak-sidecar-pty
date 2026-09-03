//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"

	"golang.org/x/sys/unix"
)

type unixSessionProcess struct {
	master *os.File
	cmd    *exec.Cmd
}

func startSessionProcess(shell, cwd string, environment []string, cols, rows uint16) (sessionProcess, error) {
	command := exec.Command(shell, "-l")
	command.Env = environment
	command.Dir = cwd
	master, err := pty.StartWithSize(command, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return &unixSessionProcess{master: master, cmd: command}, nil
}

func (p *unixSessionProcess) Read(buffer []byte) (int, error) { return p.master.Read(buffer) }
func (p *unixSessionProcess) Write(data []byte) (int, error)  { return p.master.Write(data) }
func (p *unixSessionProcess) Resize(cols, rows uint16) error {
	return pty.Setsize(p.master, &pty.Winsize{Cols: cols, Rows: rows})
}
func (p *unixSessionProcess) PID() uint32 { return uint32(p.cmd.Process.Pid) }
func (p *unixSessionProcess) Terminate() error {
	if p.cmd.Process.Pid > 0 {
		return syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	return nil
}
func (p *unixSessionProcess) Wait() error {
	_, err := p.cmd.Process.Wait()
	return err
}
func (p *unixSessionProcess) Close() error { return p.master.Close() }

// terminateProcessGroup ends the shell and everything it started.
//
// The negative pid is the group. Signalling the shell alone leaves a build, a server or an editor
// it started running with no terminal, no parent watching, and no way for anyone to find it again.

// ForegroundGroup asks the tty which process group holds it.
//
// The master side of the pty answers for the session on the other end, so this is the group a
// keystroke typed into this session would reach.
func (p *unixSessionProcess) ForegroundGroup() uint32 {
	if p.master == nil {
		return 0
	}
	group, err := unix.IoctlGetInt(int(p.master.Fd()), unix.TIOCGPGRP)
	if err != nil || group <= 0 {
		return 0
	}
	return uint32(group)
}
