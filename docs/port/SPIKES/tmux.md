# SPIKE: driving the real tmux binary

**Phase 2 dependency spike** for PORT_SPEC §3.3 item 2 ("tmux as a first-class backend, done
properly"), a §0.2 #2 sanctioned rebuild. Status: landed, tests green against real tmux, **§10
adversarial pass done** (two independent reviews + fixer, Phase 3). Findings applied are marked
in place below; the one open item is OI-1 (package placement, needs a human ruling on a frozen
document).

Code: `term/tmuxdrv/` (`tmuxdrv.go`, `naming.go`, `session.go`, `attach.go`,
`attach_unix.go`, `attach_other.go`; tests `naming_test.go`, `integration_test.go`).

---

## 1. Environment

| | |
|---|---|
| tmux | **3.5a** (`/usr/bin/tmux`, Debian 13 / kernel 6.12) |
| Go | 1.26.5 linux/amd64 |
| pty source for tests | `script` from util-linux 2.41 |
| tmux tmpdir | `/tmp/tmux-0` (uid 0); the agent VM itself runs inside tmux on the `default` socket |

Assumptions this spike does **not** make: that tmux ≥ 3.5 is what users have. Version floors,
read off the upstream changelog shipped with the package:

| Feature used | Since | Note |
|---|---|---|
| `=` exact-match session/window targets | **2.1** (Oct 2015) | load-bearing everywhere (quirk 1) |
| `#{socket_path}` format | **2.2** (Apr 2016) | only in the nesting check (quirk 13) |
| `new-window -e` | **3.0** (Sep 2019) | only when `Window.Env` is non-empty |
| `new-session -e` | **3.2** (Feb 2021) | only when `Window.Env` is non-empty on the *first* device |

`-F` formats, `-L`, `-f`, `has-session`, `--`, `remain-on-exit` and command chaining with `;` all
predate 2.1. So the effective floor is **2.1** for Kathara's default path (no injected
environment) and **3.2** if `Window.Env` is used; `creationFlags` only emits `-e` when `Env` is
set. Dropping the 3.2 floor means replacing the session-creating `-e` with `set-environment -t
<session>` (see OI-8).

---

## 2. What the prototype does

One package, standard library only, no module dependencies — so it compiles and tests without
Docker, without a lab, and without a TTY.

```go
d := &tmuxdrv.Driver{}                          // user's default socket + config
session := tmuxdrv.SessionName(lab.Name, lab.Hash)

outcome, err := d.EnsureWindow(ctx, session, tmuxdrv.Window{
    Name:    "pc1",                             // window name == device name
    Command: "/usr/bin/kathara connect -l pc1",
    Dir:     lab.Path,
})                                              // SessionCreated | WindowCreated | WindowExisted

err = d.Attach(ctx, session, "pc1")             // select-window, then execve into tmux
```

| Operation | tmux CLI |
|---|---|
| `HasSession` | `has-session -t '=<s>'` — exit status only |
| `ListSessions` / `ListKatharaSessions` | `list-sessions -F '#{session_name}'` |
| `EnsureSession` | `has-session`, then `new-session -d -s <s> -n <w> [-c] [-e] -- <cmd>` |
| `EnsureWindow` / `AddWindow` | `new-window -d -t '=<s>:' -n <w> [-c] [-e] -- <cmd>` |
| `ListWindows` / `WindowNames` / `HasWindow` | `list-windows -t '=<s>' -F '#{window_index}\t#{window_name}\t#{window_id}\t#{window_active}\t#{pane_dead}'` |
| `SelectWindow` | `select-window -t '=<s>:=<w>'` |
| `Attach` | `select-window`, then `execve(tmux attach-session -t '=<s>')` — or `switch-client -t '=<s>'` when kathara is inside the target server (quirk 13) |
| `DetachSession` | `detach-client -s '=<s>'` |
| `ClientTTYs` | `list-clients -t '=<s>' -F '#{client_tty}'` |
| `KillSession` / `KillWindow` | `kill-session -t '=<s>'` / `kill-window -t '=<s>:=<w>'` |
| `Version` / `Available` | `tmux -V` (no server contacted) |

No tmux output is parsed except values tmux was explicitly asked to print with `-F`, plus a
whitelist of stderr strings used *only* to tell "absent" from "failed" (§4, quirk 5).

**Size, against the §3.3 budget of ~250 SLOC for the tmux backend:** 656 SLOC excluding tests and
comments (`session.go` 315, `tmuxdrv.go` 159, `naming.go` 66, `attach*.go` 116). Roughly a third
is surface `term` may never call (`ListKatharaSessions`, `KillWindow`, `ClientTTYs`, `SocketPath`,
the full `WindowInfo`) and can be trimmed when the caller exists; the rest is the exit-code and
absence handling quirks 4–5 force on anything driving this CLI honestly. The 250-line estimate
looks low by roughly 2x for the transport alone, before any Kathara-facing glue.

---

## 3. Naming: the OQ-9 ruling

> **OQ-9 (SYNTHESIS §2, Tier 2).** Today all path-parsed labs (`lab.name is None`) share one tmux
> session `"Kathara"`; only vlab/named labs get their own. §3.3 mandates "one session per scenario
> named from the lab" — approve the change and define naming for unnamed labs (hash?).

**Ruled, and implemented as `tmuxdrv.SessionName(labName, labHash)`:**

1. **Approved: one session per network scenario.** The shared `"Kathara"` session is gone. This
   is the §3.3 requirement and is inside the §0.2 #2 sanction; it is recorded as a divergence
   below rather than replicated.
2. **Named scenario → `kathara_<sanitized name>`.** (`Lab("megalos")` → `kathara_megalos`.)
3. **Unnamed scenario → `kathara_<lab hash>`.** `Lab.hash` is the URL-safe md5 of the scenario's
   real path — already the identity Kathara uses for container labels, Docker network names and
   the Kubernetes namespace, and stable across invocations from the same directory, which is what
   makes reattach and `connect --tmux` find the same session in a later process.
4. **`kathara_` prefix, always.** Not decoration: "never clobber" has to hold against the *user's*
   sessions too. Without the prefix, a scenario named `work` would silently adopt the unrelated
   tmux session `work` the user has been living in, and add device windows to it. The prefix also
   makes Kathara's sessions identifiable in `tmux ls` (`ListKatharaSessions`).
5. **Sanitization is ours, not tmux's.** tmux rewrites `.` and `:` to `_` inside session names
   *silently* (quirk 2). A name we did not sanitize would be stored under a different name than
   the one we probe with — so every `HasSession` would say "absent" and every create would say
   "duplicate session". `SanitizeName` maps `.`, `:`, whitespace, control characters to `_`, and a
   leading `-` to `_`; `EnsureSession` rejects a name that is not already sanitized rather than
   letting it through.

Consequences accepted:

- `Lab("kathara_vlab")` yields `kathara_kathara_vlab`. Stripping an existing `kathara_` prefix was
  rejected: it would make two distinct lab names collide.
- Session names for path-parsed scenarios are opaque hashes in `tmux ls`. See open item OI-5.

The second half of OQ-9 ("Unix emits an initial resize, Windows never does; unify or preserve?")
is *not* in this spike's scope — that is the console-adapter rebuild (`term/console_*.go`,
PACKAGE_GRAPH rows for `TerminalRunner`/`IConsoleAdapter`), not the tmux backend.

---

## 4. tmux quirks found

Every one of these is pinned by a test in `term/tmuxdrv/`.

**1. Targets are fuzzy unless you say otherwise.** `-t lab` resolves by exact name, then prefix,
then fnmatch — with only `lab1` present, `has-session -t lab` exits **0**. Every target this
package emits uses tmux's exact-match form: `-t '=lab'`, `-t '=lab:=pc1'`. Getting this wrong
means `lstart` in `~/labs/demo` adopts the session of `~/labs/demo2`. Window targets need the
`=` twice: session and window are matched independently.
*Pinned by `TestExactMatchTargeting`.*

**2. Session names are silently rewritten.** `new-session -s 'a.b'` creates the session `a_b`;
`-s 'a:b'` also creates `a_b`, so the second one fails with `duplicate session: a_b`. Spaces are
kept; an empty name is rejected (`invalid session: `, exit 1). Sanitize before probing. tmux also
*vis-escapes* control characters in a session name (`session_check_name` → `utf8_stravis`): a
literal newline is stored as the two characters `\` and `n`, so a session someone else created
cannot forge a row boundary in the newline-delimited `list-sessions -F` output, and
`ListKatharaSessions` cannot be made to report a phantom `kathara_…` session. Verified on 3.5a
(one row, one `\n`-free name); an exact-match re-probe of each row would be the fallback if a tmux
old enough to store the raw byte ever turns up.
*Pinned by `TestSanitizedNameRoundTrip`, `TestSessionName`,
`TestForeignSessionNameCannotForgeARow`.*

**3. Server autostart, and "no server" is a normal state.** Any command needing a server starts
one; `new-session` is how Kathara starts it. There is no way to keep a server with zero sessions:
`start-server` exits 0 but the server immediately exits (`exit-empty` defaults on), and killing
the last session ends the server too. So *every* read has to treat "no server" as "no sessions"
rather than as an error.
*Pinned by `TestAbsentServerIsNotAnError`.*

**3b. The socket file outlives the server.** Neither `kill-server` nor the server's own
last-session exit unlinks the socket — verified both ways. That is precisely why there are two
"no server" spellings (quirk 4): `error connecting to …` when the socket file is gone,
`no server running on …` when a stale one is still sitting there. Consequence for tests: `-L
<name>` litters `/tmp/tmux-<uid>` with one dead socket per run forever, so the suite uses `-S
<t.TempDir()>/tmux.sock` instead and Go deletes the directory (§7).

**4. Exit codes carry almost no information.** tmux exits **1** for every error it reports. The
only signal is stderr, and there are two distinct spellings of "no server":

| Situation | stderr | exit |
|---|---|---|
| socket file does not exist | `error connecting to /tmp/tmux-0/<sock> (No such file or directory)` | 1 |
| socket exists, server gone | `no server running on /tmp/tmux-0/<sock>` | 1 |
| session absent | `can't find session: <name>` | 1 |
| window absent | `can't find window: <name>` | 1 |
| create raced | `duplicate session: <name>` | 1 |
| detach with nothing attached | `no current client` | 1 |

**5. Hence: a small, closed stderr whitelist.** `tmuxdrv.go` groups the strings that mean "not
there" by *what* is missing (`noServerMessages`, `sessionAbsentMessages`, `windowAbsentMessages`,
`clientAbsentMessages`, `duplicateSessionMessages`); anything else non-zero is a real failure and
is returned as a `*CommandError` with the full argv. tmux has no localization, so these strings do
not move with the user's locale. This is the one place the package looks at tmux prose, and it is
deliberately confined to absence-vs-failure — never to structure. Two idempotency behaviors fall
out of it: `KillSession` returns `(false, nil)` for an absent session, and `DetachSession` is a
no-op when nothing is attached.

Three rules keep the whitelist honest, all tightened in the Phase 3 review:

- **Every spelling is one this tmux actually emits.** Each was reproduced from the subcommand
  named beside it and exists in `strings /usr/bin/tmux`. Four entries an earlier draft carried
  (`no current server`, `session not found`, `no client with tty`, `can't establish current
  session`) are in no tmux 3.5a binary at all and were dropped rather than left as unverifiable
  widening.
- **A message must begin a line of stderr** (`strings.HasPrefix` per line, not `Contains`
  anywhere), so a genuine failure that merely quotes one of these strings is not read as absence.
  Per-line rather than per-message so a command that starts the server and prints configuration
  diagnostics first still classifies.
- **Each call site passes only what its own command can say.** `kill-session` cannot report a
  missing *window*, so it is not offered that spelling; `detach-client` is, because tmux answers
  `no current client` even when the `-s` target does not resolve.

Two of these are unreachable through the ordinary API — `duplicate session:` needs a lost
creation race, `no current client` on a live server needs a detach with nothing attached — so
`TestAbsenceClassifierSpellings` provokes both directly and also asserts an unknown-command
failure is classified as a failure.

**6. Configuration is read once, at server start.** `-f /dev/null` passed to a command that
reaches an already-running server is ignored. Demonstrated: a config with `set -g base-index 7`
puts the first window at index **7**, and a later `tmux -f /dev/null list-windows` still reports 7.
Two consequences: (a) production must never assume window index 0 — this package addresses
windows by name and only reports indices; (b) test isolation needs **both** a private socket
(`-S`/`-L`, own server) **and** `-f /dev/null` (own configuration), and the `-f` only counts on
the command that starts the server.

**7. Window names are not unique.** `new-window -n pc1` twice yields two windows named `pc1`, and
`=sess:=pc1` then resolves to the lower index. tmux will not stop you from adding a device twice;
`EnsureWindow` checks the listing first. (3.8.3's `libtmux` wrapper made the same check — this is
parity, not a new rule.)
*Pinned by the re-ensure half of `TestEnsureSessionLifecycle`.*

**8. Windows die with their command, and sessions die with their last window.** A window running
`kathara connect` disappears the instant the connect fails; if it was the only device, the session
disappears too, and the user is left staring at their prompt with no diagnostic — a plausible part
of what "the tmux path has shipped broken" (§3.3) looks like in the field. `remain-on-exit` keeps
the dead window and its exit status visible, but it is a window option, not a creation flag, and a
separate `set-option` call loses the race against a command that fails instantly. **Fix: chain
both commands into one tmux invocation** — `new-window … -- false ';' set-option -t '=s:=w' -w
remain-on-exit on` — the server runs both before it returns to its event loop and reaps the pane.
Verified against `false`, which cannot exit sooner. Exposed as `Window.RemainOnExit`, default off
(3.8.3 parity, see OI-4).
*Pinned by `TestWindowOptionsAndDeath`.*

**9. `-n` disables automatic renaming, for free.** A window created without `-n` gets renamed to
whatever command is running in it, which would break name-based device addressing. Passing `-n`
sets `automatic-rename` off for that window (verified: `#{automatic-rename}` = 0 with a live
`sleep` in the pane), so no `set-option allow-rename/automatic-rename` bookkeeping is needed.

**10. Windows inherit the *server's* environment, not the caller's.** The server captured its
environment when it first started, possibly hours earlier in an unrelated shell. `new-session -e
K=V` / `new-window -e K=V` inject per-window variables (verified on 3.5a, incl. that
`show-environment` reports them at session level), and `-c <dir>` sets the start directory.
Anything Kathara needs the device window to see must be passed explicitly; relying on
`os.Environ()` propagating is wrong.
*Pinned by `TestWindowOptionsAndDeath`.*

**11. `--` is honored before the shell-command positional**, so a command starting with `-` can
never be re-read as a flag. `Window.commandArgs` always emits it.

**11b. `--` does not protect against the *command-sequence* parser.** That runs earlier: an
argument ending in an unescaped `;` separates tmux commands, and tmux drops the `;`. So
`new-session … -- 'sleep 60;'` runs `sleep 60` and exits 0 — the command is silently truncated,
not rejected. Measured: `'sleep 60;'` → `pane_start_command` `"sleep 60"`; `'sleep 60 ;'` →
`"sleep 60 "`; `'sleep 60; true'` (mid-string) → unchanged; `'sleep 60\;'` → `"sleep 60;"`. Only a
trailing `;` separates, and escaping it restores it, so `commandArgs` escapes a trailing `;` the
caller did not already escape. Kathara's connect command never ends in `;`; this is the transport
not lying about what it ran.
*Pinned by `TestWindowCommandTrailingSemicolon`.*

**12. Attaching requires a real TTY.** Without one: `open terminal failed: not a terminal`,
exit 1. Tests get a pty from `script -q -e -c <cmd> /dev/null` (`-e` propagates the command's
exit status, which is how the suite asserts a clean exit after detach).

**13. Nesting is refused by `$TMUX` *plus* a pane-tty match — not by `$TMUX` alone.** `$TMUX` is
`<socket-path>,<server-pid>,<session-id>`. tmux's check (`server_client_check_nested`) refuses
`attach-session` only when `$TMUX` is set **and** the attaching client's tty is the tty of one of
*this server's* panes. Measured on 3.5a, all four combinations:

| $TMUX | client tty is a pane of the target server | result |
|---|---|---|
| set (points at the target socket) | yes | refused: `sessions should be nested with care, unset $TMUX to force`, exit 1 |
| set (points at the target socket) | no (fresh pty) | **attaches** — no refusal |
| set (points at a *different* server) | no | **attaches** — no refusal |
| unset | yes | **attaches**: a client inside its own pane, the recursive mirror |

Two consequences for `Attach`, and they pull in opposite directions: dropping `$TMUX` on the
different-server path is belt-and-braces rather than load-bearing (tmux would not have refused
anyway), while dropping it on the *same*-server path would disarm the one protection tmux has and
mirror the terminal into its own pane — row 4. So the same-server branch never execs, and a
failure to establish *which* server we are inside is returned rather than guessed past (an earlier
draft fell back to "assume different server", i.e. straight into row 4; Phase 3 review).

Three cases, all handled in `Attach`:

| kathara runs… | action |
|---|---|
| outside tmux | `execve(attach-session)` |
| inside a client of the **same** server (`#{socket_path}` == `$TMUX` socket) | `switch-client -t '=<s>'`, return normally |
| inside a client of a **different** server (user on `-L work`, Kathara on the default socket) | `execve(attach-session)` with `TMUX` dropped from the child env — not nesting |

The same-server test uses `display-message -p '#{socket_path}'` on the target server rather than
reconstructing `$TMUX_TMPDIR`/`/tmp/tmux-<uid>` by hand, and compares it to `$TMUX`'s socket
symlink-tolerantly (`sameSocket`): the two are byte-identical for the default socket, and a false
"different" is the answer that walks into row 4 of the table above. `switch-client` picks the right client on
its own: with `$TMUX_PANE` inherited from the pane kathara was started in, tmux resolves the
current client to *that* pane's client (verified with two clients attached to different sessions —
only the one owning `$TMUX_PANE` moved).
*Pinned by `TestAttachSelectAndDetach` (both exec paths, the reattach leg asserting a client is
genuinely attached), `TestAttachSwitchesClientOnSameServer` (the switch-client path, asserting the
existing client moved and no second client appeared) and `TestOuterSocketPath`.*

**14. The daemonized server does not hold the client's stdout/stderr.** Worth stating because it
is the classic exec-driver hang: Go's `exec` gives a non-`*os.File` `Stdout` a pipe and `Run()`
waits for EOF on it, so a daemon inheriting the write end would block the caller forever. tmux
3.5a's server does not — `EnsureSession` captures stdout/stderr through pipes on the very command
that starts the server, and returns immediately.

**15. `detach-client -s` detaches *every* client of the session.** If two terminals are attached
to the scenario, `DetachSession` drops both. Per-client detach needs `-t <client-tty>`; not
needed by any current caller.

---

## 5. `kathara connect --tmux <device>` → select-window + attach

`Driver.Attach(ctx, session, window)`:

1. `checkSessionName` — reject a name that tmux would rewrite (fail before touching anything).
2. `has-session -t '=<session>'` → absent means `ErrSessionNotFound`; the CLI can then say "this
   scenario has no tmux session" instead of tmux's `error connecting to …`.
3. `select-window -t '=<session>:=<device>'` → absent means `ErrWindowNotFound`, **before** the
   terminal is handed over. Selecting first is what makes the attach land on the requested device
   rather than on whatever was last active.
4. Hand over the terminal, per quirk 13. On Unix that is `syscall.Exec`: tmux *replaces* the
   kathara process, so there is no proxy in between to mangle SIGWINCH, signals or the exit status,
   and no second process for the user to wonder about. `AttachArgs` is exported so the argv can be
   asserted without a TTY.

Detach (prefix-d, or `DetachSession`) ends the client only; the session, its windows, their
`kathara connect` processes and the containers behind them keep running, and a later invocation
reattaches to the same session. *`TestAttachSelectAndDetach` proves the whole loop for real:
helper process on a pty → client attached → correct window active → detach → helper exits 0 →
session and all three windows still there → second helper reattaches to a different device.*

`--tmux` is **new CLI surface** — 3.8.3's `connect` always runs in the caller's terminal (see
OI-2).

---

## 6. What this replaces on the Python side

### `trdparty/libtmux/tmux.py` (60 SLOC) — REPLACED, per PACKAGE_GRAPH

A `TMUX` singleton over the third-party `libtmux` package (a pip dependency that parses tmux's
output — the `decode()` bug class §3.3 calls out).

| 3.8.3 behavior | here |
|---|---|
| `session_name = "Kathara" if not session_name` — **every path-parsed lab shares one session** | one session per scenario (§3, OQ-9 ruling) |
| Creates the session around a random placeholder window (`"%008x" % random.getrandbits(32)`), then kills the placeholder if it created the session | the first device *is* the first window; no placeholder, so no window-count skew and no kill-the-placeholder step |
| Duplicate-session handling: `except TmuxSessionExists`, plus `except LibTmuxException` sniffing `'duplicate session' in e.args[0][0]`; a `LibTmuxException` that is *not* a duplicate falls out of the handler with no `return`, so the swallowed error surfaces as `libtmux.exc.ObjectDoesNotExist: No objects found: session_name='…'` from the `self._server.sessions.get(...)` below (reproduced against 3.8.3 + libtmux 0.62.0; an earlier draft of this table said `UnboundLocalError`, which cannot happen — that line binds `session`) | one whitelist entry (`duplicate session:`) → "attach, do not clobber"; anything else is returned as a typed error |
| `_get_session_from_server` may return `None`, which is then cached in `self._sessions` | no session cache; tmux is the only state |
| Per-process session cache in a singleton | stateless `Driver` value (§0.2 #10 direction) |
| `kill_window(session, window_name)` static helper | `KillWindow(ctx, session, window)` |

### `cli/ui/utils.py::open_machine_terminal` + `cli/ui/event/HandleMachineTerminal.py`

- Builds `"<executable> connect [-v] -l <device>"` and runs it in the window with
  `cwd=lab.fs_path()`; on macOS it wraps that as `cd "<path>" && clear && <cmd> && exit`. The
  command string and cwd become `Window.Command` / `Window.Dir` — Kathara's business, not this
  package's.
- The `TMUX` branch exists on Linux and macOS only; Windows has no tmux path at all. Parity: the
  Go tmux backend is Unix-only in practice (`attach_other.go` exists but cannot `execve`, and tmux
  on Windows only exists under WSL/Cygwin).
- `HandleMachineTerminal` opens `get_num_terms()` terminals per device. Under TMUX this is
  already idempotent in 3.8.3 (the second call finds the existing window), i.e. `num_terms > 1`
  is a silent no-op with TMUX. `EnsureWindow` preserves that exactly (`WindowExisted`).

### `setting/Setting.py::check_terminal`

Returns `True` immediately when `terminal == "TMUX"` — **tmux's presence is never checked**, so a
missing binary surfaces as a `libtmux` traceback at deploy time. `Driver.Available(ctx)` /
`Version(ctx)` give the CLI a cheap pre-flight (`tmux -V`, no server contacted). What error that
should map to is OI-3.

### Recorded divergence (approved, §0.2 #2)

> **Shared "Kathara" tmux session → one session per network scenario.** In 3.8.3 every scenario
> opened from a path shares a single tmux session named `Kathara`; devices from unrelated
> scenarios end up as sibling windows in it, and killing that session kills all of them. Kathara-Go
> creates `kathara_<name-or-hash>` per scenario (§3). Approved under spec §0.2 #2 / §3.3 ("one
> session per network scenario named from the lab"); OQ-9.

Copied into `DIVERGENCES.md` as entry 18 in the Phase 3 review, which is where `term/tmuxdrv`
lands.

---

## 7. Tests

```
go test ./term/tmuxdrv/            # ~0.6 s, drives real tmux
go test ./term/tmuxdrv/ -race      # ~1.5 s
```

Plain `go test`, no build tag: the suite **skips** (`t.Skip`) when `tmux` is not on `PATH`, and
the attach test additionally skips without `script(1)`. Isolation rules, both mandatory:

- **`-S <t.TempDir()>/tmux.sock`** — a private server per test, killed in `t.Cleanup`. The suite
  must never touch the developer's or CI agent's tmux server; this VM's agent shell is itself
  inside tmux on the `default` socket. `-S` is chosen over `-L` because tmux never unlinks a
  socket (quirk 3b), so `-L` would leave a dead file in `/tmp/tmux-<uid>` per test per run, while
  a socket inside `t.TempDir()` goes away with the directory. Verified: a full run leaves
  `/tmp/tmux-0` containing only the pre-existing `default` socket.
- **`-f /dev/null`** — the user's `~/.tmux.conf` would otherwise move window indices
  (`base-index`), rename windows (`automatic-rename`) or replace the default command.

A hard-killed test binary (SIGKILL, `go test -timeout` panic) skips cleanup and leaves a live
server holding `sleep 300` windows; it self-heals in ≤5 minutes when the sleeps exit and the last
window closes. `pkill -f tmuxdrv.test` plus `tmux -S <path> kill-server` clears it immediately.

Attach is exercised for real, not mocked: `Attach` replaces the process image, so the test
re-executes **the test binary itself** under `script`, with `TestMain` dispatching to an attach
helper when `KATHARA_TMUXDRV_ATTACH_HELPER` is set.

Result on this VM (tmux 3.5a):

```
--- PASS: TestVersion                             tmux version under test: tmux 3.5a
--- PASS: TestEnsureSessionLifecycle              create 3 windows, verify, re-ensure (no clobber), +1 device, kill, verify gone
--- PASS: TestExactMatchTargeting
--- PASS: TestAbsentServerIsNotAnError
--- PASS: TestWindowOptionsAndDeath               -c / -e / window death / remain-on-exit
--- PASS: TestAttachSelectAndDetach               pty attach, select, detach, reattach (both exec paths, both asserted attached)
--- PASS: TestAttachSwitchesClientOnSameServer    switch-client path: the existing client moves, no second client
--- PASS: TestAbsenceClassifierSpellings          duplicate session / no current client / a real failure stays a failure
--- PASS: TestForeignSessionNameCannotForgeARow   a newline in someone else's session name cannot forge a listing row
--- PASS: TestWindowCommandTrailingSemicolon      a trailing ';' in a device command survives tmux's sequence parser
--- PASS: TestAttachMissingWindow
--- PASS: TestSanitizedNameRoundTrip
--- PASS: TestSessionName + 10 subtests, TestCheckNames, TestArgvComposition,
          TestTargetsAreExactMatch, TestDriverValidateRejectsTwoSockets,
          TestWindowCommandArgsAreGuarded, TestOuterSocketPath, TestEnvironWithoutTmux
ok  github.com/KatharaFramework/kathara-go/term/tmuxdrv
```

Phase 3 added the four tests in the middle of that list, all mutation-checked: breaking the
different-server exec branch, or the same-server `switch-client` branch, fails
`TestAttachSelectAndDetach` and `TestAttachSwitchesClientOnSameServer` respectively (before the
review, the reattach leg passed with the exec branch replaced by `return fmt.Errorf(...)`, because
`Attach` selects the window *before* it hands the terminal over and nothing asserted a client had
appeared).

`go build ./...`, `go vet`, `errcheck`, `staticcheck`, `gofmt -l`: clean.

---

## 8. Open items for Phase 3

| # | Item | Why it needs a decision |
|---|---|---|
| **OI-1** | **Package placement — still open after the Phase 3 review; both reviewers raised it, one as a BLOCKER.** PACKAGE_GRAPH.md maps `trdparty/libtmux/tmux.py` → `term/tmux.go` (a file in package `term`); this spike landed in `term/tmuxdrv/`, a new leaf package the frozen inventory does not list, and `term/pty.go`'s package doc already points at it. It imports nothing from the module, so it cannot create a cycle, and keeping it dependency-free is what lets it be tested without `kathara`/`settings`. Needs either a graph amendment (add `term/tmuxdrv`, imports: none) or a fold-in to `term/tmux.go`. Frozen doc → human ruling; the fixer pass deliberately did **not** move code or edit the frozen graph. Registered in PROPOSED-DIVERGENCES.md so it cannot land silently. |
| **OI-2** | **`connect --tmux` is new CLI surface.** 3.8.3's `connect` has no such flag (`-d/--directory`, `-v/--vmachine`, `--shell`, `-l/--logs`, `DEVICE_NAME`). §3.3 mandates it; CLI_SURFACE.md is frozen and does not list it. Needs an amendment, plus a decision on what it does when the scenario has no tmux session (error, or create-and-attach). |
| **OI-3** | **Error mapping for a missing/broken tmux.** ERROR_CODES.md sends terminal-internal sites to `InternalError`, and `check_terminal` skips the check for TMUX entirely, so 3.8.3 has no precedent. Recommendation: map "tmux binary not found" to `Settings` with the existing reason string `Terminal Emulator \`{terminal}\` not valid! Install it before using it.` (the closest existing user-facing message), and everything else in `tmuxdrv` to `InternalError`. Contract-touching → human ruling. |
| **OI-4** | **`remain-on-exit` default.** Off = 3.8.3 parity and a device whose connect fails vanishes silently. On = the failure stays on screen with its exit status, at the cost of dead windows the user must close. The mechanism is race-free and free (quirk 8); only the default is open. |
| **OI-5** | **Session-name readability.** Path-parsed scenarios get `kathara_<22-char hash>` in `tmux ls`. A decorated form (`kathara_<dir basename>_<hash>`) stays collision-free and deterministic but needs the path passed into `SessionName`. Cosmetic; decide before the name becomes user-visible API. |
| **OI-6** | **Session teardown ownership.** Nothing calls `KillSession` yet. 3.8.3 never removed tmux sessions; with per-scenario sessions, a scenario's session empties itself when `lclean` kills the containers (each `kathara connect` exits → window closes → last window closes the session). Decide whether `lclean`/`lrestart` should kill it explicitly instead of relying on that cascade. |
| **OI-7** | **Multi-client policy.** Two users can attach to the same scenario session and share one view (tmux default), and `DetachSession` drops both (quirk 15). Untouched by this spike. |
| **OI-8** | **tmux version floor: declare it, or check it.** Effective floor is 2.1 (2015) unless `Window.Env` is used, which pulls it to 3.2 (§1). Decide whether `Available()` should parse `tmux -V` and refuse below the floor with a clear message, or whether the floor is documentation only. Parsing `tmux -V` is the one place a version string would be interpreted, so it needs a ruling on failure behavior for unparsable/`master` builds. |
