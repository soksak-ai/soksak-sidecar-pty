# soksak-sidecar-pty

Owns shells. Moves bytes. Reads none of them.

Implements `soksak-spec-sidecar-pty` (`soksak-contracts/soksak-contract-pty`).

## Why a process

A shell it started survives the application that asked for it. Closing a window, restarting, or
upgrading the application does not end what is running in a pane — which is the whole reason this is
a process and not a library.

Everything downstream of that follows: the ring holds what a shell printed while nobody was
attached, `fromSeq` is how a client that comes back says where it got to, and the flow-control
watermarks bound what one unattended session can cost.
Status reports the last size successfully applied to each PTY and its source event sequence.

`process.inventory` is the owner-facing snapshot for the process monitor. It reports the shell
session records this sidecar started with explicit owner, window, pane, pid, command, state and
timestamps, plus Unix descendants found from the shell's process ancestry. `process.observe` keeps
one connection open after its initial snapshot and emits revisioned `started`/`ended` records until
the peer closes. Windows ConPTY job enumeration and updated events are separate gates; this stream
is not a complete monitor until the selected platform's ownership coverage is implemented.

A session has one renderer: the last to attach. A run that went away without detaching left a mark,
and refusing the next attach because of it would leave a pane nothing can ever draw again. The one
that left holds an older generation, so its detach cannot take the one that replaced it.

A session with no renderer and no output for half an hour is what a run that went away left behind,
and it ends: nothing can reach that shell, and it holds a process, its output ring and its file
descriptors. A session still producing output is doing work for someone and is never ended here,
whatever is attached to it.

## What it does not do

It never reads what a byte means. No escape sequences, no screen, no scrollback grid, no prompt
detection. A pane id and a window label travel through it untouched: they come in on a request, they
go out on a status, and nothing here decides anything from them.

That is why it is not the terminal's. A shell with a tty is what a terminal needs and also what a
REPL, a remote session and a build task need, so a daemon named for one of them would be renamed for
the second.

## Talking to it

One socket, under the home, checking a token in the greeting on every connection.

One JSON value per line, both directions. `pty.attach` turns the connection it arrives on into a
stream: after its answer, that connection carries raw output bytes and takes no further request. A
stream is a connection that stopped being request and response, not a second address.

**Readiness is the first stdout line**, which names the bound socket and the protocol version. A
socket file on disk is not readiness: the path exists from the moment of bind and also for as long
as the filesystem holds one a dead daemon left behind.

```
soksak-sidecar-pty -home <identity home> [-shell <path>]
```

Nothing is derived from the environment. The home arrives as an argument, and a session's shell,
working directory and environment arrive on the request that opens it — this process outlives
whatever launched it, so its own environment is a snapshot with no claim on a session started later.

## Not built

- **Handoff.** The contract has a level for a live upgrade that preserves fds and ring coordinates.
  This build reports level 0 and refuses the operation by name. Claiming the level without the fd
  plan ends every shell on the first upgrade.
- **No implicit migration.** A release publishes only the targets declared in
  `release/targets.json`. ConPTY behavior is verified by the Windows owner tests and the installed
  terminal system suite; cross-compilation alone is not a runtime verdict.

## Build contract

`go.mod` is the only Go-version owner, and `release/targets.json` is the only published-target
owner. The Make entrypoint requires one explicit native target and rejects a mismatched Go runtime
with exit 78 before dependency materialization or compilation.

```sh
make verify TARGET=aarch64-apple-darwin
make stage TARGET=aarch64-apple-darwin OUT=dist
```

`build` writes only `target/<target>/release/soksak-sidecar-pty[.exe]`. `stage` never compiles; it
copies that declared artifact and rejects a byte conflict on a repeated invocation. Release Actions
install the toolchain from `go.mod` and invoke these same Make commands for every target. The
immutable spec validator is supplied by release-train URL and SHA-256 inputs rather than a source
checkout or a repository-relative dependency path.
