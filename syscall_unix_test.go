//go:build !windows

package main

import "syscall"

// syscallKill asks whether a pid is still there without signalling it. An error means it is gone.
func syscallKill(pid int) error { return syscall.Kill(pid, 0) }
