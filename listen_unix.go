//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
)

func listen(path string) (net.Listener, error) {
	if conn, err := net.Dial("unix", path); err == nil {
		_ = conn.Close()
		return nil, fmt.Errorf("another daemon is already listening at %s", path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("clearing %s: %w", path, err)
	}
	if len(path) > socketPathLimit {
		return nil, fmt.Errorf("the socket path is %d bytes and this platform accepts %d: %s", len(path), socketPathLimit, path)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("binding %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("restricting %s: %w", path, err)
	}
	return listener, nil
}
