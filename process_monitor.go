package main

import (
	"fmt"
	"sync"
	"time"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// processSessionMonitor serializes native notifications with session close. The native watch is
// closed while holding this mutex, so no late callback can resurrect a record after ended events.
type processSessionMonitor struct {
	mu        sync.Mutex
	daemon    *daemon
	session   *session
	watch     processTreeWatch
	closed    bool
	lastError error
}

func (d *daemon) startProcessMonitoring() {
	d.processSetup.Do(func() {
		if d.identity == "" {
			d.identity = componentID
		}
		if d.processTreeEvents == nil {
			d.processTreeEvents = unsupportedProcessTreeEventSource{}
		}
		d.processState = newProcessInventoryState()
		d.processSessions = make(map[uint64]*processSessionMonitor)
		for _, value := range d.registry.bindProcessLifecycle(d.processSessionStarted, d.processSessionEnded) {
			d.processSessionStarted(value)
		}
	})
}

func (d *daemon) processSessionStarted(value *session) {
	d.processSessionsMu.Lock()
	defer d.processSessionsMu.Unlock()

	value.mu.Lock()
	closed := value.closed
	value.mu.Unlock()
	if closed {
		return
	}

	monitor := &processSessionMonitor{daemon: d, session: value}
	if _, exists := d.processSessions[value.id]; exists {
		return
	}
	d.processSessions[value.id] = monitor

	// The shell record is committed before descendant setup. A kernel notification racing setup can
	// therefore only add revision N+1; it cannot publish a child before its owning session exists.
	d.processState.replaceSession(value.id, []ptycontract.Process{d.processRecord(value, "running", nil)}, time.Now().UnixMilli())

	monitor.mu.Lock()
	if err := d.processTreeEvents.Supported(); err == nil {
		watch, watchErr := d.processTreeEvents.Observe(value.process.PID(), monitor.reconcile)
		monitor.watch = watch
		monitor.lastError = watchErr
	} else {
		monitor.lastError = err
	}
	monitor.mu.Unlock()
	monitor.reconcile()
}

// reconcile performs one snapshot comparison because a native event fired (or because the session
// was just registered). It is never called by a timer or by process.inventory.
func (monitor *processSessionMonitor) reconcile() {
	monitor.mu.Lock()
	defer monitor.mu.Unlock()
	if monitor.closed {
		return
	}

	records := []ptycontract.Process{monitor.daemon.processRecord(monitor.session, "running", nil)}
	if monitor.daemon.processTree != nil {
		entries, err := monitor.daemon.processTree.Descendants(monitor.session.process.PID())
		if err != nil {
			monitor.lastError = fmt.Errorf("session %d process tree: %w", monitor.session.id, err)
			return
		}
		if monitor.watch != nil {
			entries, err = monitor.watch.Sync(entries)
			if err != nil {
				monitor.lastError = fmt.Errorf("session %d process watch: %w", monitor.session.id, err)
				return
			}
		}
		for _, child := range entries {
			records = append(records, monitor.daemon.descendantRecord(monitor.session, child))
		}
	}
	monitor.lastError = nil
	monitor.daemon.processState.replaceSession(monitor.session.id, records, time.Now().UnixMilli())
}

func (d *daemon) processSessionEnded(value *session, endedAtUnixMs int64) {
	d.processSessionsMu.Lock()
	monitor := d.processSessions[value.id]
	delete(d.processSessions, value.id)
	d.processSessionsMu.Unlock()
	if monitor == nil {
		d.processState.replaceSession(value.id, nil, endedAtUnixMs)
		return
	}
	monitor.close(endedAtUnixMs)
}

func (monitor *processSessionMonitor) close(endedAtUnixMs int64) {
	monitor.mu.Lock()
	if monitor.closed {
		monitor.mu.Unlock()
		return
	}
	monitor.closed = true
	if monitor.watch != nil {
		_ = monitor.watch.Close()
		monitor.watch = nil
	}
	monitor.mu.Unlock()
	monitor.daemon.processState.replaceSession(monitor.session.id, nil, endedAtUnixMs)
}
