// soksak-sidecar-pty owns shells and moves bytes, and reads none of them.
//
// It is a unit somebody installs, not part of any application. That is the whole reason it is a
// process: a shell it started survives the application that asked for it, so closing a window or
// upgrading the application does not end what is running in a pane.
//
// Nothing here derives its own place. The home arrives as an argument, and every socket, the token
// and every session's environment come from it or from the caller. A daemon that read its own
// environment would answer differently depending on what launched it, and this one is meant to be
// launched once and spoken to by whatever comes later.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	controlwire "github.com/soksak/soksak-contract-control"
	ptycontract "github.com/soksak/soksak-contract-pty"
)

func main() {
	home := flag.String("home", "", "the identity home this daemon serves; every socket and the token derive from it")
	shell := flag.String("shell", "", "the shell a session runs when the caller names none")
	flag.Parse()

	if *home == "" {
		fail("no home was named. Every socket, the token and the sessions derive from it, and this " +
			"daemon derives none of it for itself: pass -home <path>")
	}
	if err := run(*home, *shell); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "soksak-sidecar-pty: "+message)
	os.Exit(1)
}

func run(home, shell string) error {
	runDirectory := filepath.Join(home, "run")
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		return fmt.Errorf("preparing %s: %w", runDirectory, err)
	}

	token, err := loadOrCreateToken(ptycontract.TokenPath(home))
	if err != nil {
		return err
	}

	d := &daemon{registry: newRegistry(shell), token: token, home: home, identity: ptycontract.UnitName}

	// One socket. A stream is a connection that stopped being request and response, not a second
	// place — and a second address would be a second thing every peer derives, a second bind to get
	// right, and a state where one is up and the other is not.
	control, err := listen(ptycontract.ControlSocketPath(home))
	if err != nil {
		return err
	}

	// The announcement, and it is the only readiness signal this daemon offers.
	//
	// It goes out after the listener is bound and before anything is served, so a caller that has
	// read it can connect. A caller watching for the socket file instead would see it appear at bind
	// time — and would see a file a dead daemon left behind exactly the same way.
	//
	// The token rides on it, because the process that reads this line is the one that started this
	// daemon and is the only one that needs to be told. Every other peer derives the token's path
	// from the home, which is what the file is for.
	announcement, err := json.Marshal(
		controlwire.NewAnnouncement(ptycontract.ControlSocketPath(home)).WithToken(token))
	if err != nil {
		return err
	}
	fmt.Println(string(announcement))
	// Flushed rather than left to the buffer. A caller blocks on this line, so a line still in a
	// buffer is a caller waiting on a daemon that is already serving.
	_ = os.Stdout.Sync()

	go accept(control, d.serveControl)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	_ = control.Close()
	ended := d.registry.shutdown()
	fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: ended %d session(s) on shutdown\n", ended)
	return nil
}

func accept(listener net.Listener, serve func(net.Conn)) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go serve(conn)
	}
}

// listen binds a unix socket, removing a path a dead daemon left behind.
//
// The removal is why a socket file is not a readiness signal: the path exists both when someone is
// listening and when nobody is, and this function is the case where nobody was.
func listen(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("clearing %s: %w", path, err)
	}
	// The length is checked before the bind, because the bind does not say so.
	//
	// A unix socket path goes into a fixed-size struct, and over the limit the kernel answers
	// "invalid argument" — a sentence that names neither the path nor the limit, and that reads like
	// a bug in the caller's arguments rather than like a home whose name is long. Measured
	// 2026-08-20 on this platform: a home under the temporary directory produced exactly that.
	if len(path) > socketPathLimit {
		return nil, fmt.Errorf("the socket path is %d bytes and this platform accepts %d: %s\n"+
			"Every socket derives from the home, so a shorter home is what shortens this",
			len(path), socketPathLimit, path)
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

// loadOrCreateToken reads the shared secret the greeting checks, creating it once.
//
// It is under the home, so a second home is a second daemon with a second token and neither can be
// spoken to with the other's. The file is the daemon's alone to read.
func loadOrCreateToken(path string) (string, error) {
	existing, err := os.ReadFile(path)
	if err == nil && len(existing) > 0 {
		return string(existing), nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating a token: %w", err)
	}
	token := hex.EncodeToString(raw)
	if err := os.WriteFile(path, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return token, nil
}
