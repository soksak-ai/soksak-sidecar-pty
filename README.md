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
- **Windows release.** ConPTY, job ownership, resize, Unicode input/output, termination, and restore
  must pass the Windows system suite before a Windows asset is published. Cross-compilation only
  proves that the source compiles; it is not a release verdict. `release/targets.json` therefore
  contains only the macOS and Linux targets that have passed their runtime suites. Windows compile
  checks remain separate from the release matrix.
