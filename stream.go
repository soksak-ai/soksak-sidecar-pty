package main

import (
	"bufio"
	"encoding/json"
	"net"
	"time"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

// The stream socket: one NDJSON hello, then raw bytes, daemon to client only.
//
// Bytes rather than JSON frames because base64 in an envelope costs a third more on every byte of a
// stream whose whole point is volume, and because a frame boundary here would be this daemon's
// rather than the shell's — and this daemon does not read what a byte means, so it has no boundary
// to offer.

func (d *daemon) serveStream(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := json.NewEncoder(conn)

	line, err := reader.ReadBytes('\n')
	if err != nil {
		return
	}
	var hello ptycontract.Hello
	if err := json.Unmarshal(line, &hello); err != nil {
		_ = writer.Encode(refuse("HELLO", "the first line is not a hello: "+err.Error()))
		return
	}
	if hello.Version < ptycontract.MinCompatibleClientProtocol || hello.Token != d.token {
		_ = writer.Encode(refuse("HELLO", "this stream hello names a protocol or a token this daemon does not serve"))
		return
	}
	if hello.Session == nil {
		_ = writer.Encode(refuse("HELLO", "a stream hello names the session whose output it carries"))
		return
	}
	value, err := d.registry.get(*hello.Session)
	if err != nil {
		_ = writer.Encode(refuse("NO_SESSION", err.Error()))
		return
	}

	at, mode := value.ring.resolve(hello.FromSeq)
	if err := writer.Encode(ptycontract.StreamAck{Session: value.id, Mode: mode, StartSeq: at}); err != nil {
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
