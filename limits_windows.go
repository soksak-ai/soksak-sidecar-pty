//go:build windows

package main

// raiseOpenFileLimit does nothing here. Handles are not bounded by a per-process soft limit a
// process raises for itself on this platform.
func raiseOpenFileLimit() {}
