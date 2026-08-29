//go:build windows

package main

// Windows process-tree membership is owned by the ConPTY job object. The job enumeration wire is
// a separate implementation gate; until it is implemented, the daemon reports its shell only.
type windowsProcessTreeReader struct{}

func newProcessTreeReader() processTreeReader { return windowsProcessTreeReader{} }

func (windowsProcessTreeReader) Descendants(uint32) ([]processTreeEntry, error) {
	return []processTreeEntry{}, nil
}
