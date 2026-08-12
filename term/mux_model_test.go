package term

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"go.uber.org/goleak"
)

// fakeSession is a Session with no process behind it: writes are recorded,
// reads block until the test feeds them or the session closes.
type fakeSession struct {
	mu      sync.Mutex
	written []byte
	sizes   [][2]uint16
	closed  bool

	out    chan []byte
	done   chan struct{}
	once   sync.Once
	remain []byte
}

func newFakeSession() *fakeSession {
	return &fakeSession{out: make(chan []byte, 8), done: make(chan struct{})}
}

func (f *fakeSession) Read(p []byte) (int, error) {
	if len(f.remain) > 0 {
		n := copy(p, f.remain)
		f.remain = f.remain[n:]
		return n, nil
	}
	select {
	case b, ok := <-f.out:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, b)
		f.remain = b[n:]
		return n, nil
	case <-f.done:
		return 0, io.EOF
	}
}

func (f *fakeSession) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakeSession) Resize(cols, rows uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sizes = append(f.sizes, [2]uint16{cols, rows})
	return nil
}

func (f *fakeSession) Close() error {
	f.once.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		close(f.done)
	})
	return nil
}

func (f *fakeSession) input() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.written)
}

func (f *fakeSession) lastSize() ([2]uint16, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sizes) == 0 {
		return [2]uint16{}, false
	}
	return f.sizes[len(f.sizes)-1], true
}

func (f *fakeSession) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// harness drives a model without a bubbletea program: the update loop is the
// test goroutine, which is exactly the single-threaded contract the model
// documents. Nothing here runs the model's own [muxModel.nextEvent] command —
// that one blocks by design — so pump and open messages are drained straight
// from the event channel instead.
type harness struct {
	t *testing.T
	m *muxModel

	// mu guards sessions, which the model's open goroutine reads while the
	// test goroutine may be replacing an entry (see replace).
	mu       sync.Mutex
	sessions []*fakeSession

	copied []string
}

// session is the fake behind one device.
func (h *harness) session(i int) *fakeSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessions[i]
}

// replace installs a fresh session for a device, which is what re-attaching
// to a still-running container gets: a new stream, not the closed one.
func (h *harness) replace(i int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[i] = newFakeSession()
}

func newHarness(t *testing.T, names ...string) *harness {
	t.Helper()
	h := &harness{t: t}
	cfg := Config{Scrollback: 100}
	for range names {
		h.sessions = append(h.sessions, newFakeSession())
	}
	for i, name := range names {
		idx := i
		cfg.Devices = append(cfg.Devices, Device{
			Name: name,
			Open: func(context.Context) (Session, error) { return h.session(idx), nil },
		})
	}
	cfg.Copy = func(text string) error {
		h.copied = append(h.copied, text)
		return nil
	}

	m, err := newMux(t.Context(), cfg)
	if err != nil {
		t.Fatalf("newMux: %v", err)
	}
	h.m = m
	t.Cleanup(m.shutdown)
	h.start()
	return h
}

// start is what a bubbletea program's first two steps do: Init (which opens
// the first pane) and the initial WindowSizeMsg.
func (h *harness) start() {
	h.t.Helper()
	h.m.Init()
	h.settle()
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10})
}

// send delivers one message to Update and hands back the command it produced,
// without running it.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	_, cmd := h.m.Update(msg)
	return cmd
}

// settle drains everything the pumps and the lazy opens have produced, and
// waits for an attach that is still in flight.
func (h *harness) settle() {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		select {
		case msg := <-h.m.events:
			h.m.Update(msg)
			continue
		default:
		}
		if !h.opening() {
			return
		}
		if time.Now().After(deadline) {
			h.t.Fatal("a pane never finished opening")
		}
		time.Sleep(time.Millisecond)
	}
}

func (h *harness) opening() bool {
	for _, p := range h.m.panes {
		if p.state == paneOpening {
			return true
		}
	}
	return false
}

// key sends one keystroke by its bubbletea spelling.
func (h *harness) key(s string) tea.Cmd {
	h.t.Helper()
	cmd := h.send(keyMsg(s))
	h.settle()
	return cmd
}

// prefix sends the prefix key followed by one command key.
func (h *harness) prefix(s string) tea.Cmd {
	h.t.Helper()
	h.send(keyMsg("ctrl+b"))
	cmd := h.send(keyMsg(s))
	h.settle()
	return cmd
}

// keyMsg spells a key the way a user's terminal would deliver it.
func keyMsg(s string) tea.KeyMsg {
	switch s {
	case "ctrl+b":
		return tea.KeyMsg{Type: tea.KeyCtrlB}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
}

// isQuit reports whether the command an Update returned is tea.Quit.
//
// It cannot simply call the command: anything that is not a quit is the model's
// own [muxModel.nextEvent], which blocks until a message arrives. A sentinel is
// queued first so that the non-quit case returns at once, and taken back when
// the quit case leaves it untouched.
func (h *harness) isQuit(cmd tea.Cmd) bool {
	h.t.Helper()
	if cmd == nil {
		return false
	}
	h.m.events <- paneOutputMsg{Index: -1}
	if _, ok := cmd().(tea.QuitMsg); ok {
		<-h.m.events
		return true
	}
	return false
}

// deliver feeds device output to a pane synchronously, bypassing the pump —
// the pump is exercised for real in the pty integration test.
func (h *harness) deliver(idx int, data string) {
	h.t.Helper()
	h.send(paneOutputMsg{Index: idx, Gen: h.m.panes[idx].gen, Data: []byte(data)})
}

func TestMuxOpensTheFirstDeviceOnly(t *testing.T) {
	h := newHarness(t, "pc1", "pc2", "r1")

	if h.m.panes[0].state != paneLive {
		t.Errorf("first pane state = %v, want live", h.m.panes[0].state)
	}
	// Lazy attach: a 50-device scenario must not open 50 exec streams before
	// the user has looked at one.
	for i := 1; i < 3; i++ {
		if h.m.panes[i].state != paneIdle {
			t.Errorf("pane %d state = %v, want idle until it is activated", i, h.m.panes[i].state)
		}
	}
}

func TestMuxTabSwitching(t *testing.T) {
	h := newHarness(t, "pc1", "pc2", "r1")

	for _, tc := range []struct {
		name string
		keys []string
		want int
	}{
		{"next", []string{"n"}, 1},
		{"next wraps", []string{"n", "n", "n"}, 0},
		{"previous wraps backwards", []string{"p"}, 2},
		{"by position", []string{"3"}, 2},
		{"tab is next", []string{"tab"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h.m.active = 0
			for _, k := range tc.keys {
				discard(h.prefix(k))
			}
			if h.m.active != tc.want {
				t.Errorf("active = %d, want %d", h.m.active, tc.want)
			}
		})
	}

	// Activating a tab opens its session, once.
	discard(h.prefix("2"))
	if h.m.panes[1].state != paneLive {
		t.Errorf("pane 2 state = %v, want live after activation", h.m.panes[1].state)
	}

	// A position with no device is refused rather than silently ignored.
	h.m.active = 0
	discard(h.prefix("9"))
	if h.m.active != 0 {
		t.Error("selecting a nonexistent device moved the active tab")
	}
	if !strings.Contains(h.m.status, "no device") {
		t.Errorf("status = %q, want it to name the missing device", h.m.status)
	}
}

func TestMuxKeysGoToTheActiveDevice(t *testing.T) {
	h := newHarness(t, "pc1", "pc2")

	h.key("i")
	h.key("p")
	h.key("enter")
	if got, want := h.session(0).input(), "ip\r"; got != want {
		t.Errorf("device input = %q, want %q", got, want)
	}

	// The prefix key itself is never forwarded…
	h.key("ctrl+b")
	if got := h.session(0).input(); got != "ip\r" {
		t.Errorf("the prefix key reached the device: %q", got)
	}
	// …unless it is doubled, which is how a user types a literal ctrl+b.
	h.key("ctrl+b")
	if got, want := h.session(0).input(), "ip\r\x02"; got != want {
		t.Errorf("send-prefix produced %q, want %q", got, want)
	}

	// Input goes to the *active* device and nowhere else.
	discard(h.prefix("2"))
	h.key("x")
	if got := h.session(1).input(); got != "x" {
		t.Errorf("second device input = %q, want %q", got, "x")
	}
	if got := h.session(0).input(); got != "ip\r\x02" {
		t.Errorf("first device received input meant for the second: %q", got)
	}
}

func TestMuxResizePropagatesToEveryPane(t *testing.T) {
	h := newHarness(t, "pc1", "pc2")
	discard(h.prefix("2")) // open the second session too

	h.send(tea.WindowSizeMsg{Width: 120, Height: 40})

	// The device gets the window minus the tab bar and the status line.
	const wantRows = 38
	for i := range h.sessions {
		size, ok := h.session(i).lastSize()
		if !ok {
			t.Fatalf("device %d was never resized", i)
		}
		if size != [2]uint16{120, wantRows} {
			t.Errorf("device %d size = %v, want [120 %d]", i, size, wantRows)
		}
	}
	// The screens agree, including the background one — a tab must not show a
	// stale geometry the moment it is selected.
	for i, p := range h.m.panes {
		if cols, rows := p.screen.Size(); cols != 120 || rows != wantRows {
			t.Errorf("pane %d screen = %d×%d, want 120×%d", i, cols, rows, wantRows)
		}
	}
}

func TestMuxResizeUsesColumnsFirst(t *testing.T) {
	// The axis-swap trap kathara/tty.go warns about: a 200×20 window must
	// reach the device as (cols=200, rows=18), not the other way round.
	h := newHarness(t, "pc1")
	h.send(tea.WindowSizeMsg{Width: 200, Height: 20})
	size, _ := h.session(0).lastSize()
	if size[0] != 200 || size[1] != 18 {
		t.Errorf("Resize got %v, want [200 18] (cols first)", size)
	}
}

func TestMuxDetach(t *testing.T) {
	h := newHarness(t, "pc1", "pc2")
	discard(h.prefix("2"))

	for _, key := range []string{"d", "q"} {
		t.Run("prefix "+key, func(t *testing.T) {
			cmd := h.prefix(key)
			if cmd == nil {
				t.Fatalf("ctrl+b %s produced no command", key)
			}
			if _, ok := cmd().(DetachMsg); !ok {
				t.Fatalf("ctrl+b %s did not produce a DetachMsg", key)
			}
		})
	}

	// DetachMsg quits the program; shutdown then closes every session, which
	// is what leaves the containers running and the streams released.
	if _, cmd := h.m.Update(DetachMsg{}); cmd == nil {
		t.Fatal("DetachMsg produced no command, want tea.Quit")
	}
	h.m.shutdown()
	for i := range h.sessions {
		if !h.session(i).isClosed() {
			t.Errorf("session %d still open after detach", i)
		}
	}
	// Idempotent: Run defers it and a test may call it too.
	h.m.shutdown()
}

func TestMuxScrollbackAndCopy(t *testing.T) {
	h := newHarness(t, "pc1")
	for i := 0; i < 30; i++ {
		h.deliver(0, "line"+string(rune('a'+i%26))+"\r\n")
	}

	if h.m.panes[0].screen.ScrollbackLen() == 0 {
		t.Fatal("no scrollback accrued")
	}

	discard(h.prefix("["))
	if h.m.mode != modeScroll {
		t.Fatalf("mode = %v, want scroll", h.m.mode)
	}

	// Moving up past the top of the viewport scrolls the pane rather than
	// running the cursor off it.
	for i := 0; i < 12; i++ {
		h.key("up")
	}
	if h.m.panes[0].offset == 0 {
		t.Error("moving above the viewport did not scroll the pane")
	}

	// Select three lines and copy them.
	h.key("v")
	h.key("down")
	h.key("down")
	h.key("y")

	if len(h.copied) != 1 {
		t.Fatalf("copied %d times, want 1", len(h.copied))
	}
	if got := strings.Count(h.copied[0], "\n"); got != 2 {
		t.Errorf("copied %d newlines, want 2 (three lines): %q", got, h.copied[0])
	}
	if strings.ContainsRune(h.copied[0], 0x1b) {
		t.Errorf("copied text contains escape sequences: %q", h.copied[0])
	}
	if h.m.mode != modeAttached {
		t.Error("copying did not leave scrollback mode")
	}
	if h.m.panes[0].offset != 0 {
		t.Error("copying did not return the pane to the live tail")
	}
	if !strings.Contains(h.m.status, "copied 3 lines") {
		t.Errorf("status = %q, want it to report the copy", h.m.status)
	}
}

// TestMuxScrollbackStaysPutUnderNewOutput: a device that keeps printing must
// not drag the view out from under someone reading its history. The offset
// counts from the bottom, so it has to grow by whatever the write added.
func TestMuxScrollbackStaysPutUnderNewOutput(t *testing.T) {
	h := newHarness(t, "pc1")
	for i := 0; i < 40; i++ {
		h.deliver(0, "old-"+strconv.Itoa(i)+"\r\n")
	}
	discard(h.prefix("["))
	for i := 0; i < 20; i++ {
		h.key("up")
	}
	if h.m.panes[0].offset == 0 {
		t.Fatal("the pane did not scroll up")
	}
	before := h.m.paneBody()

	// A routing daemon logging away while the user reads.
	for i := 0; i < 15; i++ {
		h.deliver(0, "new-"+strconv.Itoa(i)+"\r\n")
	}

	after := h.m.paneBody()
	if !slices.Equal(before, after) {
		t.Errorf("the scrollback view moved under new output:\nbefore %q\nafter  %q", before, after)
	}
	for _, line := range after {
		if strings.Contains(line, "new-") {
			t.Errorf("new output appeared in the scrolled-back view: %q", after)
			break
		}
	}
}

// TestMuxScrollModePrefixPagesUp: `ctrl+b ctrl+b` is send-prefix while attached,
// but in scrollback mode there is no device to send it to — the mode exists so
// that keys do not reach one — so it pages up instead of dropping the reader
// back to the tail with a stray 0x02 on the wire.
func TestMuxScrollModePrefixPagesUp(t *testing.T) {
	h := newHarness(t, "pc1")
	for i := 0; i < 40; i++ {
		h.deliver(0, "line-"+strconv.Itoa(i)+"\r\n")
	}
	discard(h.prefix("["))

	h.send(keyMsg("ctrl+b"))
	h.send(keyMsg("ctrl+b"))
	h.settle()

	if h.m.mode != modeScroll {
		t.Errorf("mode = %v, want scroll: the doubled prefix left scrollback", h.m.mode)
	}
	if h.m.panes[0].offset == 0 {
		t.Error("the doubled prefix did not page up")
	}
	if got := h.session(0).input(); got != "" {
		t.Errorf("scrollback mode wrote %q to the device", got)
	}
}

func TestMuxScrollModeSwallowsKeys(t *testing.T) {
	h := newHarness(t, "pc1")
	discard(h.prefix("["))
	h.key("j")
	h.key("k")
	if got := h.session(0).input(); got != "" {
		t.Errorf("scrollback-mode keys reached the device: %q", got)
	}
	h.key("esc")
	h.key("j")
	if got := h.session(0).input(); got != "j" {
		t.Errorf("after leaving scrollback the device input = %q, want %q", got, "j")
	}
}

func TestMuxCopyWithoutSelectionTakesTheVisiblePane(t *testing.T) {
	h := newHarness(t, "pc1")
	for i := 0; i < 5; i++ {
		h.deliver(0, "row\r\n")
	}
	discard(h.prefix("["))
	h.key("y")
	if len(h.copied) != 1 {
		t.Fatalf("copied %d times, want 1", len(h.copied))
	}
	if !strings.Contains(h.copied[0], "row") {
		t.Errorf("copy did not include the pane contents: %q", h.copied[0])
	}
}

func TestMuxPaneClosedAndReattach(t *testing.T) {
	h := newHarness(t, "pc1")

	h.send(paneClosedMsg{Index: 0, Gen: h.m.panes[0].gen})
	if h.m.panes[0].state != paneClosed {
		t.Fatalf("pane state = %v, want closed", h.m.panes[0].state)
	}
	// The pane says so where the user is looking, and the session is released.
	if !strings.Contains(h.m.panes[0].screen.String(), "session ended") {
		t.Errorf("pane does not report the end of the session:\n%s", h.m.panes[0].screen.String())
	}
	if !h.session(0).isClosed() {
		t.Error("the ended session was not closed")
	}
	// Keys are dropped rather than written to a dead session.
	h.key("x")

	// Re-attaching opens a fresh stream to a container that never stopped.
	h.replace(0)
	discard(h.prefix("r"))
	if h.m.panes[0].state != paneLive {
		t.Errorf("pane state after re-attach = %v, want live", h.m.panes[0].state)
	}
}

// TestMuxQuitsWhenEverySessionHasEndedCleanly is what makes `kathara connect`
// under the multiplexer end where the raw attach ended: type `exit` and the
// window goes with the shell (PORT_SPEC §3.3 item 4).
func TestMuxQuitsWhenEverySessionHasEndedCleanly(t *testing.T) {
	t.Run("the only session ends", func(t *testing.T) {
		h := newHarness(t, "pc1")
		if !h.isQuit(h.send(paneClosedMsg{Index: 0, Gen: h.m.panes[0].gen})) {
			t.Error("the last clean close did not quit the multiplexer")
		}
	})

	t.Run("a tab nobody opened keeps the window", func(t *testing.T) {
		h := newHarness(t, "pc1", "pc2")
		if h.isQuit(h.send(paneClosedMsg{Index: 0, Gen: h.m.panes[0].gen})) {
			t.Error("the window closed while pc2 had never been looked at")
		}
	})

	t.Run("a session that ended with an error keeps the window", func(t *testing.T) {
		h := newHarness(t, "pc1")
		closed := paneClosedMsg{Index: 0, Gen: h.m.panes[0].gen, Err: errors.New("stream reset")}
		if h.isQuit(h.send(closed)) {
			t.Error("the window closed over an error the user never got to read")
		}
	})
}

func TestMuxOpenFailureIsReportedInThePane(t *testing.T) {
	boom := errors.New("cannot attach: no such container")
	m, err := newMux(t.Context(), Config{Devices: []Device{{
		Name: "pc1",
		Open: func(context.Context) (Session, error) { return nil, boom },
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer m.shutdown()

	h := &harness{t: t, m: m}
	h.m.Init()
	h.settle()
	// A wide window: the pane does not reflow on resize, so a message written
	// at the default 80 columns would be cut by a narrower one.
	h.send(tea.WindowSizeMsg{Width: 100, Height: 20})

	if m.panes[0].state != paneClosed {
		t.Fatalf("pane state = %v, want closed after a failed attach", m.panes[0].state)
	}
	if !strings.Contains(m.panes[0].screen.String(), "no such container") {
		t.Errorf("the attach error is not visible in the pane:\n%s", m.panes[0].screen.String())
	}
	// The window stays up so the user can read the error and detach.
	if !strings.Contains(m.View(), "pc1") {
		t.Error("the failed device left the tab bar")
	}
}

func TestMuxViewLayout(t *testing.T) {
	h := newHarness(t, "pc1", "pc2")
	h.deliver(0, "hello from pc1")

	view := h.m.View()
	lines := strings.Split(view, "\n")
	if len(lines) != 10 {
		t.Fatalf("view has %d lines, want exactly the window height 10", len(lines))
	}
	if !strings.Contains(lines[0], "1:pc1") || !strings.Contains(lines[0], "2:pc2") {
		t.Errorf("tab bar = %q, want both devices", lines[0])
	}
	if !strings.Contains(lines[1], "hello from pc1") {
		t.Errorf("first pane line = %q, want the device output", lines[1])
	}
	if !strings.Contains(lines[9], "detach") {
		t.Errorf("status line = %q, want the detach hint", lines[9])
	}

	// Help replaces the pane, and any key returns.
	discard(h.prefix("?"))
	if !strings.Contains(h.m.View(), "ctrl+b d") {
		t.Error("help does not document detach")
	}
	h.key("x")
	if h.m.mode != modeAttached {
		t.Error("help did not exit on a keypress")
	}
	if got := h.session(0).input(); got != "" {
		t.Errorf("the key that closed help was also sent to the device: %q", got)
	}
}

func TestMuxNoDevicesIsAnError(t *testing.T) {
	if _, err := newMux(t.Context(), Config{}); !errors.Is(err, ErrNoDevices) {
		t.Errorf("newMux with no devices = %v, want ErrNoDevices", err)
	}
}

func TestEncodeKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  tea.KeyMsg
		want string
	}{
		{"runes", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("ls")}, "ls"},
		{"utf-8 runes", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("é")}, "é"},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, " "},
		{"enter is CR, not LF", tea.KeyMsg{Type: tea.KeyEnter}, "\r"},
		{"backspace is DEL", tea.KeyMsg{Type: tea.KeyBackspace}, "\x7f"},
		{"tab", tea.KeyMsg{Type: tea.KeyTab}, "\t"},
		{"ctrl+c reaches the device as a byte", tea.KeyMsg{Type: tea.KeyCtrlC}, "\x03"},
		{"ctrl+d", tea.KeyMsg{Type: tea.KeyCtrlD}, "\x04"},
		{"esc", tea.KeyMsg{Type: tea.KeyEsc}, "\x1b"},
		{"up", tea.KeyMsg{Type: tea.KeyUp}, "\x1b[A"},
		{"left", tea.KeyMsg{Type: tea.KeyLeft}, "\x1b[D"},
		{"home", tea.KeyMsg{Type: tea.KeyHome}, "\x1b[H"},
		{"page up", tea.KeyMsg{Type: tea.KeyPgUp}, "\x1b[5~"},
		{"delete", tea.KeyMsg{Type: tea.KeyDelete}, "\x1b[3~"},
		{"shift+tab", tea.KeyMsg{Type: tea.KeyShiftTab}, "\x1b[Z"},
		// SPIKES/windows-terminal.md §9 row 4: modern xterm SS3, not Python's
		// legacy ESC[11~ KEYCODES table.
		{"F1 is SS3", tea.KeyMsg{Type: tea.KeyF1}, "\x1bOP"},
		{"F4 is SS3", tea.KeyMsg{Type: tea.KeyF4}, "\x1bOS"},
		{"F5 is CSI", tea.KeyMsg{Type: tea.KeyF5}, "\x1b[15~"},
		{"F12", tea.KeyMsg{Type: tea.KeyF12}, "\x1b[24~"},
		{"ctrl+right", tea.KeyMsg{Type: tea.KeyCtrlRight}, "\x1b[1;5C"},
		{"alt prefixes ESC", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Alt: true}, "\x1bf"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(encodeKey(tc.key)); got != tc.want {
				t.Errorf("encodeKey = %q, want %q", got, tc.want)
			}
		})
	}

	// A key with no known encoding sends nothing rather than a guess: a wrong
	// sequence in a router CLI is worse than a missing one.
	if got := encodeKey(tea.KeyMsg{Type: tea.KeyType(-9999)}); got != nil {
		t.Errorf("unknown key encoded as %q, want nothing", got)
	}
}

// discard runs a command that is known not to block and drops its message.
// Most prefix commands return nil; the ones that do not (detach) are asserted
// on directly by the tests that care.
func discard(tea.Cmd) {}

// TestShutdownAbandonsAnAttachStillInFlight: detaching while a tab is still
// "opening…" must not hold the process behind the backend call.
//
// A pane's Open is `ConnectTTYObj`, which runs the startup-execution wait, and
// that wait has no deadline of its own (RULINGS.md:96 removed the ENTER
// override). Without the multiplexer cancelling its own context, `ctrl+b d`
// would leave the user staring at a restored shell while `Run` sat in
// `wg.Wait` until the daemon answered — or forever.
func TestShutdownAbandonsAnAttachStillInFlight(t *testing.T) {
	defer goleak.VerifyNone(t)

	opening := make(chan struct{})
	m, err := newMux(t.Context(), Config{Devices: []Device{{
		Name: "pc1",
		Open: func(ctx context.Context) (Session, error) {
			close(opening)
			<-ctx.Done() // a daemon that never answers
			return nil, ctx.Err()
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	m.Init() // activates the first tab, which starts the attach
	<-opening

	done := make(chan struct{})
	go func() {
		m.shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("shutdown blocked on an attach that was still in flight")
	}
}

// TestShutdownClosesASessionTheProgramNeverSaw: the attach that lands after the
// update loop has stopped belongs to nobody, and it is a stream on the daemon
// until someone closes it. The open goroutine records the session before it
// announces it, so shutdown can.
func TestShutdownClosesASessionTheProgramNeverSaw(t *testing.T) {
	defer goleak.VerifyNone(t)

	sess := newFakeSession()
	release := make(chan struct{})
	m, err := newMux(t.Context(), Config{Devices: []Device{{
		Name: "pc1",
		Open: func(context.Context) (Session, error) {
			<-release
			return sess, nil
		},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	m.Init()

	// Nobody is reading the event channel: this is the window between the
	// program stopping and shutdown running.
	close(release)
	m.shutdown()

	if !sess.isClosed() {
		t.Error("a session opened after the last update was left open on the daemon")
	}
}

// TestRunDetachesAndClosesEverything drives the real bubbletea program, not
// just the model: input arrives as bytes on a pipe, the view is rendered to a
// buffer, and `ctrl+b d` has to come back out the other side as a clean return
// with every session closed and every goroutine finished.
//
// It is the one test that covers [Run] itself — the alt-screen setup, the
// renderer, and the shutdown ordering that makes detach leave containers
// running.
func TestRunDetachesAndClosesEverything(t *testing.T) {
	defer goleak.VerifyNone(t)

	sess := newFakeSession()
	in, inw := io.Pipe()
	var out bytes.Buffer

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Devices: []Device{{
				Name: "pc1",
				Open: func(context.Context) (Session, error) { return sess, nil },
			}},
			Input:  in,
			Output: &out,
			Copy:   func(string) error { return nil },
		})
	}()

	// ctrl+b then d. The write is on its own goroutine's schedule, so the
	// program may still be starting: the pipe write blocks until it reads.
	if _, err := inw.Write([]byte{0x02, 'd'}); err != nil {
		t.Fatalf("write to the program's input: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want a clean detach", err)
		}
	case <-ctx.Done():
		t.Fatal("Run did not return after ctrl+b d")
	}
	if err := inw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if !sess.isClosed() {
		t.Error("detach left the device session open")
	}
	if out.Len() == 0 {
		t.Error("the program rendered nothing")
	}
}
