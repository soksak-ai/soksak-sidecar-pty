package main

import (
	"encoding/binary"
	"net"
	"time"

	ptycontract "github.com/soksak-ai/soksak-contract-pty"
)

// After an attach, a connection carries raw bytes and takes no further request.
//
// Bytes rather than framed messages because base64 in an envelope costs a third more on every byte
// of a stream whose whole point is volume, and because a frame boundary here would be this daemon's
// rather than the shell's — and this daemon does not read what a byte means, so it has none to offer.

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

func deliverObservations(conn net.Conn, observer *observer) {
	for {
		event := observer.next()
		if event.Output != nil {
			payload := make([]byte, 24+len(event.Output.Bytes))
			binary.BigEndian.PutUint64(payload[0:8], event.Output.EventSequence)
			binary.BigEndian.PutUint64(payload[8:16], event.Output.FromSequence)
			binary.BigEndian.PutUint64(payload[16:24], event.Output.ThroughSequence)
			copy(payload[24:], event.Output.Bytes)
			if !writeObservationFrame(conn, ptycontract.ObservationFrameOutput, payload) {
				return
			}
			continue
		}
		if event.Gap != nil {
			payload := make([]byte, 32)
			binary.BigEndian.PutUint64(payload[0:8], event.Gap.FromEventSequence)
			binary.BigEndian.PutUint64(payload[8:16], event.Gap.ThroughEventSequence)
			binary.BigEndian.PutUint64(payload[16:24], event.Gap.FromSequence)
			binary.BigEndian.PutUint64(payload[24:32], event.Gap.ThroughSequence)
			if !writeObservationFrame(conn, ptycontract.ObservationFrameGap, payload) {
				return
			}
			continue
		}
		if event.Resize != nil {
			payload := make([]byte, 12)
			binary.BigEndian.PutUint64(payload[0:8], event.Resize.EventSequence)
			binary.BigEndian.PutUint16(payload[8:10], event.Resize.Cols)
			binary.BigEndian.PutUint16(payload[10:12], event.Resize.Rows)
			if !writeObservationFrame(conn, ptycontract.ObservationFrameResize, payload) {
				return
			}
			continue
		}
		if event.End != nil {
			payload := make([]byte, 12)
			binary.BigEndian.PutUint64(payload[0:8], event.End.EventSequence)
			binary.BigEndian.PutUint32(payload[8:12], uint32(event.End.ExitCode))
			writeObservationFrame(conn, ptycontract.ObservationFrameEnd, payload)
			return
		}
		if event.Opened != nil {
			payload := make([]byte, 32)
			binary.BigEndian.PutUint64(payload[0:8], event.Opened.Session)
			binary.BigEndian.PutUint64(payload[8:16], event.Opened.Generation)
			binary.BigEndian.PutUint64(payload[16:24], event.Opened.EventSequence)
			binary.BigEndian.PutUint64(payload[24:32], event.Opened.OutputSequence)
			if !writeObservationFrame(conn, ptycontract.ObservationFrameOpened, payload) {
				return
			}
			continue
		}
		return
	}
}

func writeObservationFrame(conn net.Conn, kind byte, payload []byte) bool {
	header := [5]byte{kind}
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if _, err := conn.Write(header[:]); err != nil {
		return false
	}
	_, err := conn.Write(payload)
	return err == nil
}
