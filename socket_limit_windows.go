//go:build windows

package main

// This target has no unix socket path limit of its own in this build. The value is the same one
// used elsewhere so a home that works here works on every target rather than only on this one.
const socketPathLimit = 104
