//go:build !darwin

package main

func newProcessTreeEventSource() processTreeEventSource {
	return unsupportedProcessTreeEventSource{}
}
