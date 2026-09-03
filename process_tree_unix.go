//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrProcessExitedDuringSnapshot = errors.New("process exited during snapshot")

// unixProcessTreeReader takes one native process-table snapshot for each inventory request. It
// never infers ownership from names: ancestry from the PTY shell PID is the ownership proof.
type unixProcessTreeReader struct {
	readProcessTable func() ([]byte, error)
	readProcessCWD   func(uint32) (string, error)
}

func newProcessTreeReader() processTreeReader { return unixProcessTreeReader{} }

func (reader unixProcessTreeReader) Descendants(root uint32) ([]processTreeEntry, error) {
	readProcessTable := reader.readProcessTable
	if readProcessTable == nil {
		readProcessTable = func() ([]byte, error) {
			// The process group comes back with the rest: a terminal gives the keyboard to one group,
			// and that is what says which child is in front rather than merely running.
			return exec.Command("ps", "-axo", "pid=,ppid=,pgid=,command=").Output()
		}
	}
	readProcessCWD := reader.readProcessCWD
	if readProcessCWD == nil {
		readProcessCWD = processCWD
	}
	rows, err := readProcessTable()
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	type row struct {
		pid, parent, group uint32
		command            string
	}
	children := make(map[uint32][]row)
	rootSeen := false
	scanner := bufio.NewScanner(bytes.NewReader(rows))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}
		pid, pidErr := strconv.ParseUint(fields[0], 10, 32)
		parent, parentErr := strconv.ParseUint(fields[1], 10, 32)
		group, groupErr := strconv.ParseUint(fields[2], 10, 32)
		if pidErr != nil || parentErr != nil || groupErr != nil {
			continue
		}
		value := row{
			pid: uint32(pid), parent: uint32(parent), group: uint32(group),
			command: strings.Join(fields[3:], " "),
		}
		children[value.parent] = append(children[value.parent], value)
		if value.pid == root {
			rootSeen = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	if !rootSeen {
		return []processTreeEntry{}, nil
	}
	result := make([]processTreeEntry, 0)
	queue := append([]uint32(nil), root)
	seen := map[uint32]bool{root: true}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, child := range children[parent] {
			if seen[child.pid] {
				continue
			}
			seen[child.pid] = true
			queue = append(queue, child.pid)
			cwd, err := readProcessCWD(child.pid)
			if errors.Is(err, ErrProcessExitedDuringSnapshot) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("read cwd for pid %d: %w", child.pid, err)
			}
			result = append(result, processTreeEntry{
				PID: child.pid, ParentPID: child.parent, GroupID: child.group,
				Command: child.command, CWD: cwd,
			})
		}
	}
	return result, nil
}

func processCWD(pid uint32) (string, error) {
	if runtime.GOOS == "linux" {
		cwd, err := filepath.EvalSymlinks(fmt.Sprintf("/proc/%d/cwd", pid))
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrProcessExitedDuringSnapshot
		}
		return cwd, err
	}
	output, err := exec.Command("lsof", "-a", "-p", strconv.FormatUint(uint64(pid), 10), "-d", "cwd", "-Fn").Output()
	if err != nil {
		if signalErr := unix.Kill(int(pid), 0); errors.Is(signalErr, unix.ESRCH) {
			return "", ErrProcessExitedDuringSnapshot
		}
		return "", fmt.Errorf("lsof: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:], nil
		}
	}
	return "", fmt.Errorf("cwd was not reported")
}
