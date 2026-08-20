//go:build windows

package main

import "fmt"

// This target has no session reaper yet, and it fails by name rather than doing nothing.
//
// An empty function here made "the group was ended" and "this build cannot end a group" the same
// answer. Every caller then reported a clean shutdown while the shell and everything under it went
// on running, and the only way to find out was to look at the process list.
//
// A group here needs a job object: the unix group signal has no counterpart, and a pty on this
// target is ConPTY rather than a master fd. Neither is written.
func terminateProcessGroup(pid int) {
	panic(fmt.Sprintf("this build cannot end process group %d on this target: no job object is created and none is closed", pid))
}
