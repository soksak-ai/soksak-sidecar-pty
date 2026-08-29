// soksak-sidecar-pty owns shells and moves bytes, and reads none of them.
//
// It is an installed sidecar, not part of any application. That is the whole reason it is a
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
	"runtime"
	"syscall"

	controlwire "github.com/soksak-ai/soksak-contract-control"
	ptycontract "github.com/soksak-ai/soksak-contract-pty"
	"time"
)

func main() {
	home := flag.String("home", "", "the identity home this daemon serves; every socket and the token derive from it")
	runtimeRoot := flag.String("runtime", "", "the identity runtime root for sockets and tokens")
	shell := flag.String("shell", "", "the shell a session runs when the caller names none")
	flag.Parse()
	processLabel, err := processLabelFromEnvironment(os.Getenv(controlwire.ProcessLabelEnvironment))
	if err != nil {
		fail("PROCESS_LABEL_INVALID: " + err.Error())
	}
	sidecarName, err := controlwire.ParseProcessLabel(os.Getenv(controlwire.SidecarNameEnvironment))
	if err != nil {
		fail("SIDECAR_NAME_INVALID: " + err.Error())
	}
	if *home == "" {
		fail("no home was named. Every socket, the token and the sessions derive from it, and this " +
			"daemon derives none of it for itself: pass -home <path>")
	}
	if *runtimeRoot == "" || !filepath.IsAbs(*runtimeRoot) {
		fail("-runtime requires an absolute identity runtime root")
	}
	if err := run(*home, *runtimeRoot, *shell, processLabel, sidecarName); err != nil {
		fail(err.Error())
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "soksak-sidecar-pty: "+message)
	os.Exit(1)
}

func run(home, runtimeRoot, shell, processLabel, sidecarName string) error {
	runDirectory := runtimeRoot
	if err := os.MkdirAll(runDirectory, 0o700); err != nil {
		return fmt.Errorf("preparing %s: %w", runDirectory, err)
	}

	token, err := loadOrCreateToken(ptycontract.TokenPath(runtimeRoot, sidecarName))
	if err != nil {
		return err
	}

	d := &daemon{registry: newRegistry(shell), token: token, home: home, identity: ptycontract.SidecarName, processLabel: processLabel, processTree: newProcessTreeReader()}

	// One socket. A stream is a connection that stopped being request and response, not a second
	// place — and a second address would be a second thing every peer derives, a second bind to get
	// right, and a state where one is up and the other is not.
	controlAddress := ptycontract.ControlSocketPath(runtimeRoot, sidecarName, runtime.GOOS == "windows")
	control, err := listen(controlAddress)
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
		controlwire.NewAnnouncement(controlAddress, processLabel).WithToken(token))
	if err != nil {
		return err
	}
	fmt.Println(string(announcement))
	// Flushed rather than left to the buffer. A caller blocks on this line, so a line still in a
	// buffer is a caller waiting on a daemon that is already serving.
	_ = os.Stdout.Sync()

	go accept(control, d.serveControl)

	// Sessions nothing reattaches to end on their own. Without this a run that went away leaves its
	// shells running for as long as this daemon does, and nothing can reach them again.
	sweepDone := make(chan struct{})
	go sweepAbandoned(d.registry, sweepDone)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	close(sweepDone)
	_ = control.Close()
	ended := d.registry.shutdown()
	fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: ended %d session(s) on shutdown\n", ended)
	return nil
}

// sweepAbandoned ends sessions past the abandon window until the daemon stops.
func sweepAbandoned(reg *registry, done <-chan struct{}) {
	ticker := time.NewTicker(sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if ended := reg.endAbandoned(); ended > 0 {
				fmt.Fprintf(os.Stderr, "soksak-sidecar-pty: ended %d abandoned session(s)\n", ended)
			}
		}
	}
}

const sweepEvery = 15 * time.Second

func accept(listener net.Listener, serve func(net.Conn)) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go serve(conn)
	}
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
