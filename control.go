package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"

	controlwire "github.com/soksak/soksak-contract-control"
	ptycontract "github.com/soksak/soksak-contract-pty"
)

// The control socket answers on the control envelope, not on a wire of its own.
//
// One envelope rather than two is the point: a second one diverges from the first, and a divergence
// does not fail — it arrives as a different answer. So a client that can talk to the application can
// talk to this, with the same framing, the same correlation id and the same greeting.
//
// The commands are registered here and on nothing else. Nothing this daemon serves appears on the
// application's registry: replacing this unit would otherwise change what the application answers,
// two units driving one daemon would collide on the same names, and the permission that admitted a
// call would have been granted to something no manifest declared.

type daemon struct {
	registry *registry
	token    string
	home     string
	identity string
}

// handler answers one command's arguments.
type handler func(args map[string]json.RawMessage) (string, any, error)

func (d *daemon) commands() map[string]handler {
	return map[string]handler{
		ptycontract.CommandOpen:        d.open,
		ptycontract.CommandWrite:       d.write,
		ptycontract.CommandResize:      d.resize,
		ptycontract.CommandAck:         d.ack,
		ptycontract.CommandClose:       d.closeSession,
		ptycontract.CommandSessions:    d.sessions,
		ptycontract.CommandPane:        d.pane,
		ptycontract.CommandCloseWindow: d.closeWindow,
		ptycontract.CommandStatus:      d.status,
	}
}

// unserved is what this daemon declares and refuses, with the reason.
//
// Declared rather than left out. A caller that receives "unknown command" cannot tell a level this
// build will never keep from a name older than the idea, and it retries against both.
func (d *daemon) unserved() map[string]string {
	return map[string]string{
		ptycontract.CommandHandoff: "this daemon reports handoff level 0: no fd plan is written, and " +
			"ordering a handoff without one ends every shell it holds",
	}
}

func (d *daemon) serveControl(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := json.NewEncoder(conn)
	commands := d.commands()
	greeted := false

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var request controlwire.Request
		if err := json.Unmarshal(line, &request); err != nil {
			if err := writer.Encode(controlwire.Response{
				Ok: false, Error: "the line is not a request: " + err.Error(),
			}); err != nil {
				return
			}
			continue
		}

		answer := d.answer(request, commands, &greeted)
		if err := writer.Encode(answer); err != nil {
			return
		}
	}
}

// answer runs one request. The greeting has to come first: a caller that skipped it has not agreed a
// protocol, and answering it anyway is how a mismatch reaches the first command instead of the
// greeting.
func (d *daemon) answer(request controlwire.Request, commands map[string]handler, greeted *bool) controlwire.Response {
	if request.Command == controlwire.HelloCommand {
		return d.greet(request, greeted)
	}
	if !*greeted {
		return refusal(request, "GREETING", "this connection has not agreed a protocol: send "+
			controlwire.HelloCommand+" before anything else")
	}
	if reason, declared := d.unserved()[request.Command]; declared {
		return refusal(request, "UNSERVED", reason)
	}
	run, served := commands[request.Command]
	if !served {
		return refusal(request, "UNKNOWN_COMMAND", "this daemon serves no command named "+request.Command)
	}
	code, result, err := run(request.Args)
	if err != nil {
		return refusal(request, code, err.Error())
	}
	return controlwire.Response{
		ID: request.ID, Ok: true,
		Result: controlwire.Answer{Code: "OK", Data: result},
	}
}

// greet checks the protocol and the token, and answers with what this daemon serves and refuses.
//
// A version mismatch is refused here rather than at the first command that behaves differently: a
// mismatch found halfway through has already produced answers the caller trusted.
func (d *daemon) greet(request controlwire.Request, greeted *bool) controlwire.Response {
	if raw, asked := request.Args["protocol"]; asked {
		var wanted int
		if err := json.Unmarshal(raw, &wanted); err != nil {
			return refusal(request, "PROTOCOL", fmt.Sprintf("argument %q: %v", "protocol", err))
		}
		if wanted != controlwire.Protocol {
			return refusal(request, "PROTOCOL", fmt.Sprintf(
				"this daemon speaks protocol %d and the client asked for %d", controlwire.Protocol, wanted))
		}
	}
	var token string
	if raw, sent := request.Args["token"]; sent {
		if err := json.Unmarshal(raw, &token); err != nil {
			return refusal(request, "TOKEN", fmt.Sprintf("argument %q: %v", "token", err))
		}
	}
	if token != d.token {
		return refusal(request, "TOKEN", "the greeting carries a token this daemon did not issue")
	}
	*greeted = true

	table := controlwire.Table{
		Commands: make([]controlwire.Served, 0, len(d.commands())),
		Unserved: make([]controlwire.Unserved, 0, len(d.unserved())),
	}
	for name := range d.commands() {
		table.Commands = append(table.Commands, controlwire.Served{Name: name, Owner: controlwire.OwnerPlugin})
	}
	for name, reason := range d.unserved() {
		table.Unserved = append(table.Unserved, controlwire.Unserved{Name: name, BlockedBy: reason})
	}
	sortTable(&table)

	return controlwire.Response{
		ID: request.ID, Ok: true,
		Result: controlwire.Greeting{
			Protocol:  controlwire.Protocol,
			Identity:  d.identity,
			Commands:  table,
			Language:  "en",
			Languages: []string{"en"},
		},
	}
}

func refusal(request controlwire.Request, code, message string) controlwire.Response {
	return controlwire.Response{
		ID: request.ID, Ok: false, Error: message,
		Result: controlwire.Answer{Code: code},
	}
}

// decode reads one command's arguments out of the envelope.
//
// Arguments arrive under a single "request" key rather than spread across the map, because the
// payload shapes are the contract's and splitting them here would restate every field name in a
// second place.
func decode[T any](args map[string]json.RawMessage) (T, error) {
	var value T
	raw, given := args["request"]
	if !given {
		return value, fmt.Errorf("no %q argument: the command's payload travels under it", "request")
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("argument %q: %w", "request", err)
	}
	return value, nil
}

func (d *daemon) open(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Open](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	// A pane that already holds a session is answered with it rather than given a second shell. Two
	// shells behind one pane is a shell nobody can reach and nobody reaps.
	if existing, held := d.registry.byPane(request.PaneID); held && request.PaneID != "" {
		return "", opened(existing, false), nil
	}
	value, err := d.registry.open(request)
	if err != nil {
		return "OPEN", nil, err
	}
	return "", opened(value, true), nil
}

func opened(value *session, created bool) ptycontract.Opened {
	pid := 0
	if value.cmd != nil && value.cmd.Process != nil {
		pid = value.cmd.Process.Pid
	}
	return ptycontract.Opened{
		Session: value.id, PaneID: value.paneID, Generation: value.generation,
		ShellPID: pid, Created: created,
	}
}

func (d *daemon) write(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Write](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	data, err := base64.StdEncoding.DecodeString(request.DataBase64)
	if err != nil {
		return "ARGUMENT", nil, fmt.Errorf("dataB64 is not base64: %w", err)
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return "NO_SESSION", nil, err
	}
	if err := value.write(data); err != nil {
		return "WRITE", nil, err
	}
	return "", map[string]int{"bytes": len(data)}, nil
}

func (d *daemon) resize(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Resize](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return "NO_SESSION", nil, err
	}
	if err := value.resize(request.Cols, request.Rows); err != nil {
		return "RESIZE", nil, err
	}
	return "", request, nil
}

func (d *daemon) ack(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Ack](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	value, err := d.registry.get(request.Session)
	if err != nil {
		return "NO_SESSION", nil, err
	}
	value.ack(request.Bytes)
	return "", request, nil
}

func (d *daemon) closeSession(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Session](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	if err := d.registry.close(request.Session); err != nil {
		return "NO_SESSION", nil, err
	}
	return "", request, nil
}

func (d *daemon) sessions(map[string]json.RawMessage) (string, any, error) {
	return "", d.registry.list(), nil
}

func (d *daemon) status(map[string]json.RawMessage) (string, any, error) {
	return "", ptycontract.Status{
		Version:  ptycontract.Version,
		Handoff:  ptycontract.HandoffNone,
		Sessions: d.registry.list(),
	}, nil
}

func (d *daemon) pane(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Pane](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	value, held := d.registry.byPane(request.PaneID)
	if !held {
		return "", ptycontract.Held{PaneID: request.PaneID, Held: false}, nil
	}
	answer := opened(value, false)
	return "", ptycontract.Held{PaneID: request.PaneID, Held: true, Opened: &answer}, nil
}

// closeWindow ends every session opened under one window label. The label is opaque: this daemon
// matches the string it was given and reads nothing into it.
func (d *daemon) closeWindow(args map[string]json.RawMessage) (string, any, error) {
	request, err := decode[ptycontract.Window](args)
	if err != nil {
		return "ARGUMENT", nil, err
	}
	ended := make([]uint64, 0)
	for _, info := range d.registry.list() {
		if info.WindowLabel != nil && *info.WindowLabel == request.WindowLabel {
			if err := d.registry.close(info.Session); err == nil {
				ended = append(ended, info.Session)
			}
		}
	}
	return "", map[string]any{"windowLabel": request.WindowLabel, "ended": ended}, nil
}
