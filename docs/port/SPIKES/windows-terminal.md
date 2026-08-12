# SPIKE: Windows terminal + ConPTY

**Phase 2 dependency spike** for the PORT_SPEC §0.2 #2 rebuild's Windows leg. Status: design
complete, ConPTY compile-proof landed and cross-compiling clean (no Windows runtime in this
environment — nothing here has executed on Windows). Not review-complete — this code still gets
the full §10 adversarial pass in Phase 3, and everything Windows-runtime-dependent is flagged
for Phase 6 verification on a real runner.

Code: `term/pty.go` (interface), `term/pty_unix.go` + `term/pty_linux.go` + `term/pty_darwin.go`
(creack/pty leg), `term/conpty_windows.go` (ConPTY leg), tests `term/pty_unix_test.go`.

Inputs conformed to: PACKAGE_GRAPH (dependency pins, D-5), ERROR_CODES.md (no new public error
codes introduced; see §11 item W6-11), analysis/manager-foundation.md §§1.7–1.10 + OQ-18/19/20,
analysis/docker-backend.md §§1.9–1.11, PORT_SPEC §12 risk 6.

---

## 1. Where ConPTY actually sits (scope clarification)

The Python code conflates transport and rendering (PACKAGE_GRAPH D-5). Splitting them shows
ConPTY is needed in exactly **one** place, and it is *not* `kathara connect`:

| Path | Local terminal handling | Device transport | ConPTY? |
|---|---|---|---|
| `kathara connect <dev>` (plain) | raw console mode + byte pump (`term/runner.go`, `term/console_*.go`) | `backend/docker/tty_windows.go` npipe attach (§8) | **No** |
| Built-in multiplexer pane (§3.3 item 1) | bubbletea renders; each pane hosts a local child process on a `term.Pty` | the child runs the connect path itself | **Yes — this is the ConPTY consumer** |
| External emulator opt-in (`cmd /c start …`) | new OS console window; child does plain connect | as above | No |
| tmux backend | see SPIKES/tmux.md (Unix-only; tmux does not exist on Windows) | as above | No |

So the spike's `Pty` seam is the mux's pane engine: `creack/pty` on Unix, ConPTY on Windows,
one interface. `connect` itself needs only console-mode handling (§5), stdin pumping (§6),
resize watching (§7) and the npipe transport (§8).

A useful consequence: a pane child running `kathara connect` under ConPTY detects pane resizes
through its *own* console (the ConPTY), using the same §7 polling it uses standalone —
`ResizePseudoConsole` updates the ConPTY screen-buffer size, so no extra resize plumbing is
needed between mux and child.

---

## 2. `golang.org/x/sys/windows` surface audit (v0.47.0, the PACKAGE_GRAPH pin)

Checked via pkg.go.dev and then verified against the downloaded module source
(`/root/go/pkg/mod/golang.org/x/sys@v0.47.0/windows`), which is authoritative. **Everything the
design needs is exported; no `NewLazySystemDLL` fallback is required.**

### 2.1 Present (signatures from source)

| Binding | Signature / value | File |
|---|---|---|
| `CreatePseudoConsole` | `(size Coord, in Handle, out Handle, flags uint32, pconsole *Handle) error` | syscall_windows.go:1871 (hand-written wrapper: `Coord` is passed packed) |
| `ResizePseudoConsole` | `(pconsole Handle, size Coord) error` | syscall_windows.go:1878 |
| `ClosePseudoConsole` | `(console Handle)` — **no error return** | zsyscall_windows.go:1833 |
| `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` | `0x00020016` | types_windows.go:268 |
| `PSEUDOCONSOLE_INHERIT_CURSOR` | `0x1` | types_windows.go:2513 |
| `NewProcThreadAttributeList` | `(maxAttrCount uint32) (*ProcThreadAttributeListContainer, error)` | exec_windows.go:209 |
| `(*ProcThreadAttributeListContainer).Update` | `(attribute uintptr, value unsafe.Pointer, size uintptr) error` — forwards `value` **verbatim** as `lpValue` (see §4.2) | exec_windows.go:232 |
| `(*ProcThreadAttributeListContainer).List/Delete` | | exec_windows.go:238,246 |
| `StartupInfoEx` | `struct { StartupInfo; ProcThreadAttributeList *ProcThreadAttributeList }` | types_windows.go:950 |
| `CreateProcess` | takes `*StartupInfo`; `&si.StartupInfo` of a `StartupInfoEx` works because the embed is field 0 | zsyscall_windows.go:1955 |
| `EXTENDED_STARTUPINFO_PRESENT`, `CREATE_UNICODE_ENVIRONMENT` | | types_windows.go |
| `CreatePipe`, `CloseHandle`, `TerminateProcess`, `GetExitCodeProcess`, `WaitForSingleObject` | | zsyscall_windows.go |
| `GetConsoleMode` / `SetConsoleMode` | `(console Handle, mode *uint32)` / `(console Handle, mode uint32)` | zsyscall_windows.go:2321,3252 |
| `GetConsoleScreenBufferInfo` | `(console Handle, info *ConsoleScreenBufferInfo) error` | zsyscall_windows.go:2338 |
| `ENABLE_ECHO_INPUT 0x4`, `ENABLE_LINE_INPUT 0x2`, `ENABLE_PROCESSED_INPUT 0x1`, `ENABLE_WINDOW_INPUT 0x8`, `ENABLE_VIRTUAL_TERMINAL_INPUT 0x200` | input modes | types_windows.go:2492-2501 |
| `ENABLE_VIRTUAL_TERMINAL_PROCESSING 0x4`, `DISABLE_NEWLINE_AUTO_RETURN 0x8` | output modes | types_windows.go:2505-2506 |
| `GetStdHandle`, `STD_INPUT_HANDLE`, `STD_OUTPUT_HANDLE` | | — |
| `ComposeCommandLine([]string) string` | inverse of `CommandLineToArgv`, correct Windows quoting | exec_windows.go:84 |
| `UTF16FromString` / `UTF16PtrFromString` | reject interior NUL | — |
| `GenerateConsoleCtrlEvent`, `CTRL_C_EVENT 0`, `CTRL_BREAK_EVENT 1` | | syscall_windows.go:359, types_windows.go:228 |

### 2.2 Absent, and the fallback that is NOT needed

`SetConsoleCtrlHandler` is **not** exported by x/sys/windows v0.47.0 (the only console-ctrl
surface is `GenerateConsoleCtrlEvent`). This does not matter: the Go runtime installs its own
console ctrl handler and delivers events through `os/signal` — `CTRL_C_EVENT` /
`CTRL_BREAK_EVENT` as `os.Interrupt`, `CTRL_CLOSE_EVENT` / `CTRL_LOGOFF_EVENT` /
`CTRL_SHUTDOWN_EVENT` as `syscall.SIGTERM` (documented in `os/signal`; Windows gives the
process ~5 s on close, ample for a `SetConsoleMode` restore). §5.2 builds on that.

**Contingency spec** (only if a future x/sys regression or an unexported API is ever needed):

```go
var (
    kernel32              = syscall.NewLazySystemDLL("kernel32.dll") // System DLL search only — no cwd DLL planting
    procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)
// r1, _, e1 := procSetConsoleCtrlHandler.Call(handlerCallback, 1)
// handlerCallback = syscall.NewCallback(func(ctrlType uint32) uintptr {...})
```

Rules if this path is ever taken: `NewLazySystemDLL` (never `NewLazyDLL`), one package-level
`LazyProc` per symbol, `syscall.NewCallback` callbacks must never grow the stack unboundedly and
must not panic across the callback boundary, and each such symbol gets a comment naming the
first Windows build that ships it. ConPTY itself needs none of this — `kernel32`'s
`CreatePseudoConsole` family ships since Windows 10 1809 (build 17763), which becomes the
rebuild's hard floor (matching Python 3.8.3's practical floor for VT support on Windows).

---

## 3. The `Pty` seam (compile-proof, landed)

```go
// Winsize is a terminal geometry in character cells. Cols-first, matching the
// Python ITerminalSession.resize(cols, rows) argument order.
type Winsize struct {
    Cols uint16
    Rows uint16
}

// Pty is one local pseudo-terminal hosting one child process.
type Pty interface {
    Start(cmd *exec.Cmd) error // exactly once; wires child stdio (and console on Windows)
    Resize(ws Winsize) error   // pre-Start: sets creation size; post-Start: live resize
    io.Reader                  // child output; io.EOF after exit+drain on all platforms
    io.Writer                  // terminal input to the child
    io.Closer                  // idempotent; does not kill the child on Unix (see §4.4 for Windows)
}

func New(ws Winsize) (Pty, error) // zero dims default to 80×24 (ConPTY rejects 0×0)
```

| File | Constraint | Content |
|---|---|---|
| `term/pty.go` | none | interface, `Winsize`, `New`, lifecycle errors |
| `term/pty_unix.go` | `linux \|\| darwin` | `unixPty` over `creack/pty` v1.1.24: `pty.StartWithSize` (size applied before the child runs), `pty.Setsize`, Linux `EIO`→`io.EOF` normalization |
| `term/pty_linux.go`, `term/pty_darwin.go` | filename tags | per-OS constructors delegating to `unixPty` — the seam where genuinely divergent per-OS behaviour lands in Phase 6 without touching the shared code |
| `term/conpty_windows.go` | filename tag | `conPty`, §4 |
| `term/pty_unix_test.go` | `linux \|\| darwin` | echo, initial-size (`stty size` sees `42 101`), resize-then-observe, lifecycle errors, default size — all pass, `-race` clean |

Deliberately absent from the spike surface (Phase 6): `Wait`/exit-code access beyond
`cmd.Process`, ConPTY child-handle retention, bubbletea, console modes, the runner.

---

## 4. ConPTY implementation design (`term/conpty_windows.go`)

### 4.1 Creation sequence (as coded)

```
CreatePipe ×2:      parent Write → inW ═══ inR  → ConPTY input
                    parent Read  ← outR ═══ outW ← ConPTY output (always a VT byte stream)
CreatePseudoConsole(Coord{Cols,Rows}, inR, outW, 0, &hpc)   // sized at creation — no initial-resize race
CloseHandle(inR, outW)                                       // ConPTY holds duplicates
NewProcThreadAttributeList(1); Update(PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, hpc-as-lpValue, sizeof(hpc))
StartupInfoEx{Cb: sizeof, ProcThreadAttributeList: attrs.List()}
CreateProcess(appName=cmd.Path, ComposeCommandLine(cmd.Args), inheritHandles=false,
              EXTENDED_STARTUPINFO_PRESENT|CREATE_UNICODE_ENVIRONMENT, envBlock(cmd.Env), cmd.Dir,
              &si.StartupInfo, &pi)
CloseHandle(pi.Thread); cmd.Process = os.FindProcess(pi.ProcessId); CloseHandle(pi.Process)
```

Notes wired into the code: `flags=0` — **not** `PSEUDOCONSOLE_INHERIT_CURSOR`, because that flag
makes conhost interrogate the *hosting* terminal with a DSR (`ESC[6n`) on the output pipe and
expect the reply on the input pipe; the mux is not a real terminal answering DSRs, so panes
would hang on spawn. `inheritHandles=false` and no `STARTF_USESTDHANDLES`: the pseudoconsole
supplies the child's console handles and nothing else may leak in. `cmd.Env == nil` ⇒ `envp=nil`
(inherit), the os/exec convention; the env block is UTF-16, double-NUL-terminated, built with
`UTF16FromString` so interior NULs error instead of truncating.

### 4.2 The HPCON-as-lpValue subtlety

The Win32 contract for `PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE` is that `lpValue` **is the HPCON
value itself**, not a pointer to it (the MS sample passes `hPC` in the pointer parameter).
x/sys's `Update` forwards `value unsafe.Pointer` verbatim as `lpValue`, so the handle must be
reinterpreted as a pointer. A direct `unsafe.Pointer(uintptr(hpc))` is exactly what `go vet`'s
`unsafeptr` check exists to reject, so the code launders it:

```go
func handleAsPointer(h windows.Handle) unsafe.Pointer {
    return *(*unsafe.Pointer)(unsafe.Pointer(&h)) // bits only; never dereferenced by Go
}
```

`GOOS=windows go vet` passes (§10). If a future vet version tightens on this pattern, the
alternative is a small `//go:nosplit`-free wrapper in an internal package with the check
disabled for that one file — record in Phase 3 review if it comes up.

### 4.3 The os/exec gap

`os/exec` has no hook for pseudoconsole attributes (`SysProcAttr` carries none; the upstream
proposal for ConPTY in os/exec is still open), so `Start` calls `windows.CreateProcess`
directly. Consequences, deliberate and documented on the interface:

- `cmd.Process` **is** set (via `os.FindProcess(pid)`), so `cmd.Process.Kill()` and
  `cmd.Process.Wait()` work.
- `cmd.Wait()` does **not** work on Windows (os/exec never observed a Start; it errors with
  "not started"). Unix callers get a working `cmd.Wait` because `pty.Start` uses `cmd.Start`
  underneath. The runner/mux must therefore standardize on `cmd.Process.Wait()` — Phase 6 item
  W6-7 decides whether to fold a `Wait() (exitCode int, err error)` into the `Pty` interface to
  erase the asymmetry.
- `cmd.Stdin/Stdout/Stderr/ExtraFiles` are ignored on Windows (the ConPTY owns child stdio).
  W6-7 adds validation that errors when they are set, instead of silently ignoring.

### 4.4 Teardown ordering, the drain rule, and EOF semantics

`Close()` does: `ClosePseudoConsole(hpc)` → close `inW` → close `outR` (idempotent, mutex-held).
The ordering interacts with two known ConPTY behaviours that cannot be unit-tested off-Windows:

1. **Console-attached children die with the pseudoconsole.** Closing the ConPTY disconnects the
   child's console, which normally terminates console clients (as if their terminal window
   closed). This is a real platform difference from Unix, where `Close` only drops the master
   and the child keeps running. Recorded on the `Pty` doc comment; the mux must treat "close
   pane" as "session over" on both platforms anyway, so no behavioural divergence surfaces —
   but "detach leaving the child running" is **not** implementable with one ConPTY per pane,
   which is fine because detach in the rebuild means *containers* keep running (the pane child
   is just a connect client, cheap to respawn on reattach).
2. **Drain discipline.** On old Windows 10 builds (pre-2004) `ClosePseudoConsole` can block if
   the output pipe has undrained data. The runner discipline that avoids it on every build:
   the pane pump reads `outR` continuously for the whole session; teardown is
   *child exit observed* → `Close()` → the pending `Read` returns — after
   `ClosePseudoConsole` drops conhost's duplicate of `outW`, the pipe write side is fully
   closed and `ReadFile` returns `ERROR_BROKEN_PIPE`, which `os.File` maps to `io.EOF`. So
   both legs end streams with `io.EOF` (Unix via the `EIO` normalization in `pty_unix.go`).

### 4.5 Known limitation: blocked `Read` vs `Close` (W6-3)

The parent pipe ends are anonymous pipes wrapped in `os.NewFile` — not pollable by the Go
runtime, so a `Read` blocked in synchronous `ReadFile` is **not** unblocked by `outR.Close()`
alone; it returns only once the write side fully closes (child exit + `ClosePseudoConsole`,
§4.4), and `CancelIoEx` needs the read to be issued and cancelled on a known thread. For the
mux (close pane ⇒ session over ⇒ child exits) the §4.4 ordering makes this a non-issue, but a
wedged child could pin a pump goroutine. Phase 6 item W6-3: replace the parent ends with a
named-pipe pair created `FILE_FLAG_OVERLAPPED` (the go-winio pattern), making reads
deadline-able and cancellable. This is the single biggest correctness gap between the spike and
production code.

---

## 5. Console mode save/restore (PORT_SPEC §12 risk 6)

For `kathara connect` and the mux shell — the *hosting* console, not the ConPTY.

### 5.1 Target modes

Input handle (`STD_INPUT_HANDLE`): exactly what `golang.org/x/term` v0.45.0 `MakeRaw` already
does on Windows (verified in `term_windows.go`):

```
raw = orig &^ (ENABLE_ECHO_INPUT | ENABLE_PROCESSED_INPUT | ENABLE_LINE_INPUT)
raw |= ENABLE_VIRTUAL_TERMINAL_INPUT
```

The cleared bits are the same three Python's `WindowsConsoleAdapter.enter_raw` clears; the
**added** `ENABLE_VIRTUAL_TERMINAL_INPUT` is the rebuild's improvement — the console itself
translates keys to VT sequences, deleting Python's hand-rolled `KEYCODES` virtual-key table
(§6, §9). So: use `term.MakeRaw(stdin)` / `term.Restore` for input, no bespoke code.

Output handle (`STD_OUTPUT_HANDLE`) — x/term does not touch it, so `term/console_windows.go`
adds:

```
out = orig | ENABLE_VIRTUAL_TERMINAL_PROCESSING | DISABLE_NEWLINE_AUTO_RETURN
```

(`ENABLE_PROCESSED_OUTPUT` stays set — VT processing requires it.) Output is then written with
plain `os.Stdout.Write` (`WriteFile`), a raw byte stream end-to-end. This deletes Python's
`WriteConsoleW` path and with it OQ-19's extra-NUL bug and OQ-20's U+FFFD chunk-boundary
mangling (§9). Both original modes are saved before any change; failure to get either mode ⇒
not a console ⇒ refuse interactive connect with the existing "not a TTY" behaviour rather than
Python's silent read-nothing-forever hang (manager-foundation §1.10 `enter_raw` failure note —
a recorded fix, §9 row 5).

### 5.2 Restore-on-all-exits pattern (explicit, the §12.6 mitigation)

One idempotent restore closure, registered on **every** exit path:

```go
// term/console_windows.go (console_unix.go is isomorphic with termios)
func EnterRaw() (restore func(), err error) {
    inSt, err := term.MakeRaw(int(windows.GetStdHandle(...)))   // input modes, saved orig
    ...                                                          // output modes, saved orig
    var once sync.Once
    return func() {
        once.Do(func() {
            _ = windows.SetConsoleMode(outH, origOut)  // best-effort, error-swallowing:
            _ = term.Restore(stdinFd, inSt)            // restore must never abort restore
        })
    }, nil
}
```

Registration discipline (all four, every time — the pattern the Phase 3 reviewer enforces):

1. **Top-frame defer**: `restore, err := term.EnterRaw(); if err != nil {...}; defer restore()`
   in the connect command's Run function. Covers normal return **and panics** (Go unwinds
   defers on panic — this is the backstop that Python's `finally` provided).
2. **Signal path**: `signal.Notify(ch, os.Interrupt, syscall.SIGTERM)` before raw mode; the
   watcher goroutine calls `restore()` then re-raises the default behaviour. On Windows this
   covers Ctrl+Break and console-close/logoff/shutdown (delivered as SIGTERM, §2.2); on Unix,
   SIGINT/SIGTERM/SIGHUP. Note Ctrl+C itself does *not* arrive as a signal during a session —
   raw mode cleared `ENABLE_PROCESSED_INPUT` (Windows) / ISIG (Unix), so 0x03 flows to the
   device shell as bytes, identical to Python.
3. **Cleanup ordering**: the runner's teardown runs each step isolated (swallow-and-continue,
   Python parity) with `restore()` **last** — session close first, restore after, so a failed
   session close cannot skip restore (manager-foundation §1.8 `_cleanup` order). Unlike
   Python, a *hang* in session close no longer leaves the shell raw: the top-frame defer and
   signal path still fire, and `restore` is `sync.Once`-safe against all three racing.
4. **No `os.Exit` between EnterRaw and restore.** Defers do not run on `os.Exit`; the CLI's
   exit-code path must unwind normally out of the command. Reviewer rule for Phase 6, plus a
   grep-able comment on `EnterRaw`.

### 5.3 Test plan (risk 6 demands all three platforms)

- Unix (CI now): spawn `kathara connect` under a test pty; kill it mid-session with SIGTERM /
  SIGKILL / a forced panic; parent asserts `tcgetattr` equals the pre-spawn state (SIGKILL case
  documents the accepted loss: no process can restore after SIGKILL — same as Python).
- Windows (Phase 6 CI, W6-9): same shape under a ConPTY harness — which this spike's
  `conPty` provides: the test *host* creates a `conPty`, runs `kathara connect` inside it,
  terminates it, then checks `GetConsoleMode` restoration from a sibling probe process.
  ConPTY is thus both a product feature and the Windows test rig for §12.6.

---

## 6. Stdin raw handling and the input pipeline

With `ENABLE_VIRTUAL_TERMINAL_INPUT` set, `ReadFile` on the console input handle yields UTF-8
VT byte sequences directly (arrows as `ESC[A…`, etc.). The connect runner's input pump is then
platform-uniform: one goroutine looping `os.Stdin.Read(buf 4096)` → `session.Write` /
`Pty.Write`, empty-read or error ⇒ close (EOF-on-empty-read parity, manager-foundation §1.8).
Python's Windows path instead ran `PeekConsoleInputW`+`ReadConsoleInputW` on a 10 ms poll,
translating virtual keys through the `KEYCODES` table — all deleted (§9 rows 3–4).

**Blocked-read teardown**: a goroutine parked in synchronous console `ReadFile` cannot be
cleanly cancelled without `CancelSynchronousIo` + `LockOSThread` bookkeeping. Policy, matching
Python (whose executor thread also just died with the process): in `connect` the pump goroutine
may outlive the session and exit with the process — acceptable because connect is
process-per-session. It is **not** acceptable inside the long-lived mux, where stdin is owned
by bubbletea (which has its own Windows input machinery) and our pump does not exist. The
spec's `goleak` merge gate needs a documented exemption for the connect pump (W6-12).

---

## 7. Resize events without SIGWINCH

**Chosen: poll `GetConsoleScreenBufferInfo` on the output handle.** Watcher goroutine, 250 ms
tick: compute `cols = srWindow.Right-Left+1`, `rows = srWindow.Bottom-Top+1` (window rect, not
buffer size — the buffer is the scrollback), compare with last, on change call
`onResize(cols, rows)` → `session.Resize` (docker exec-resize API, §8). **Emit once
immediately at start**, before the pumps — making Windows symmetric with the Unix adapter's
initial emission, which manager-foundation §1.9/OQ-18 records as load-bearing (remote TTY gets
correct dimensions before I/O). That asymmetry was OQ-19's "no initial resize" quirk; fixed,
recorded in §9 row 2.

Why not `ReadConsoleInput` + `WINDOW_BUFFER_SIZE_EVENT` (the Python design): the console input
handle has **one** ordered event stream. Consuming records via `ReadConsoleInputW` to sniff
resize events competes destructively with the VT-input `ReadFile` pump for key data — you get
either translated VT bytes *or* raw records, not both (this is precisely why Python, having
chosen records, had to hand-translate keys). Peeking doesn't help: `PeekConsoleInput` cannot
selectively consume the resize record ahead of interleaved key records without racing the
reader. Polling costs one cheap syscall per tick, needs no second console consumer, works
identically when the process runs nested under the mux's ConPTY (§1), and degrades to a no-op
when stdout is redirected (not a console ⇒ no watcher, connect already refused raw mode, §5.1).

Unix counterpart for symmetry (`term/console_unix.go`, Phase 6): `signal.Notify(SIGWINCH)` +
initial emission — the poll ticker is Windows-only.

---

## 8. Docker attach transport on Windows (PACKAGE_GRAPH D-5)

Lives in `backend/docker/tty_windows.go` implementing `kathara.TTYSession`; `term/` never
imports docker. The Go SDK collapses most of Python's Windows-specific machinery:

- **Dial**: the moby client with `npipe://./pipe/docker_engine` uses `go-winio` v0.6.2 (the
  PACKAGE_GRAPH pin, also docker's transitive dep) as its named-pipe dialer. No code of ours
  touches win32 pipe APIs — unlike docker-py, where the npipe socket lacked a usable fileno and
  forced the `DockerNPipeSession` class.
- **Session**: `client.ContainerExecAttach` returns `types.HijackedResponse{Conn net.Conn,
  Reader *bufio.Reader}` on **all** platforms — on Windows `Conn` is a winio pipe conn. So
  `tty_windows.go` and `tty_unix.go` share shape: `Read` from `Reader`, `Write` to `Conn`,
  `Resize(cols, rows)` via `client.ContainerExecResize(ctx, execID, ResizeOptions{Height: rows,
  Width: cols})` (resize goes through the API, not the stream — same as Python; note the
  cols/rows→Width/Height swap, the same axis-swap trap `Winsize` documents), `Close` via
  `resp.Close()` (which replaces Python's reach into `handler._response.close()` internals).
- **Blocking model**: Python set `PIPE_NOWAIT` and spun on `ERROR_NO_DATA`(232)/`ERROR_PIPE_NOT_
  CONNECTED`(233)→`b""` with a 30 ms asyncio sleep. Go uses blocking reads on the conn;
  winio conns support `SetReadDeadline`, giving the runner clean cancellation Python never had.
  The 232/233-swallowing and the empty-read-means-no-data (vs EOF!) asymmetry of the Python
  threaded path both disappear: end of stream is `io.EOF`, uniformly (§9 row 6).
- **Half-close**: interactive connect never needs `CloseWrite`; whether the winio conn
  supports it is irrelevant here but matters for future exec-with-stdin streaming — open
  question OQ-W1, verify on a real daemon in Phase 6 (W6-6).
- **TTY framing**: with `Tty: true` the exec stream is unmultiplexed raw bytes (no 8-byte
  stdcopy headers) — same on Windows; no demux in the session.

---

## 9. Recorded divergences (the rebuild fixes; OQ-18/19/20 of manager-foundation)

Per PACKAGE_GRAPH ("recorded divergences per OQ-19, not bug-compat requirements") these are
**intentional behaviour changes**, to be copied into DIVERGENCES.md when Phase 6 lands the
runner (W6-10). None are observable by golden tests on Linux CI; all are user-visible on
Windows.

| # | Python 3.8.3 behaviour | Go rebuild behaviour |
|---|---|---|
| 1 | `WriteConsoleW` writes `len(buffer)` incl. terminating NUL ⇒ one extra U+0000 per output chunk (OQ-19) | raw `WriteFile` byte stream; no NUL |
| 2 | No initial resize emission on Windows — remote TTY keeps wrong size until the user resizes (OQ-18/19) | initial size emitted at session start on all platforms (§7) |
| 3 | Output decoded UTF-8 `errors="replace"` per 4096-byte chunk ⇒ U+FFFD at split multi-byte sequences (OQ-20) | byte stream end-to-end; no decode, no replacement |
| 4 | Keys translated via hand-rolled `KEYCODES` VK table: F1–F4 sent as legacy xterm `ESC[11~`…`ESC[14~` | console VT input: conhost emits `ESC OP`…`ESC OS` (SS3) for F1–F4, `ESC[15~`+ for F5+. Modern-xterm-correct; byte-level difference a device-side raw reader could observe — test on real devices in W6-9 |
| 5 | `GetConsoleMode` failure in `enter_raw` silently returns ⇒ session reads nothing forever | not-a-console detected up front ⇒ clear error, no hang (§5.1) |
| 6 | npipe empty read means "no data yet" (30 ms sleep), EOF signalled by exception — opposite of the Unix fd path | `io.EOF` means end-of-stream, uniformly, both platforms (§8) |
| 7 | Raw-mode restore skipped if session close *hangs* (restore is last in a single-threaded cleanup) | restore additionally guaranteed by top-frame defer + signal watcher, `sync.Once`-idempotent (§5.2) |

---

## 10. Compile-proof transcript

Commands run in this environment (Go 1.26.5 linux/amd64; `errcheck`/`staticcheck` from
`/root/go/bin`); deps added at the PACKAGE_GRAPH pins: `creack/pty v1.1.24`,
`golang.org/x/sys v0.47.0`, `golang.org/x/term v0.45.0` (x/term unused by the spike code but
pinned now for §5.1).

```
$ go build ./term/...                                  # native linux/amd64     OK
$ GOOS=windows GOARCH=amd64 go build ./term/...        #                        OK
$ GOOS=darwin  GOARCH=arm64 go build ./term/...        #                        OK
$ go vet ./term/...                                    # native                 OK
$ GOOS=windows GOARCH=amd64 go vet ./term/...          #                        OK  (unsafeptr clean, §4.2)
$ GOOS=darwin  GOARCH=arm64 go vet ./term/...          #                        OK
$ errcheck ./term/...                                  # native                 OK
$ GOOS=windows GOARCH=amd64 errcheck ./term/...        #                        OK
$ GOOS=darwin  GOARCH=arm64 errcheck ./term/...        #                        OK
$ staticcheck ./term/...                               # native                 OK
$ GOOS=windows GOARCH=amd64 staticcheck ./term/...     #                        OK
$ GOOS=darwin  GOARCH=arm64 staticcheck ./term/...     #                        OK
$ go test ./term/ -count=1                             # 5 tests                PASS
$ go test ./term/ -count=1 -race                       #                        PASS
$ go build ./...                                       # whole repo still       OK
```

What cross-compilation does and does not prove: the Windows leg type-checks against the real
x/sys surface (so §2's audit cannot rot silently) and passes the lint gates; it has **never
executed**. Everything in §4.4–§4.5 is design-from-documentation until W6-9's Windows CI runs.

---

## 11. Phase 6 work items this design implies

| ID | Item | Anchors |
|---|---|---|
| W6-1 | `term/console_windows.go`: `EnterRaw`/restore over x/term MakeRaw + output VT modes; not-a-console detection; §5.2 four-path registration discipline | §5 |
| W6-2 | `term/console_unix.go`: termios raw + SIGWINCH watcher + initial-size emission (port of `UnixConsoleAdapter` semantics incl. cols/rows reorder) | §5, §7 |
| W6-3 | Replace ConPTY parent pipe ends with overlapped named-pipe pair (go-winio pattern) so `Read` is cancellable/deadline-able; revisit `Close`-unblocks-`Read` contract in `Pty` docs | §4.5 |
| W6-4 | `term/runner.go`: goroutine pumps + done channel; preserve initial-resize-before-I/O, EOF-on-empty-read, swallow-and-close pump errors, restore-last cleanup ordering | §5.2-3, §6, manager-foundation §1.8 |
| W6-5 | Resize watcher: Windows 250 ms `GetConsoleScreenBufferInfo` poll + initial emission; Unix SIGWINCH; wire into `TTYSession.Resize` / `Pty.Resize` | §7 |
| W6-6 | `backend/docker/tty_windows.go`: npipe `HijackedResponse` session, `ContainerExecResize`, `resp.Close()` epilogue; verify winio deadlines + half-close (OQ-W1) against a real Windows daemon | §8 |
| W6-7 | Resolve the `cmd.Wait` asymmetry: add `Wait`/exit-code to `Pty` or standardize on `cmd.Process.Wait`; error when caller pre-set `cmd.Stdin/Stdout/Stderr/ExtraFiles` on the ConPTY leg | §4.3 |
| W6-8 | Mux integration: pane = `Pty` + VT screen model under bubbletea v1.3.10; pane child = `kathara connect <dev>`; `WindowSizeMsg` → per-pane `Resize`; detach = drop panes, containers untouched | §1, §4.4 |
| W6-9 | Windows CI runner: execute `conpty_windows.go` mirror of `pty_unix_test.go` (echo/size/resize/lifecycle); §12.6 restore test on all three platforms using `conPty` as the Windows test rig; F-key sequence check against real device shells (§9 row 4) | §5.3 |
| W6-10 | Copy §9's table into DIVERGENCES.md when the runner lands (per PACKAGE_GRAPH's OQ-19 ruling; keep out of PROPOSED-DIVERGENCES.md — already sanctioned by §0.2 #2) | §9 |
| W6-11 | Map `errAlreadyStarted`/`errNotStarted`/`errClosed` + ConPTY failures onto ERROR_CODES.md (`InternalError` for terminal-internal sites per the ERROR_CODES `OSError` ruling); decide exported error surface | ERROR_CODES.md §OSError |
| W6-12 | `goleak` exemption (documented) for the connect stdin pump goroutine on Windows | §6 |
| W6-13 | `term/external_windows.go`: `cmd /c start` adapter (faithful-port territory), quoting per `util.GetExecutablePath` convention | PACKAGE_GRAPH term row |
| W6-14 | Enforce + document the Windows 10 1809 (17763) floor: version check with a clear error, release-notes line | §2.2 |

### 11.1 Addendum after the Phase 6 build (2026-08-12)

W6-8 shipped a different pane than this table assumed, and three items move with it. Recorded
in full as DIVERGENCES.md item 120; in short:

- **A pane is a backend `TTYSession` on every platform**, not a local `kathara connect` child
  on a `Pty`. One transport instead of two, the same one `kathara connect` uses, and bubbletea
  owns the console modes (it sets the Windows VT modes itself). `conpty_windows.go` and
  `term.StartPtySession` remain as the tested local-child seam — the Unix integration tests
  drive it — but nothing in `cmd/kathara` reaches ConPTY.
- **W6-9 is therefore partly moot and partly outstanding.** The §12.6 restore test exists and
  is executed on Unix and macOS (`cmd/kathara/connect_unix_test.go`, four exit paths, real
  pty, termios compared before and after); Windows has no equivalent and no ConPTY test
  mirror, so §12 risk 1 ("nothing Windows has executed") stands for `conpty_windows.go`.
- **W6-14 is not implemented.** With no production caller for `CreatePseudoConsole` the floor
  guards nothing reachable; it becomes required the moment a pane hosts a local child on
  Windows.
- W6-3, W6-7 and W6-12 are in the same position for the same reason: they are properties of
  the local-child leg, which no shipped path uses.

---

## 12. Top design risks (carried into Phase 3 review)

1. **Nothing Windows has executed.** §4's CreateProcess wiring, the §4.4 EOF chain
   (`ClosePseudoConsole` ⇒ `ERROR_BROKEN_PIPE` ⇒ `io.EOF`), and the §7 poll are
   design-from-documentation. First Windows CI run (W6-9) is the moment of truth; until then
   treat `conpty_windows.go` as unproven despite green cross-compiles.
2. **Blocked reads on non-pollable pipes (§4.5).** The spike's anonymous-pipe parent ends make
   `Close` unable to unblock a `Read` if the child wedges. W6-3 (overlapped pipes) is required
   before the mux ships; the mux multiplies pump goroutines by pane count.
3. **ConPTY teardown kills pane children (§4.4 note 1).** Fine for the planned
   respawn-on-reattach model, but it forecloses any future "detach keeps local pane processes"
   feature — if the mux design drifts that way, this becomes an architecture problem, not a
   bug.
4. **Terminal restore (§12.6) remains high-consequence.** The §5.2 pattern is belt-and-braces,
   but `os.Exit` anywhere in the CLI's command epilogue silently defeats the defer path —
   needs the reviewer rule and ideally a lint (forbid `os.Exit` in `term/` and command Run
   funcs).
5. **Key-sequence divergence (§9 row 4)** is the only rebuild change a *device* can observe
   (raw-mode readers seeing F1 as `ESC OP` vs `ESC[11~`). Probability of user impact is low
   (readline/vim handle both), but it must be tested deliberately, not discovered.
6. **x/sys pin drift.** §2's audit is true of v0.47.0; the wrappers used
   (`CreatePseudoConsole` packing `Coord`, `Update` forwarding `lpValue` verbatim) are
   hand-written code whose semantics a future x/sys could change. The pin is load-bearing;
   any bump re-runs §2 against source.
