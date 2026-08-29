//go:build !windows

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// unixProcessTreeReader takes one native process-table snapshot for each inventory request. It
// never infers ownership from names: ancestry from the PTY shell PID is the ownership proof.
type unixProcessTreeReader struct{}

func newProcessTreeReader() processTreeReader { return unixProcessTreeReader{} }

func (unixProcessTreeReader) Descendants(root uint32) ([]processTreeEntry, error) {
	rows, err := exec.Command("ps", "-axo", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	type row struct {
		pid, parent uint32
		command    string
	}
	children := make(map[uint32][]row)
	rootSeen := false
	scanner := bufio.NewScanner(bytes.NewReader(rows))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.ParseUint(fields[0], 10, 32)
		parent, parentErr := strconv.ParseUint(fields[1], 10, 32)
		if pidErr != nil || parentErr != nil {
			continue
		}
		value := row{pid: uint32(pid), parent: uint32(parent), command: strings.Join(fields[2:], " ")}
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
			cwd, err := processCWD(child.pid)
			if err != nil {
				return nil, fmt.Errorf("read cwd for pid %d: %w", child.pid, err)
			}
			result = append(result, processTreeEntry{PID: child.pid, ParentPID: child.parent, Command: child.command, CWD: cwd})
			queue = append(queue, child.pid)
		}
	}
	return result, nil
}

func processCWD(pid uint32) (string, error) {
	if runtime.GOOS == "linux" {
		return filepath.EvalSymlinks(fmt.Sprintf("/proc/%d/cwd", pid))
	}
	output, err := exec.Command("lsof", "-a", "-p", strconv.FormatUint(uint64(pid), 10), "-d", "cwd", "-Fn").Output()
	if err != nil {
		return "", fmt.Errorf("lsof: %w", err)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "n") && len(line) > 1 {
			return line[1:], nil
		}
	}
	return "", fmt.Errorf("cwd was not reported")
}
