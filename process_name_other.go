//go:build !darwin

package main

// No operating-system process-name projection is declared for non-Darwin PTY builds. The validated
// process label remains available through the control announcement, greeting, and status surfaces.
func applyPlatformProcessName(string) error { return nil }
