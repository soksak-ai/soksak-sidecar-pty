package main

import "testing"

// The foreground program is the one the tty is giving the keyboard to.
//
// Picking the shell's first-listed child names whichever has the lowest pid, so a job put in the
// background before the foreground program started is what gets offered. With `npm run dev &` at
// pid 900 and vim at pid 950, the record said npm run dev — and S6-3's offer is "the same program,
// over the same files, in the same directory".
//
// The tty knows the answer: it holds one foreground process group, which is what a keystroke goes
// to. The children are matched against it rather than ordered by pid.
func TestTheForegroundProgramIsTheOneHoldingTheTerminal(t *testing.T) {
	const shell = 800
	entries := []processTreeEntry{
		{PID: 900, ParentPID: shell, GroupID: 900, Command: "npm run dev", CWD: "/work"},
		{PID: 950, ParentPID: shell, GroupID: 950, Command: "vim main.go", CWD: "/work/src"},
		{PID: 970, ParentPID: 950, GroupID: 950, Command: "gopls", CWD: "/work/src"},
	}

	command, cwd := foregroundOf(entries, shell, 950)
	if command != "vim main.go" || cwd != "/work/src" {
		t.Fatalf("named %q in %q, want the program the terminal is giving the keyboard to",
			command, cwd)
	}
}

// With no foreground group to go on, the shell's own child is the program.
//
// A tty that answers nothing is a platform that does not report it, not a shell with no program.
// The older answer is still better than none, and a deeper descendant is that program's — offering
// it would offer a build step rather than the build.
func TestWithNoForegroundGroupTheShellsChildIsTheProgram(t *testing.T) {
	const shell = 800
	entries := []processTreeEntry{
		{PID: 900, ParentPID: shell, GroupID: 900, Command: "vim", CWD: "/work"},
		{PID: 970, ParentPID: 900, GroupID: 900, Command: "gopls", CWD: "/work"},
	}
	command, cwd := foregroundOf(entries, shell, 0)
	if command != "vim" || cwd != "/work" {
		t.Fatalf("named %q in %q, want the shell's own child", command, cwd)
	}
}

// The shell itself in the foreground is no program.
func TestAShellInTheForegroundNamesNoProgram(t *testing.T) {
	const shell = 800
	entries := []processTreeEntry{
		{PID: 900, ParentPID: shell, GroupID: 900, Command: "npm run dev", CWD: "/work"},
	}
	command, _ := foregroundOf(entries, shell, shell)
	if command != "" {
		t.Fatalf("named %q while the shell held the terminal", command)
	}
}
