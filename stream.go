package main

import (
	"bufio"
	"encoding/json"
	"net"
	"time"

	controlwire "github.com/soksak/soksak-contract-control"
	ptycontract "github.com/soksak/soksak-contract-pty"
)

// The stream socket: one control request, then raw bytes, daemon to client only.
//
// The request is the same envelope the control socket uses, so nothing here is a second wire. What
// makes this connection different is what it answers with: the connection itself, carrying output
// until the session ends.
//
// Bytes rather than framed messages because base64 in an envelope costs a third more on every byte
// of a stream whose whole point is volume, and because a frame boundary here would be this daemon's
// rather than the shell's — and this daemon does not read what a byte means, so it has none to offer.

func (d *daemon) serveStream(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := json.NewEncoder(conn)

	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var request controlwire.Request
	if err := json.Unmarshal(line, &request); err != nil {
		_ = writer.Encode(controlwire.Response{Ok: false, Error: "the line is not a request: " + err.Error()})
		return
	}
	if request.Command != ptycontract.CommandAttach {
		_ = writer.Encode(refusal(request, "UNKNOWN_COMMAND",
			"this socket serves "+ptycontract.CommandAttach+" and nothing else"))
		return
	}
	attach, err := decode[ptycontract.Attach](request.Args)
	if err != nil {
		_ = writer.Encode(refusal(request, "ARGUMENT", err.Error()))
		return
	}
	if attach.Token != d.token {
		_ = writer.Encode(refusal(request, "TOKEN", "the request carries a token this daemon did not issue"))
		return
	}
	value, err := d.registry.get(attach.Session)
	if err != nil {
		_ = writer.Encode(refusal(request, "NO_SESSION", err.Error()))
		return
	}

	at, mode := value.ring.resolve(attach.FromSeq)
	answer := controlwire.Response{
		ID: request.ID, Ok: true,
		Result: controlwire.Answer{Code: "OK", Data: ptycontract.Attached{
			Session: value.id, Mode: mode, StartSeq: at,
		}},
	}
	if err := writer.Encode(answer); err != nil {
		return
	}
	deliver(conn, value.ring, at)
}

// deliver writes the session's output to the connection, batching the way the contract states.
//
// A batch under DeliveryMinHoldBytes is smaller than one read off a master, so it is an echo or a
// prompt and goes now. At or above it the batch is stream output and waits up to the deadline for
// more, up to DeliveryBatchBytes. The wait is armed only while a batch is open and released by the
// batch closing, so nothing here polls and the echo path never pays it.
func deliver(conn net.Conn, r *ring, at uint64) {
	var batch []byte
	var deadline <-chan time.Time

	flush := func() bool {
		if len(batch) == 0 {
			return true
		}
		_, err := conn.Write(batch)
		batch = batch[:0]
		deadline = nil
		return err == nil
	}

	arrivals := make(chan []byte, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		cursor := at
		for {
			bytes, next := r.read(cursor)
			if len(bytes) == 0 {
				close(arrivals)
				return
			}
			cursor = next
			select {
			case arrivals <- bytes:
			case <-done:
				return
			}
		}
	}()

	for {
		select {
		case bytes, open := <-arrivals:
			if !open {
				flush()
				return
			}
			batch = append(batch, bytes...)
			switch {
			case len(batch) >= ptycontract.DeliveryBatchBytes:
				if !flush() {
					return
				}
			case len(batch) < ptycontract.DeliveryMinHoldBytes:
				if !flush() {
					return
				}
			default:
				if deadline == nil {
					deadline = time.After(ptycontract.DeliveryDeadlineMs * time.Millisecond)
				}
			}
		case <-deadline:
			if !flush() {
				return
			}
		}
	}
}
