//go:build !darwin

package main

import (
	"errors"
	"testing"
)

func TestProcessTreeEventSourceNamesUnsupportedPlatforms(t *testing.T) {
	source := newProcessTreeEventSource()
	if err := source.Supported(); !errors.Is(err, ErrProcessObservationUnsupported) {
		t.Fatalf("supported error=%v, want ErrProcessObservationUnsupported", err)
	}
	watch, err := source.Observe(1, func() {})
	if watch != nil || !errors.Is(err, ErrProcessObservationUnsupported) {
		t.Fatalf("watch=%v err=%v, want named unsupported result", watch, err)
	}
}
