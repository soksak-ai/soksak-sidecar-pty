//go:build !windows

package main

import "syscall"

// terminateProcessGroup ends the shell and everything it started.
//
// The negative pid is the group. Signalling the shell alone leaves a build, a server or an editor
// it started running with no terminal, no parent watching, and no way for anyone to find it again.
func terminateProcessGroup(pid int) {
	if pid > 0 {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	}
}
