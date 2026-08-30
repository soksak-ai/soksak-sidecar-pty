//go:build darwin

package main

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

type darwinProcessTreeEventSource struct{}

func newProcessTreeEventSource() processTreeEventSource { return darwinProcessTreeEventSource{} }

func (darwinProcessTreeEventSource) Supported() error { return nil }

func (darwinProcessTreeEventSource) Observe(root uint32, changed func()) (processTreeWatch, error) {
	queue, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("create process kqueue: %w", err)
	}
	watch := &darwinProcessTreeWatch{
		root: root, queue: queue, changed: changed, tracked: make(map[uint32]struct{}),
	}
	watch.mu.Lock()
	err = watch.addLocked(root)
	watch.mu.Unlock()
	if err != nil {
		_ = unix.Close(queue)
		return nil, fmt.Errorf("observe root process %d: %w", root, err)
	}
	go watch.run(queue)
	return watch, nil
}

type darwinProcessTreeWatch struct {
	mu      sync.Mutex
	root    uint32
	queue   int
	changed func()
	tracked map[uint32]struct{}
	closed  bool
}

func (watch *darwinProcessTreeWatch) Sync(entries []processTreeEntry) ([]processTreeEntry, error) {
	watch.mu.Lock()
	defer watch.mu.Unlock()
	if watch.closed {
		return nil, errors.New("process watch is closed")
	}

	wanted := map[uint32]struct{}{watch.root: {}}
	byPID := make(map[uint32]processTreeEntry, len(entries))
	for _, entry := range entries {
		wanted[entry.PID] = struct{}{}
		byPID[entry.PID] = entry
	}
	pids := make([]uint32, 0, len(wanted))
	for pid := range wanted {
		pids = append(pids, pid)
	}
	sort.Slice(pids, func(i, j int) bool { return pids[i] < pids[j] })

	alive := make(map[uint32]struct{}, len(wanted))
	alive[watch.root] = struct{}{}
	for _, pid := range pids {
		if pid == watch.root {
			continue
		}
		if _, tracked := watch.tracked[pid]; !tracked {
			if err := watch.addLocked(pid); err != nil {
				if errors.Is(err, syscall.ESRCH) {
					continue
				}
				return nil, fmt.Errorf("observe descendant process %d: %w", pid, err)
			}
		}
		alive[pid] = struct{}{}
	}

	for pid := range watch.tracked {
		if _, keep := wanted[pid]; keep {
			continue
		}
		watch.deleteLocked(pid)
	}

	result := make([]processTreeEntry, 0, len(alive)-1)
	for _, pid := range pids {
		if pid == watch.root {
			continue
		}
		if _, present := alive[pid]; present {
			result = append(result, byPID[pid])
		}
	}
	return result, nil
}

func (watch *darwinProcessTreeWatch) addLocked(pid uint32) error {
	change := unix.Kevent_t{
		Ident: uint64(pid), Filter: unix.EVFILT_PROC,
		Flags:  unix.EV_ADD | unix.EV_ENABLE | unix.EV_CLEAR,
		Fflags: unix.NOTE_FORK | unix.NOTE_EXEC | unix.NOTE_EXIT,
	}
	if _, err := unix.Kevent(watch.queue, []unix.Kevent_t{change}, nil, nil); err != nil {
		return err
	}
	watch.tracked[pid] = struct{}{}
	return nil
}

func (watch *darwinProcessTreeWatch) deleteLocked(pid uint32) {
	change := unix.Kevent_t{Ident: uint64(pid), Filter: unix.EVFILT_PROC, Flags: unix.EV_DELETE}
	if _, err := unix.Kevent(watch.queue, []unix.Kevent_t{change}, nil, nil); err != nil &&
		!errors.Is(err, syscall.ENOENT) && !errors.Is(err, syscall.ESRCH) {
		// The next snapshot is authoritative. A failed best-effort delete cannot add a record and the
		// kqueue itself is closed with the session, so there is no independent lifetime to leak.
	}
	delete(watch.tracked, pid)
}

func (watch *darwinProcessTreeWatch) run(queue int) {
	events := make([]unix.Kevent_t, 64)
	for {
		n, err := unix.Kevent(queue, nil, events, nil)
		if err != nil {
			watch.mu.Lock()
			closed := watch.closed
			watch.mu.Unlock()
			if closed || errors.Is(err, syscall.EBADF) {
				return
			}
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return
		}
		if n > 0 && watch.changed != nil {
			watch.changed()
		}
	}
}

func (watch *darwinProcessTreeWatch) Close() error {
	watch.mu.Lock()
	if watch.closed {
		watch.mu.Unlock()
		return nil
	}
	watch.closed = true
	queue := watch.queue
	watch.queue = -1
	watch.tracked = nil
	watch.mu.Unlock()
	if err := unix.Close(queue); err != nil && !errors.Is(err, syscall.EBADF) {
		return fmt.Errorf("close process kqueue: %w", err)
	}
	return nil
}
