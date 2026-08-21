package main

import (
	"sync"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

type observation struct {
	Output *ptycontract.OutputObservation
	Gap    *ptycontract.GapObservation
	Resize *ptycontract.ResizeObservation
	End    *ptycontract.EndObservation
	Opened *ptycontract.OpenedObservation
}

type observer struct {
	mu       sync.Mutex
	ready    *sync.Cond
	capacity int
	queued   int
	events   []observation
	closed   bool
	gaps     uint64
}

func newObserver(capacity int) *observer {
	if capacity < 1 {
		capacity = 1
	}
	value := &observer{capacity: capacity}
	value.ready = sync.NewCond(&value.mu)
	return value
}

func (value *observer) publishOutput(output ptycontract.OutputObservation) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.closed {
		return
	}
	if value.queued+len(output.Bytes) <= value.capacity {
		output.Bytes = append([]byte(nil), output.Bytes...)
		value.events = append(value.events, observation{Output: &output})
		value.queued += len(output.Bytes)
		value.ready.Signal()
		return
	}
	value.publishGapLocked(ptycontract.GapObservation{
		FromEventSequence:    output.EventSequence,
		ThroughEventSequence: output.EventSequence + 1,
		FromSequence:         output.FromSequence,
		ThroughSequence:      output.ThroughSequence,
	})
}

func (value *observer) publishGapLocked(gap ptycontract.GapObservation) {
	if len(value.events) > 0 {
		last := &value.events[len(value.events)-1]
		if last.Gap != nil && last.Gap.ThroughEventSequence == gap.FromEventSequence {
			last.Gap.ThroughEventSequence = gap.ThroughEventSequence
			last.Gap.ThroughSequence = gap.ThroughSequence
			return
		}
	}
	value.gaps++
	value.events = append(value.events, observation{Gap: &gap})
	value.ready.Signal()
}

func (value *observer) publishResize(resize ptycontract.ResizeObservation) {
	value.publish(observation{Resize: &resize})
}

func (value *observer) publishEnd(end ptycontract.EndObservation) {
	value.publish(observation{End: &end})
}

func (value *observer) publishOpened(opened ptycontract.OpenedObservation) {
	value.publish(observation{Opened: &opened})
}

func (value *observer) publish(event observation) {
	value.mu.Lock()
	defer value.mu.Unlock()
	if value.closed {
		return
	}
	value.events = append(value.events, event)
	value.ready.Signal()
}

func (value *observer) next() observation {
	value.mu.Lock()
	defer value.mu.Unlock()
	for len(value.events) == 0 && !value.closed {
		value.ready.Wait()
	}
	if len(value.events) == 0 {
		return observation{}
	}
	event := value.events[0]
	value.events = value.events[1:]
	if event.Output != nil {
		value.queued -= len(event.Output.Bytes)
	}
	return event
}

func (value *observer) close() {
	value.mu.Lock()
	value.closed = true
	value.ready.Broadcast()
	value.mu.Unlock()
}

func (value *observer) gapCount() uint64 {
	value.mu.Lock()
	defer value.mu.Unlock()
	return value.gaps
}
