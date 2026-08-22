//go:build windows

package conptycompare

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
	qconpty "github.com/qsocket/conpty-go"
	rconpty "github.com/rurreac/conpty"
	"golang.org/x/sys/windows"
)

type terminalProcess interface {
	io.ReadWriteCloser
	Resize(int, int) error
	Wait(context.Context) error
}

type qsocketProcess struct{ value *qconpty.ConPty }

func (p qsocketProcess) Read(b []byte) (int, error)     { return p.value.Read(b) }
func (p qsocketProcess) Write(b []byte) (int, error)    { return p.value.Write(b) }
func (p qsocketProcess) Close() error                   { return p.value.Close() }
func (p qsocketProcess) Resize(w, h int) error          { return p.value.Resize(w, h) }
func (p qsocketProcess) Wait(ctx context.Context) error { _, err := p.value.Wait(ctx); return err }

type rurreacProcess struct{ value *rconpty.LocalConPty }

func (p rurreacProcess) Read(b []byte) (int, error)     { return p.value.Read(b) }
func (p rurreacProcess) Write(b []byte) (int, error)    { return p.value.Write(b) }
func (p rurreacProcess) Close() error                   { return p.value.Close() }
func (p rurreacProcess) Resize(w, h int) error          { return p.value.Resize(w, h) }
func (p rurreacProcess) Wait(ctx context.Context) error { _, err := p.value.Wait(ctx); return err }

type goPTYProcess struct {
	value gopty.Pty
	cmd   *gopty.Cmd
}

func (p goPTYProcess) Read(b []byte) (int, error)  { return p.value.Read(b) }
func (p goPTYProcess) Write(b []byte) (int, error) { return p.value.Write(b) }
func (p goPTYProcess) Close() error                { return p.value.Close() }
func (p goPTYProcess) Resize(w, h int) error       { return p.value.Resize(w, h) }
func (p goPTYProcess) Wait(ctx context.Context) error {
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestCandidateRuntimeContracts(t *testing.T) {
	system, err := windows.GetSystemDirectory()
	if err != nil {
		t.Fatal(err)
	}
	shell := filepath.Join(system, "cmd.exe")
	candidates := map[string]func() (terminalProcess, error){
		"qsocket": func() (terminalProcess, error) {
			value, err := qconpty.Start(shell, qconpty.ConPtyDimensions(80, 24))
			return qsocketProcess{value}, err
		},
		"rurreac": func() (terminalProcess, error) {
			value, err := rconpty.StartConPty(shell, 80, 24, os.Environ())
			return rurreacProcess{value}, err
		},
		"go-pty": func() (terminalProcess, error) {
			value, err := gopty.New()
			if err != nil {
				return nil, err
			}
			cmd := value.Command(shell)
			cmd.Env = os.Environ()
			if err := cmd.Start(); err != nil {
				_ = value.Close()
				return nil, err
			}
			return goPTYProcess{value: value, cmd: cmd}, nil
		},
	}
	for name, start := range candidates {
		t.Run(name, func(t *testing.T) { verifyCandidate(t, start) })
	}
}

func verifyCandidate(t *testing.T, start func() (terminalProcess, error)) {
	value, err := start()
	if err != nil {
		t.Fatal(err)
	}
	defer value.Close()
	if err := value.Resize(100, 40); err != nil {
		t.Fatal(err)
	}
	marker := []byte("SOKSAK_CONPTY_CANDIDATE")
	if _, err := value.Write([]byte("echo SOKSAK_CONPTY_CANDIDATE\r\n")); err != nil {
		t.Fatal(err)
	}
	output, err := readUntil(value, marker, 10*time.Second)
	if err != nil {
		t.Fatalf("marker: %v; output=%q", err, output)
	}
	if _, err := value.Write([]byte("exit\r\n")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := value.Wait(ctx); err != nil {
		t.Fatal(err)
	}
}

func readUntil(reader io.Reader, marker []byte, timeout time.Duration) ([]byte, error) {
	result := make(chan struct {
		body []byte
		err  error
	}, 1)
	go func() {
		buffer := make([]byte, 4096)
		var output bytes.Buffer
		for !bytes.Contains(output.Bytes(), marker) {
			count, err := reader.Read(buffer)
			if count > 0 {
				output.Write(buffer[:count])
			}
			if err != nil {
				result <- struct {
					body []byte
					err  error
				}{output.Bytes(), err}
				return
			}
		}
		result <- struct {
			body []byte
			err  error
		}{output.Bytes(), nil}
	}()
	select {
	case value := <-result:
		return value.body, value.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %s", timeout)
	}
}
