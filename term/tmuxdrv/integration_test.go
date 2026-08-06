package tmuxdrv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests drive the real tmux binary. They are skipped when tmux is absent.
//
// Isolation rules, both mandatory (see docs/port/SPIKES/tmux.md):
//   - -S <socket in t.TempDir()>: never touch the tmux server the developer or
//     CI agent is sitting in. Every test gets its own server, kills it
//     afterwards, and — unlike -L, which litters /tmp/tmux-<uid> because tmux
//     never unlinks a socket, not even on kill-server — leaves nothing behind.
//   - -f /dev/null: a ~/.tmux.conf setting base-index, automatic-rename or
//     default-command would otherwise change what the assertions see.

const attachHelperEnv = "KATHARA_TMUXDRV_ATTACH_HELPER"

// TestMain doubles as the attach helper process: Attach replaces the process
// image, so it can only be exercised by a process we are willing to lose.
func TestMain(m *testing.M) {
	if spec := os.Getenv(attachHelperEnv); spec != "" {
		runAttachHelper(spec)
		return
	}
	os.Exit(m.Run())
}

func runAttachHelper(spec string) {
	parts := strings.SplitN(spec, "|", 3)
	if len(parts) != 3 {
		fmt.Fprintf(os.Stderr, "attach helper: bad spec %q\n", spec)
		os.Exit(2)
	}
	d := &Driver{SocketPath: parts[0], Config: os.DevNull}
	// On success this execve's into tmux and never returns.
	if err := d.Attach(context.Background(), parts[1], parts[2]); err != nil {
		fmt.Fprintf(os.Stderr, "attach helper: %v\n", err)
		os.Exit(3)
	}
	os.Exit(0)
}

// testDriver returns a Driver bound to a private tmux server, killed on cleanup.
func testDriver(t *testing.T) *Driver {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping tmux integration test")
	}

	d := &Driver{SocketPath: filepath.Join(t.TempDir(), "tmux.sock"), Config: os.DevNull}

	t.Cleanup(func() {
		// kill-server exits 1 when no server is running, which is fine here.
		_, _ = d.run(context.Background(), "kill-server")
	})
	return d
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestVersion(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	v, err := d.Version(ctx)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !strings.HasPrefix(v, "tmux ") {
		t.Fatalf("Version = %q, want a string starting with %q", v, "tmux ")
	}
	t.Logf("tmux version under test: %s", v)

	if err := d.Available(ctx); err != nil {
		t.Fatalf("Available: %v", err)
	}
}

// TestEnsureSessionLifecycle is the spike's headline scenario: create a session
// with detached windows, verify them, re-run ensure (attach, do not clobber),
// kill, verify gone.
func TestEnsureSessionLifecycle(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	session := SessionName("", "d41d8cd98f00b204e980")
	devices := []string{"pc1", "pc2", "router"}

	// --- create -----------------------------------------------------------
	outcomes := make([]WindowOutcome, 0, len(devices))
	for _, dev := range devices {
		got, err := d.EnsureWindow(ctx, session, Window{Name: dev, Command: "sleep 300"})
		if err != nil {
			t.Fatalf("EnsureWindow(%s): %v", dev, err)
		}
		outcomes = append(outcomes, got)
	}
	if outcomes[0] != SessionCreated {
		t.Errorf("first device: outcome = %v, want %v", outcomes[0], SessionCreated)
	}
	for i, o := range outcomes[1:] {
		if o != WindowCreated {
			t.Errorf("device %s: outcome = %v, want %v", devices[i+1], o, WindowCreated)
		}
	}

	// --- verify through list-windows -F ------------------------------------
	names, err := d.WindowNames(ctx, session)
	if err != nil {
		t.Fatalf("WindowNames: %v", err)
	}
	if strings.Join(names, ",") != strings.Join(devices, ",") {
		t.Fatalf("windows = %v, want %v (one window per device, in creation order, no placeholder)", names, devices)
	}

	windows, err := d.ListWindows(ctx, session)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	for _, w := range windows {
		if w.ID == "" || !strings.HasPrefix(w.ID, "@") {
			t.Errorf("window %s: id = %q, want a tmux window id", w.Name, w.ID)
		}
		if w.Dead {
			t.Errorf("window %s: dead, want its sleep still running", w.Name)
		}
	}

	sessions, err := d.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0] != session {
		t.Fatalf("ListSessions = %v, want [%s]", sessions, session)
	}
	kathara, err := d.ListKatharaSessions(ctx)
	if err != nil {
		t.Fatalf("ListKatharaSessions: %v", err)
	}
	if len(kathara) != 1 {
		t.Fatalf("ListKatharaSessions = %v, want 1 entry", kathara)
	}

	// --- attach, do not clobber -------------------------------------------
	created, err := d.EnsureSession(ctx, session, Window{Name: "would-clobber", Command: "sleep 300"})
	if err != nil {
		t.Fatalf("EnsureSession (second run): %v", err)
	}
	if created {
		t.Fatal("EnsureSession reported it created an already-existing session")
	}

	// Re-running the whole deploy must be a no-op, not a second set of windows.
	for _, dev := range devices {
		got, err := d.EnsureWindow(ctx, session, Window{Name: dev, Command: "sleep 300"})
		if err != nil {
			t.Fatalf("EnsureWindow(%s) (second run): %v", dev, err)
		}
		if got != WindowExisted {
			t.Errorf("EnsureWindow(%s) (second run): outcome = %v, want %v", dev, got, WindowExisted)
		}
	}

	after, err := d.WindowNames(ctx, session)
	if err != nil {
		t.Fatalf("WindowNames (after re-ensure): %v", err)
	}
	if strings.Join(after, ",") != strings.Join(devices, ",") {
		t.Fatalf("windows after re-ensure = %v, want unchanged %v", after, devices)
	}

	// --- one more device joins a live session ------------------------------
	got, err := d.EnsureWindow(ctx, session, Window{Name: "pc4", Command: "sleep 300"})
	if err != nil {
		t.Fatalf("EnsureWindow(pc4): %v", err)
	}
	if got != WindowCreated {
		t.Errorf("EnsureWindow(pc4): outcome = %v, want %v", got, WindowCreated)
	}
	if has, err := d.HasWindow(ctx, session, "pc4"); err != nil || !has {
		t.Fatalf("HasWindow(pc4) = %v, %v; want true, nil", has, err)
	}

	// --- kill --------------------------------------------------------------
	killed, err := d.KillSession(ctx, session)
	if err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if !killed {
		t.Fatal("KillSession reported nothing to kill")
	}

	if has, err := d.HasSession(ctx, session); err != nil || has {
		t.Fatalf("HasSession after kill = %v, %v; want false, nil", has, err)
	}
	if _, err := d.ListWindows(ctx, session); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ListWindows after kill: err = %v, want ErrSessionNotFound", err)
	}

	// Killing again is idempotent, and reports that there was nothing to kill.
	killed, err = d.KillSession(ctx, session)
	if err != nil {
		t.Fatalf("KillSession (idempotent): %v", err)
	}
	if killed {
		t.Error("KillSession reported killing an absent session")
	}
}

// TestExactMatchTargeting pins the quirk that makes every probe in this package
// trustworthy: without the '=' prefix, tmux resolves "kathara_lab" to
// "kathara_lab1".
func TestExactMatchTargeting(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	session := "kathara_lab1"
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	if has, err := d.HasSession(ctx, "kathara_lab"); err != nil || has {
		t.Fatalf("HasSession(prefix) = %v, %v; want false, nil (exact match required)", has, err)
	}
	if has, err := d.HasSession(ctx, "kathara_lab1"); err != nil || !has {
		t.Fatalf("HasSession(exact) = %v, %v; want true, nil", has, err)
	}
	if has, err := d.HasSession(ctx, "KATHARA_LAB1"); err != nil || has {
		t.Fatalf("HasSession(wrong case) = %v, %v; want false, nil", has, err)
	}

	// The same holds for windows, and a prefix must not select a device.
	if err := d.SelectWindow(ctx, session, "pc"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("SelectWindow(prefix) = %v, want ErrWindowNotFound", err)
	}
	if has, err := d.HasWindow(ctx, session, "pc"); err != nil || has {
		t.Fatalf("HasWindow(prefix) = %v, %v; want false, nil", has, err)
	}
}

// TestAbsentServerIsNotAnError pins the second exit-code quirk: with no server
// on the socket, every read-only query exits 1.
func TestAbsentServerIsNotAnError(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	sessions, err := d.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions with no server: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("ListSessions with no server = %v, want empty", sessions)
	}

	if has, err := d.HasSession(ctx, "kathara_nothing"); err != nil || has {
		t.Fatalf("HasSession with no server = %v, %v; want false, nil", has, err)
	}
	if killed, err := d.KillSession(ctx, "kathara_nothing"); err != nil || killed {
		t.Fatalf("KillSession with no server = %v, %v; want false, nil", killed, err)
	}
	if err := d.DetachSession(ctx, "kathara_nothing"); err != nil {
		t.Fatalf("DetachSession with no server: %v", err)
	}
	if _, err := d.ListWindows(ctx, "kathara_nothing"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("ListWindows with no server: err = %v, want ErrSessionNotFound", err)
	}
	if err := d.Attach(ctx, "kathara_nothing", ""); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Attach with no server: err = %v, want ErrSessionNotFound", err)
	}

	// The server also exits when its last session is killed, putting the socket
	// back in this state.
	session := SessionName("transient", "")
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if _, err := d.KillSession(ctx, session); err != nil {
		t.Fatalf("KillSession: %v", err)
	}
	if sessions, err := d.ListSessions(ctx); err != nil || len(sessions) != 0 {
		t.Fatalf("ListSessions after last session died = %v, %v; want empty, nil", sessions, err)
	}
}

// TestWindowOptionsAndDeath covers -c, -e, and what happens when a window's
// command exits.
func TestWindowOptionsAndDeath(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	dir := t.TempDir()
	session := SessionName("optlab", "")
	marker := filepath.Join(dir, "marker")

	w := Window{
		Name:    "pc1",
		Command: fmt.Sprintf("sh -c 'printf %%s \"$KATHARA_SPIKE:$PWD\" > %q; sleep 300'", marker),
		Dir:     dir,
		Env:     []string{"KATHARA_SPIKE=injected"},
	}
	if _, err := d.EnsureSession(ctx, session, w); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	var content string
	waitFor(t, 5*time.Second, func() bool {
		b, err := os.ReadFile(marker)
		if err != nil {
			return false
		}
		content = string(b)
		return content != ""
	})
	if want := "injected:" + dir; content != want {
		t.Errorf("window environment/cwd = %q, want %q (-e and -c must reach the command)", content, want)
	}

	// A window whose command exits is closed by tmux and disappears...
	if err := d.AddWindow(ctx, session, Window{Name: "transient", Command: "true"}); err != nil {
		t.Fatalf("AddWindow(transient): %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		has, err := d.HasWindow(ctx, session, "transient")
		return err == nil && !has
	})
	// The error is checked: waitFor gives up silently, so a HasWindow that
	// keeps failing would otherwise make this assertion pass vacuously.
	if has, err := d.HasWindow(ctx, session, "transient"); err != nil || has {
		t.Errorf("HasWindow(transient) = %v, %v; want false, nil (tmux should have closed the window)", has, err)
	}

	// ...unless remain-on-exit keeps the corpse visible, which is how a failing
	// `kathara connect` stops vanishing before the user can read it. `false`
	// exits as fast as a command can, so this also pins that the option is
	// applied in the same tmux invocation as the window creation.
	if err := d.AddWindow(ctx, session, Window{Name: "failed", Command: "false", RemainOnExit: true}); err != nil {
		t.Fatalf("AddWindow(failed): %v", err)
	}
	waitFor(t, 5*time.Second, func() bool {
		windows, err := d.ListWindows(ctx, session)
		if err != nil {
			return false
		}
		for _, w := range windows {
			if w.Name == "failed" && w.Dead {
				return true
			}
		}
		return false
	})
	windows, err := d.ListWindows(ctx, session)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	var found bool
	for _, w := range windows {
		if w.Name == "failed" {
			found = true
			if !w.Dead {
				t.Error("remain-on-exit window is not marked dead")
			}
		}
	}
	if !found {
		t.Error("remain-on-exit window disappeared")
	}

	// AddWindow against an absent session is a typed error, not a silent create.
	if err := d.AddWindow(ctx, "kathara_absent", Window{Name: "pc1", Command: "sleep 1"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("AddWindow into absent session = %v, want ErrSessionNotFound", err)
	}
}

// TestAttachSelectAndDetach exercises the `kathara connect --tmux <device>`
// path end to end: a helper process execve's into tmux under a pty, the right
// window is selected, detaching leaves the session running.
func TestAttachSelectAndDetach(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not installed; cannot allocate a pty for the attach test")
	}

	session := SessionName("attachlab", "")
	for _, dev := range []string{"pc1", "pc2", "pc3"} {
		if _, err := d.EnsureWindow(ctx, session, Window{Name: dev, Command: "sleep 300"}); err != nil {
			t.Fatalf("EnsureWindow(%s): %v", dev, err)
		}
	}

	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("locating test binary: %v", err)
	}

	// The helper runs with $TMUX cleared: the ordinary "user runs kathara from
	// a plain shell" case, which must execve into tmux.
	helper := startAttachHelper(t, ctx, self, envWithout(os.Environ(), "TMUX"),
		d.SocketPath+"|"+session+"|pc2")

	waitFor(t, 10*time.Second, func() bool {
		ttys, err := d.ClientTTYs(ctx, session)
		return err == nil && len(ttys) > 0
	})
	ttys, err := d.ClientTTYs(ctx, session)
	if err != nil {
		t.Fatalf("ClientTTYs: %v", err)
	}
	if len(ttys) == 0 {
		t.Fatal("no client attached after Attach; the helper never reached tmux")
	}

	// Attach(session, "pc2") must have selected pc2, not left window 0 active.
	windows, err := d.ListWindows(ctx, session)
	if err != nil {
		t.Fatalf("ListWindows: %v", err)
	}
	var active string
	for _, w := range windows {
		if w.Active {
			active = w.Name
		}
	}
	if active != "pc2" {
		t.Errorf("active window = %q, want %q (connect --tmux must land on the requested device)", active, "pc2")
	}

	// Detach: the client goes away, everything else stays.
	if err := d.DetachSession(ctx, session); err != nil {
		t.Fatalf("DetachSession: %v", err)
	}
	waitFor(t, 10*time.Second, func() bool {
		ttys, err := d.ClientTTYs(ctx, session)
		return err == nil && len(ttys) == 0
	})
	if ttys, err := d.ClientTTYs(ctx, session); err != nil || len(ttys) != 0 {
		t.Fatalf("ClientTTYs after detach = %v, %v; want empty, nil", ttys, err)
	}

	// Detaching ends the client cleanly; the helper's tmux exits 0.
	helper.expectExit(t, 10*time.Second)

	if has, err := d.HasSession(ctx, session); err != nil || !has {
		t.Fatalf("HasSession after detach = %v, %v; want true, nil (detach must not kill the session)", has, err)
	}
	names, err := d.WindowNames(ctx, session)
	if err != nil {
		t.Fatalf("WindowNames after detach: %v", err)
	}
	if len(names) != 3 {
		t.Fatalf("windows after detach = %v, want all 3 still running", names)
	}

	// Reattach across invocations: a second helper must find the same session.
	// This one runs with $TMUX pointing at a *different* tmux server, the
	// "user's own tmux is on another socket" case: not nesting, so Attach must
	// still execve into tmux after dropping $TMUX.
	helper2 := startAttachHelper(t, ctx,
		self,
		append(envWithout(os.Environ(), "TMUX"), "TMUX=/tmp/tmux-0/definitely-not-our-socket,1,0"),
		d.SocketPath+"|"+session+"|pc3",
	)

	waitFor(t, 10*time.Second, func() bool {
		ttys, err := d.ClientTTYs(ctx, session)
		return err == nil && len(ttys) > 0
	})
	// Asserted, not assumed: Attach select-windows *before* it hands the
	// terminal over, so the active-window check below passes even when the
	// attach itself never happens. Without this the whole different-server
	// exec path could be dead and the test would still be green.
	ttys, err = d.ClientTTYs(ctx, session)
	if err != nil {
		t.Fatalf("ClientTTYs (reattach): %v", err)
	}
	if len(ttys) == 0 {
		t.Fatal("no client attached after the second Attach; the different-server exec path did not reach tmux")
	}

	windows, err = d.ListWindows(ctx, session)
	if err != nil {
		t.Fatalf("ListWindows (reattach): %v", err)
	}
	active = ""
	for _, w := range windows {
		if w.Active {
			active = w.Name
		}
	}
	if active != "pc3" {
		t.Errorf("active window after reattach = %q, want %q", active, "pc3")
	}

	// And the second client detaches as cleanly as the first, which is the
	// only thing that proves it was a real tmux client and not a corpse.
	if err := d.DetachSession(ctx, session); err != nil {
		t.Fatalf("DetachSession (reattach): %v", err)
	}
	helper2.expectExit(t, 10*time.Second)
	if has, err := d.HasSession(ctx, session); err != nil || !has {
		t.Fatalf("HasSession after second detach = %v, %v; want true, nil", has, err)
	}
}

// TestAttachSwitchesClientOnSameServer covers the third hand-over path, the one
// users hit most: kathara is run from inside a pane of the very server that
// holds the scenario session. execve'ing attach-session there would either be
// refused by tmux (when $TMUX survives) or mirror the terminal into its own
// pane (when it does not), so Attach must switch the existing client instead
// and return normally.
func TestAttachSwitchesClientOnSameServer(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script(1) not installed; cannot allocate a pty for the attach test")
	}

	first := SessionName("switchfrom", "")
	second := SessionName("switchto", "")
	for _, s := range []string{first, second} {
		if _, err := d.EnsureWindow(ctx, s, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
			t.Fatalf("EnsureWindow(%s): %v", s, err)
		}
	}

	self, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("locating test binary: %v", err)
	}

	// A real client on a pty, sitting in the first session.
	startAttachHelper(t, ctx, self, envWithout(os.Environ(), "TMUX"), d.SocketPath+"|"+first+"|pc1")
	waitFor(t, 10*time.Second, func() bool {
		ttys, err := d.ClientTTYs(ctx, first)
		return err == nil && len(ttys) > 0
	})
	before, err := d.ClientTTYs(ctx, first)
	if err != nil || len(before) == 0 {
		t.Fatalf("ClientTTYs(%s) = %v, %v; want one attached client", first, before, err)
	}

	// Now "kathara connect --tmux" from inside that server: $TMUX names this
	// socket. No pty for this helper on purpose — if Attach were to take an
	// exec path, tmux would fail with "open terminal failed: not a terminal"
	// and the helper's exit status would say so.
	inner := startHelperProcess(t, ctx, self,
		append(envWithout(os.Environ(), "TMUX"), "TMUX="+d.SocketPath+",1,0"),
		d.SocketPath+"|"+second+"|pc1")
	inner.expectExit(t, 10*time.Second)

	// The pre-existing client moved to the second session; no second client
	// was created, and nothing was left attached to the first.
	waitFor(t, 10*time.Second, func() bool {
		ttys, err := d.ClientTTYs(ctx, second)
		return err == nil && len(ttys) > 0
	})
	after, err := d.ClientTTYs(ctx, second)
	if err != nil {
		t.Fatalf("ClientTTYs(%s): %v", second, err)
	}
	if len(after) != 1 || after[0] != before[0] {
		t.Fatalf("clients on %s = %v, want the existing client %v switched over", second, after, before)
	}
	if left, err := d.ClientTTYs(ctx, first); err != nil || len(left) != 0 {
		t.Fatalf("clients on %s after switch = %v, %v; want none", first, left, err)
	}
}

// TestAbsenceClassifierSpellings pins the two whitelist entries that no other
// test can reach through the exported API, because the code is written to
// avoid provoking them: "duplicate session:" (EnsureSession checks HasSession
// first, so only a lost race gets there) and "no current client" on a *live*
// server (TestAbsentServerIsNotAnError only covers the no-server spelling).
// If tmux ever renames either, attach-don't-clobber turns into an error and
// DetachSession stops being idempotent — silently, in both cases.
func TestAbsenceClassifierSpellings(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	session := SessionName("classifier", "")
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	// The race EnsureSession is written to survive: another process created
	// the session between our has-session and our new-session.
	_, err := d.run(ctx, "new-session", "-d", "-s", session, "-n", "pc1")
	if err == nil {
		t.Fatal("second new-session succeeded; tmux should reject a duplicate name")
	}
	if !isAbsence(err, duplicateSessionMessages...) {
		t.Fatalf("duplicate new-session error %v is not classified as a lost race; EnsureSession would report it as a failure", err)
	}
	if isSessionAbsent(err) {
		t.Fatalf("duplicate new-session error %v classified as session-absent", err)
	}

	// detach-client with a live server but nothing attached.
	_, err = d.run(ctx, "detach-client", "-s", sessionTarget(session))
	if err == nil {
		t.Fatal("detach-client with nothing attached succeeded; expected tmux to exit 1")
	}
	if !isAbsence(err, clientAbsentMessages...) {
		t.Fatalf("detach-client-with-no-client error %v is not classified as absence; DetachSession would fail instead of being a no-op", err)
	}
	if err := d.DetachSession(ctx, session); err != nil {
		t.Fatalf("DetachSession on a live server with no client: %v", err)
	}

	// A real failure must not be swallowed by any of the groups: tmux rejects
	// an unknown command with prose none of them matches.
	_, err = d.run(ctx, "no-such-tmux-command")
	if err == nil {
		t.Fatal("unknown tmux command succeeded")
	}
	if isSessionAbsent(err) || isAbsence(err, windowAbsentMessages...) ||
		isAbsence(err, clientAbsentMessages...) || isAbsence(err, duplicateSessionMessages...) {
		t.Fatalf("real failure %v classified as absence", err)
	}
}

// TestForeignSessionNameCannotForgeARow backs the claim in ListSessions's doc:
// the newline-delimited -F listing cannot be spoofed by a session someone else
// created, because tmux vis-escapes control characters in session names.
func TestForeignSessionNameCannotForgeARow(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	// Created through the bare CLI: this is a name *we* would never build.
	if _, err := d.run(ctx, "new-session", "-d", "-s", "evil\nkathara_phantom", "-n", "w", "--", "sleep 300"); err != nil {
		t.Fatalf("new-session with a newline in the name: %v", err)
	}

	sessions, err := d.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("ListSessions = %q, want a single row (tmux must escape the newline, not store it)", sessions)
	}
	if strings.Contains(sessions[0], "\n") {
		t.Fatalf("ListSessions row %q contains a raw newline", sessions[0])
	}
	if kathara, err := d.ListKatharaSessions(ctx); err != nil || len(kathara) != 0 {
		t.Fatalf("ListKatharaSessions = %q, %v; want none (no phantom kathara_ session)", kathara, err)
	}
}

// TestWindowCommandTrailingSemicolon: tmux's command-sequence parser eats a
// trailing ';' even after "--", which would silently truncate a device's
// command. Kathara's own connect command never ends in ';', so this is about
// the transport not lying about what it ran.
func TestWindowCommandTrailingSemicolon(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	session := SessionName("semicolon", "")
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300;"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	// tmux renders a start command containing spaces in double quotes.
	got, err := d.run(ctx, "list-panes", "-t", windowTarget(session, "pc1"), "-F", "#{pane_start_command}")
	if err != nil {
		t.Fatalf("list-panes: %v", err)
	}
	if want := `"sleep 300;"`; got != want {
		t.Errorf("pane start command = %q, want %q (the trailing ';' must survive)", got, want)
	}
	// The escaping is only for the trailing one; a ';' inside the command is
	// already safe and must not be doubled.
	if _, err := d.EnsureWindow(ctx, session, Window{Name: "pc2", Command: "sleep 300; true"}); err != nil {
		t.Fatalf("EnsureWindow(pc2): %v", err)
	}
	got, err = d.run(ctx, "list-panes", "-t", windowTarget(session, "pc2"), "-F", "#{pane_start_command}")
	if err != nil {
		t.Fatalf("list-panes (pc2): %v", err)
	}
	if want := `"sleep 300; true"`; got != want {
		t.Errorf("pane start command = %q, want %q", got, want)
	}
}

// TestAttachMissingWindow: connecting to a device with no window must fail
// before the terminal is handed over.
func TestAttachMissingWindow(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	session := SessionName("missingwin", "")
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}
	if err := d.Attach(ctx, session, "nosuchdevice"); !errors.Is(err, ErrWindowNotFound) {
		t.Fatalf("Attach to missing window = %v, want ErrWindowNotFound", err)
	}
}

// TestSanitizedNameRoundTrip proves the sanitization is not cosmetic: tmux
// rewrites '.' and ':' itself, so a name we did not sanitize would be stored
// under a different name than the one we probe for.
func TestSanitizedNameRoundTrip(t *testing.T) {
	d := testDriver(t)
	ctx := testContext(t)

	raw := "lab.v2:beta"
	session := SessionName(raw, "")
	if _, err := d.EnsureSession(ctx, session, Window{Name: "pc1", Command: "sleep 300"}); err != nil {
		t.Fatalf("EnsureSession: %v", err)
	}

	sessions, err := d.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 || sessions[0] != session {
		t.Fatalf("tmux stored %v, we asked for %q: sanitization is not round-tripping", sessions, session)
	}
	if has, err := d.HasSession(ctx, session); err != nil || !has {
		t.Fatalf("HasSession(%q) = %v, %v; want true, nil", session, has, err)
	}

	// The raw, unsanitized name is exactly what a naive implementation would
	// probe with. tmux would never report it as present — proven here with the
	// bare CLI call...
	if _, err := d.run(ctx, "has-session", "-t", sessionTarget(SessionPrefix+raw)); !isSessionAbsent(err) {
		t.Fatalf("raw has-session(unsanitized) = %v, want tmux to report it absent", err)
	}
	// ...and the exported probe refuses the question outright rather than
	// handing back a "false" the caller would act on forever.
	if _, err := d.HasSession(ctx, SessionPrefix+raw); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("HasSession(unsanitized) = %v, want ErrInvalidName", err)
	}
	// ...and creating with it would be rejected by us rather than silently
	// producing a session under a different name.
	if _, err := d.EnsureSession(ctx, SessionPrefix+raw, Window{Name: "pc1"}); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("EnsureSession(unsanitized) = %v, want ErrInvalidName", err)
	}
}

// attachHelper is this test binary re-executed on a pty, standing in for a
// `kathara connect --tmux` invocation. Attach replaces the process image, so
// the helper has to be a process we can afford to lose.
type attachHelper struct {
	cmd    *exec.Cmd
	done   chan error
	exited bool
	stderr *strings.Builder // only for helpers started without a pty
}

// startAttachHelper runs `script -q -e -c <testbin> /dev/null`, which gives the
// helper a pty; tmux refuses to attach without one ("open terminal failed: not
// a terminal"). script -e propagates the command's exit status.
func startAttachHelper(t *testing.T, ctx context.Context, self string, env []string, spec string) *attachHelper {
	t.Helper()
	return startHelper(t, exec.CommandContext(ctx, "script", "-q", "-e", "-c", shellQuote(self), "/dev/null"), env, spec)
}

// startHelperProcess runs the helper *without* a pty. Used for the same-server
// switch-client path, which must not need one: if Attach took an exec path
// instead, tmux would refuse with "open terminal failed: not a terminal" and
// the helper would exit non-zero. Its stderr is kept so that failure says why.
func startHelperProcess(t *testing.T, ctx context.Context, self string, env []string, spec string) *attachHelper {
	t.Helper()
	cmd := exec.CommandContext(ctx, self)
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	h := startHelper(t, cmd, env, spec)
	h.stderr = stderr
	return h
}

func startHelper(t *testing.T, cmd *exec.Cmd, env []string, spec string) *attachHelper {
	t.Helper()

	cmd.Env = append(env, attachHelperEnv+"="+spec)
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting attach helper (%s): %v", spec, err)
	}

	h := &attachHelper{cmd: cmd, done: make(chan error, 1)}
	go func() { h.done <- cmd.Wait() }()

	t.Cleanup(func() {
		if h.exited {
			return
		}
		_ = cmd.Process.Kill()
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
			t.Errorf("attach helper (%s) did not die after SIGKILL", spec)
		}
	})
	return h
}

// expectExit waits for the helper to exit cleanly, e.g. after a detach.
func (h *attachHelper) expectExit(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case err := <-h.done:
		h.exited = true
		if err != nil {
			var why string
			if h.stderr != nil {
				why = ": " + strings.TrimSpace(h.stderr.String())
			}
			t.Errorf("attach helper exited with %v, want a clean exit%s", err, why)
		}
	case <-time.After(timeout):
		t.Error("attach helper did not exit")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func envWithout(env []string, key string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
