package main

import "errors"

const ProcessObserveUnsupportedCode = "PROCESS_OBSERVE_UNSUPPORTED"

var ErrProcessObservationUnsupported = errors.New("native descendant process observation is unsupported")

// processTreeEventSource owns the platform notification boundary. A notification says only that
// ancestry may have changed; the process-tree reader remains the single record materializer.
type processTreeEventSource interface {
	Supported() error
	Observe(root uint32, changed func()) (processTreeWatch, error)
}

// processTreeWatch registers the current owned pids after each event-triggered snapshot. Sync
// returns only entries whose pids were still alive when their native watch was installed.
type processTreeWatch interface {
	Sync(entries []processTreeEntry) ([]processTreeEntry, error)
	Close() error
}

type unsupportedProcessTreeEventSource struct{}

func (unsupportedProcessTreeEventSource) Supported() error {
	return ErrProcessObservationUnsupported
}

func (unsupportedProcessTreeEventSource) Observe(uint32, func()) (processTreeWatch, error) {
	return nil, ErrProcessObservationUnsupported
}
