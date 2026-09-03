# Session state in this daemon

`SESSION.md` S3 in `soksak-core` splits what a session holds into session state, which a later
attachment needs and which survives the process, and process state, which has meaning only while a
process runs. A store cannot be judged against a split nobody wrote down, so this document is that
split for the `session` struct in `session.go`.

`TestEverySessionFieldIsClassified` compares the table below against the struct. A field the struct
holds and this table omits fails, and so does a row naming a field the struct does not hold.

This daemon owns the shell. It does not parse the output it relays, so it holds no screen and
classifies none. The terminal mirror owns that.

## The split

| Field | Class | Reason |
| --- | --- | --- |
| `id` | session | The name every operation takes. A record is found by it |
| `paneID` | session | The caller's coordinate. `byPane` compares it to answer whether a pane already holds a session |
| `windowLabel` | session | The caller's other coordinate, reported in the inventory |
| `cwd` | session | A creation fact. An equivalent shell starts in the same directory |
| `command` | session | A creation fact. What was started |
| `environment` | session | A creation fact. Without it a recreated shell starts under this daemon's environment rather than the one the caller named |
| `startedAt` | session | When the work began. A new process would restamp it and lose that |
| `generation` | process | Seeded per daemon instance so a restart hands no pane the generation it had. A restored session takes the new daemon's |
| `process` | process | The running shell |
| `ring` | session | The retained output tail. Its waiters and leases are process state; the bytes and the floor are not |
| `observers` | process | The live subscriber set |
| `observerTokens` | process | The live subscribers by token |
| `displaying` | process | The subset of live subscribers showing this session |
| `mu` | process | A lock |
| `written` | derived | `ring.write` returns it and `ring.snapshot` returns it as `through`. Storing it would make two sources for one position |
| `closed` | process | Whether `reap` ran. A closed session is removed rather than restored |
| `paused` | process | What a live reader is doing |
| `rendererGeneration` | process | The current renderer lease |
| `rendererAttached` | process | Whether a renderer holds that lease |
| `detachedAt` | process | The abandon timer's start, judged by a running clock |
| `now` | process | The clock |
| `writtenAt` | process | The abandon judgment's other input |
| `resume` | process | A channel |
| `eventSequence` | session | Observers detect gaps by it. A restore that restarted it at zero would report a gap that did not happen |
| `cols` | session | The size applied to the pty. A restore reapplies it |
| `rows` | session | The size applied to the pty. A restore reapplies it |
| `processEnded` | process | A callback into the registry |
| `store` | process | The open handle this session appends through. What it writes survives; the handle does not |

## What S4-2 requires and this daemon does not hold

`SESSION.md` S4-2 requires the creation facts to be enough to create an equivalent session: what
was started, where, and with what environment. All three are held; the exit status is not.

- **The exit status.** `processEnded` receives the code and the struct keeps none, so the record's
  `exitCode` is written as absent. S3 lists it as session state.

## What the store holds and this struct does not

`SESSION.md` S4-5 states two parts. The output is one and this daemon appends it. The modes are the
other, and no field here holds them: this daemon parses no output, so a mode a program set is in no
fact it has. The component that parses reports them through `pty.modes` and the record keeps the
bytes opaquely.

The program running in the session is recorded beside them. `command` is the login shell this daemon
started and a restore starts that again on its own; the program a person was in is what they would
have to start themselves, and a restore never does it for them.

They are a record and not output. A consumer reads them and applies them to a fresh mirror before it
replays; put in the ring they would be replayed and drawn as the characters they are, over the
screen they were meant to restore.
