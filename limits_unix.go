//go:build !windows

package main

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// raiseOpenFileLimit lifts the soft limit on open files to the hard one.
//
// Every session holds a pty master and a segment file, and every attached observer holds a socket
// on top. A daemon started by a launcher takes whatever soft limit the launcher had — 256 on macOS
// under launchd — which is about 120 sessions.
//
// It matters more than the number suggests because of how it fails. The limit is reached inside
// opening a segment, which the feed reports as bytes lost from the record; the session keeps
// running and silently stops being stored, one line of stderr per chunk, and the next start does
// not find it. Raising the soft limit costs nothing — the hard limit is the real ceiling and this
// does not move it.
func raiseOpenFileLimit() {
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_NOFILE, &limit); err != nil {
		fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: the open file limit could not be read: %v\n", err)
		return
	}
	if limit.Cur >= limit.Max {
		return
	}
	raised := limit
	raised.Cur = limit.Max
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &raised); err != nil {
		fmt.Fprintf(os.Stderr,
			"soksak-sidecar-pty: the open file limit stays at %d, which bounds how many sessions this daemon can store: %v\n",
			limit.Cur, err)
	}
}
