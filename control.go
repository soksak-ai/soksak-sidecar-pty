package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"

	ptycontract "github.com/soksak/soksak-contract-pty"
)

// The control socket: one JSON value per line, both directions.
//
// Every answer is the same envelope. A refusal states a code and a sentence, and absence is not a
// refusal: a pane with no session answers ok with nothing in it, and a pane this daemon cannot
// serve answers not-ok with the reason. A caller that gets one shape for both cannot act on either.

func reply(data any) ptycontract.Reply {
	raw, err := json.Marshal(data)
	if err != nil {
		return refuse("ENCODE", err.Error())
	}
	return ptycontract.Reply{OK: true, Data: raw}
}

func refuse(code, message string) ptycontract.Reply {
	return ptycontract.Reply{OK: false, Code: code, Message: message}
}

type daemon struct {
	registry *registry
	token    string
	home     string
}

// serveControl answers one connection until it closes.
func (d *daemon) serveControl(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := json.NewEncoder(conn)

	if err := d.greet(reader, writer); err != nil {
		return
	}
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		if err := writer.Encode(d.dispatch(line)); err != nil {
			return
		}
	}
}

// greet checks the version and the token before anything else is answered.
//
// A version mismatch is refused here rather than at the first operation that behaves differently,
// because a mismatch found halfway through has already produced answers the caller trusted.
func (d *daemon) greet(reader *bufio.Reader, writer *json.Encoder) error {
	line, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	var hello ptycontract.Hello
	if err := json.Unmarshal(line, &hello); err != nil {
		_ = writer.Encode(refuse("HELLO", "the first line is not a hello: "+err.Error()))
		return err
	}
	if hello.Version < ptycontract.MinCompatibleClientProtocol {
		message := fmt.Sprintf("this daemon serves protocol %d and above; the hello named %d",
			ptycontract.MinCompatibleClientProtocol, hello.Version)
		_ = writer.Encode(refuse("PROTOCOL", message))
		return fmt.Errorf("%s", message)
	}
	if hello.Token != d.token {
		_ = writer.Encode(refuse("TOKEN", "the hello carries a token this daemon did not issue"))
		return fmt.Errorf("token")
	}
	return writer.Encode(reply(map[string]any{
		"version":  ptycontract.ProtocolVersion,
		"handoff":  ptycontract.HandoffNone,
		"sessions": len(d.registry.list()),
	}))
}

func (d *daemon) dispatch(line []byte) ptycontract.Reply {
	var named ptycontract.OperationRequest
	if err := json.Unmarshal(line, &named); err != nil {
		return refuse("PARSE", err.Error())
	}
	switch named.Op {
	case "open":
		return d.open(line)
	case "write":
		return d.write(line)
	case "resize":
		return d.resize(line)
	case "ack":
		return d.ack(line)
	case "close":
		return d.closeSession(line)
	case "sessions":
		return reply(d.registry.list())
	case "pane":
		return d.pane(line)
	case "closeWindow":
		return d.closeWindow(line)
	case "status":
		return reply(map[string]any{
			"version":  ptycontract.ProtocolVersion,
			"handoff":  ptycontract.HandoffNone,
			"sessions": d.registry.list(),
		})
	case "handoff":
		// Declared and refused rather than absent. A caller that gets "unknown operation" cannot
		// tell a daemon that will never hand off from one that is older than the idea, and it
		// retries against both.
		return refuse("UNSERVED", "this daemon reports handoff level 0: no fd plan is written, "+
			"and ordering a handoff without one ends every shell it holds")
	default:
		return refuse("UNKNOWN_OP", "no operation named "+named.Op)
	}
}

func (d *daemon) open(line []byte) ptycontract.Reply {
	var request ptycontract.CreateOrAttachRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	// A pane that already holds a session is answered with that session rather than given a second
	// shell. Two shells behind one pane is a shell nobody can reach and nobody reaps.
	if existing, held := d.registry.byPane(request.PaneID); held && request.PaneID != "" {
		return reply(sessionAnswer(existing, false))
	}
	value, err := d.registry.open(request)
	if err != nil {
		return refuse("OPEN", err.Error())
	}
	return reply(sessionAnswer(value, true))
}

func sessionAnswer(value *session, created bool) map[string]any {
	pid := 0
	if value.cmd != nil && value.cmd.Process != nil {
		pid = value.cmd.Process.Pid
	}
	return map[string]any{
		"session":    value.id,
		"paneId":     value.paneID,
		"generation": value.generation,
		"shellPid":   pid,
		"created":    created,
	}
}

func (d *daemon) write(line []byte) ptycontract.Reply {
	var request ptycontract.WriteRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil {
		return refuse("PARSE", "dataB64 is not base64: "+err.Error())
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return refuse("NO_SESSION", err.Error())
	}
	if err := value.write(data); err != nil {
		return refuse("WRITE", err.Error())
	}
	return reply(map[string]any{"bytes": len(data)})
}

func (d *daemon) resize(line []byte) ptycontract.Reply {
	var request ptycontract.ResizeRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return refuse("NO_SESSION", err.Error())
	}
	if err := value.resize(request.Cols, request.Rows); err != nil {
		return refuse("RESIZE", err.Error())
	}
	return reply(map[string]any{"cols": request.Cols, "rows": request.Rows})
}

func (d *daemon) ack(line []byte) ptycontract.Reply {
	var request ptycontract.AckRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return refuse("NO_SESSION", err.Error())
	}
	value.ack(request.Bytes)
	return reply(map[string]any{"acked": request.Bytes})
}

func (d *daemon) closeSession(line []byte) ptycontract.Reply {
	var request ptycontract.SessionRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	if err := d.registry.close(request.Session); err != nil {
		return refuse("NO_SESSION", err.Error())
	}
	return reply(map[string]any{"session": request.Session})
}

// pane answers whether a pane holds a live session, and which.
//
// Absence answers ok with held false. A pane with no session is a fact, not a failure, and a caller
// that received a refusal for it would report a broken daemon on every empty pane.
func (d *daemon) pane(line []byte) ptycontract.Reply {
	var request ptycontract.PaneRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	value, held := d.registry.byPane(request.PaneID)
	if !held {
		return reply(map[string]any{"paneId": request.PaneID, "held": false})
	}
	answer := sessionAnswer(value, false)
	answer["held"] = true
	return reply(answer)
}

// closeWindow ends every session the caller opened under one window label.
//
// The label is opaque: this daemon matches the string it was given and reads nothing into it.
func (d *daemon) closeWindow(line []byte) ptycontract.Reply {
	var request ptycontract.WindowRequest
	if err := json.Unmarshal(line, &request); err != nil {
		return refuse("PARSE", err.Error())
	}
	ended := make([]uint64, 0)
	for _, info := range d.registry.list() {
		if info.WindowLabel != nil && *info.WindowLabel == request.WindowLabel {
			if err := d.registry.close(info.Session); err == nil {
				ended = append(ended, info.Session)
			}
		}
	}
	return reply(map[string]any{"windowLabel": request.WindowLabel, "ended": ended})
}
