//go:build !windows

package main

// socketPathLimit is how many bytes of path a unix socket address holds on this platform.
//
// It is 104 on the BSD-derived platforms and 108 on Linux; the smaller one is used everywhere,
// because a daemon that accepted a path here and was refused it on another target would move the
// failure to whichever machine has the longer home.
const socketPathLimit = 104
