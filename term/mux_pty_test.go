//go:build linux || darwin

package term

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/goleak"
)

// This is the integration test PORT_SPEC §9's test-infrastructure section asks
// for and the one the headless model tests cannot give: a real pseudo-terminal
// with a real child process on it, driven through the multiplexer model.
//
// The chain it exercises end to end is the whole of §3.3 item 1:
//
//	tea.KeyMsg → encodeKey → Session.Write → pty master → shell
//	shell → pty master → pump goroutine → paneOutputMsg → Screen → View
//
// A [PtySession] is used rather than a backend transport because `term` must
// not import a backend (PACKAGE_GRAPH.md D-5) — and because it is the exact
// shape the Windows leg needs, where a pane hosts a local `kathara connect`
// child on a ConPTY (SPIKES/windows-terminal.md §1).
//
// `goleak` is the merge gate PORT_SPEC §9 names: a multiplexer that leaked one
// goroutine per pane would leak one per device per scenario.

// ptyHarness is [harness] with a real session behind the single pane.
func newPtyHarness(t *testing.T, sess Session) *harness {
	t.Helper()
	m, err := newMux(t.Context(), Config{
		Scrollback: 200,
		Devices: []Device{{
			Name: "pc1",
			Open: func(context.Context) (Session, error) { return sess, nil },
		}},
	})
	if err != nil {
		t.Fatalf("newMux: %v", err)
	}
	h := &harness{t: t, m: m}
	h.m.Init()
	h.settle()
	return h
}

// paneText is everything the active pane holds, scrollback included.
func (h *harness) paneText() string {
	screen := h.m.current().screen
	return screen.Text(0, screen.TotalLines())
}

// waitUntil drains pump messages until the pane satisfies want, or the test
// times out. Draining is how the update loop would see them; the timeout is
// what turns a wedged pump into a failure instead of a hang.
func (h *harness) waitUntil(what string, want func(pane string) bool, timeout time.Duration) string {
	h.t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		select {
		case msg := <-h.m.events:
			h.m.Update(msg)
		case <-time.After(10 * time.Millisecond):
		}
		text := h.paneText()
		if want(text) {
			return text
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("timed out waiting for %s; pane holds:\n%s", what, text)
		}
	}
}

// waitFor is waitUntil for the common "this text appeared" case.
func (h *harness) waitFor(want string, timeout time.Duration) string {
	h.t.Helper()
	return h.waitUntil(strconv.Quote(want),
		func(pane string) bool { return strings.Contains(pane, want) }, timeout)
}

// typeLine sends a line of text and Enter, one key event at a time, exactly as
// bubbletea would deliver them.
func (h *harness) typeLine(s string) {
	h.t.Helper()
	for _, r := range s {
		h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	h.send(tea.KeyMsg{Type: tea.KeyEnter})
}

func TestMultiplexerOverRealPty(t *testing.T) {
	defer goleak.VerifyNone(t)

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no shell on PATH: %v", err)
	}

	cmd := exec.Command(sh)
	// A predictable environment: no user rc file, no prompt escape sequences,
	// and a TERM the shell will not try to query.
	cmd.Env = []string{"PS1=", "PS2=", "TERM=xterm", "PATH=/usr/bin:/bin:/usr/sbin:/sbin", "HOME=/nonexistent"}

	sess, err := StartPtySession(cmd, Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatalf("StartPtySession: %v", err)
	}

	h := newPtyHarness(t, sess)
	// Before goleak's own deferred check, which runs after this one (LIFO) —
	// a t.Cleanup would run too late to be seen by it.
	defer h.m.shutdown()

	if h.m.panes[0].state != paneLive {
		t.Fatalf("pane state = %v, want live", h.m.panes[0].state)
	}

	t.Run("echo round trip", func(t *testing.T) {
		h.send(tea.WindowSizeMsg{Width: 80, Height: 26})
		h.typeLine("echo kathara-pty-ok")

		// Twice: once because the pty's line discipline echoed what was typed,
		// once because the shell ran it. Waiting for both is what proves the
		// round trip rather than just the write.
		h.waitUntil("the typed line and the shell's output",
			func(pane string) bool { return strings.Count(pane, "kathara-pty-ok") >= 2 },
			10*time.Second)
	})

	t.Run("resize reaches the pty", func(t *testing.T) {
		// The window is 100×32, so the device gets 100×30 — the tab bar and
		// the status line are not the device's.
		h.send(tea.WindowSizeMsg{Width: 100, Height: 32})
		h.typeLine("stty size")
		h.waitFor("30 100", 10*time.Second)
	})

	t.Run("scrollback accrues from real output", func(t *testing.T) {
		h.typeLine("i=0; while [ $i -lt 60 ]; do echo scroll-$i; i=$((i+1)); done")
		h.waitFor("scroll-59", 10*time.Second)

		screen := h.m.current().screen
		if screen.ScrollbackLen() == 0 {
			t.Fatal("60 lines of output produced no scrollback")
		}
		// The early lines have left the visible screen but not the history.
		all := screen.Text(0, screen.TotalLines())
		if !strings.Contains(all, "scroll-0") {
			t.Error("the oldest line is not in the scrollback")
		}
		if strings.Contains(screen.String(), "scroll-0") {
			t.Error("60 lines fitted on a 30-row screen; the test proves nothing")
		}
	})

	t.Run("copy yields the scrollback as plain text", func(t *testing.T) {
		var copied string
		h.m.copy = func(text string) error {
			copied = text
			return nil
		}
		h.send(keyMsg("ctrl+b"))
		h.send(keyMsg("["))
		h.send(keyMsg("v"))
		for i := 0; i < 5; i++ {
			h.send(keyMsg("up"))
		}
		h.send(keyMsg("y"))

		if copied == "" {
			t.Fatal("copy produced nothing")
		}
		if strings.ContainsRune(copied, 0x1b) {
			t.Errorf("copied text carries escape sequences: %q", copied)
		}
		if !strings.Contains(copied, "scroll-") {
			t.Errorf("copied text does not come from the pane: %q", copied)
		}
	})

	t.Run("detach closes the session and reaps the child", func(t *testing.T) {
		if _, cmd := h.m.Update(DetachMsg{}); cmd == nil {
			t.Fatal("DetachMsg produced no command, want tea.Quit")
		}
		h.m.shutdown()

		// The child is gone: signal 0 to a reaped pid fails.
		if err := cmd.Process.Signal(nil); err == nil {
			t.Error("the pane child survived detach")
		}
	})
}

// TestPaneStaleEOFDoesNotCloseAReattachedSession pins the generation guard.
//
// Without it, the EOF the *previous* session's pump reports arrives a moment
// after `ctrl+b r` has opened a new one, and closes the pane the user has just
// re-attached — a bug that only shows up under real timing, which is why it is
// pinned with a real pty rather than a fake.
func TestPaneStaleEOFDoesNotCloseAReattachedSession(t *testing.T) {
	defer goleak.VerifyNone(t)

	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skipf("no shell on PATH: %v", err)
	}

	newSession := func() Session {
		cmd := exec.Command(sh)
		cmd.Env = []string{"PS1=", "TERM=xterm", "PATH=/usr/bin:/bin"}
		s, err := StartPtySession(cmd, Winsize{Cols: 80, Rows: 24})
		if err != nil {
			t.Fatalf("StartPtySession: %v", err)
		}
		return s
	}

	first := newSession()
	second := newSession()
	calls := 0

	m, err := newMux(t.Context(), Config{Devices: []Device{{
		Name: "pc1",
		Open: func(context.Context) (Session, error) {
			calls++
			if calls == 1 {
				return first, nil
			}
			return second, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, m: m}
	defer m.shutdown()
	m.Init()
	h.settle()
	h.send(tea.WindowSizeMsg{Width: 80, Height: 12})

	// End the first session for real, then re-attach before draining the EOF
	// the pump is about to report.
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	m.panes[0].state = paneClosed
	h.send(keyMsg("ctrl+b"))
	h.send(keyMsg("r"))
	h.settle()

	if m.panes[0].state != paneLive {
		t.Fatalf("pane state = %v, want live after re-attach", m.panes[0].state)
	}
	// Drain whatever the dead pump left behind; none of it may close the pane.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-m.events:
			m.Update(msg)
		case <-time.After(50 * time.Millisecond):
			if m.panes[0].state != paneLive {
				t.Fatalf("a stale message closed the re-attached pane: %v", m.panes[0].state)
			}
			return
		}
		if m.panes[0].state != paneLive {
			t.Fatalf("a stale message closed the re-attached pane: %v", m.panes[0].state)
		}
	}
}
