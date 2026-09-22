package term

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
)

// Device is one tab: a name and a way to open its session.
type Device struct {
	// Name is the device name, shown in the tab bar. It is also what a user
	// types after `kathara connect`.
	Name string

	// Open opens the session for this device. It is called at most once per
	// attach, on the first activation of the tab, and may block — the
	// multiplexer shows the tab as "opening…" while it does.
	// The ctx passed is the multiplexer's own, which is cancelled when the
	// multiplexer stops for any reason: a Ctrl-C during a slow attach cancels
	// it, and so does a detach, which is what stops `ctrl+b d` from blocking
	// behind a backend call that may take minutes (the startup-execution wait
	// of `ConnectTTY`).
	Open func(ctx context.Context) (Session, error)
}

// Config configures one multiplexer window.
type Config struct {
	// Devices are the tabs, in order. A multiplexer with no devices is an
	// error rather than an empty window with nothing to detach from.
	Devices []Device

	// Input and Output default to os.Stdin and os.Stdout. They exist so that
	// a test can drive the program without a terminal.
	Input  io.Reader
	Output io.Writer

	// Scrollback is the retained history per device; zero means
	// [DefaultScrollback].
	Scrollback int

	// Copy receives text the user yanks. Nil installs the OSC 52 writer over
	// Output, which puts the text in the clipboard of the terminal the user is
	// actually sitting at — including through SSH and through tmux — with no
	// helper binary and no clipboard dependency.
	Copy func(text string) error

	// Title, when non-empty, is set as the terminal window title for the
	// duration of the session.
	Title string
}

// ErrNoDevices is returned by [Run] for an empty device list. It is internal
// to `term`: callers reach the multiplexer only after deciding they have
// devices to show.
var ErrNoDevices = errors.New("term: multiplexer started with no devices")

// Run opens the multiplexer and blocks until the user detaches, every device
// that was attached has ended its session cleanly, or ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	m, err := newMux(ctx, cfg)
	if err != nil {
		return err
	}
	defer m.shutdown()

	opts := []tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithContext(ctx),
		tea.WithInput(m.input),
		tea.WithOutput(m.output),
	}
	p := tea.NewProgram(m, opts...)
	if _, err := p.Run(); err != nil {

		if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
			return nil
		}
		return err
	}
	return nil
}

// newMux builds the model and its plumbing without starting a bubbletea
// program, which is what lets the model be table-tested headless and driven
// from a real pty in the integration test.
func newMux(ctx context.Context, cfg Config) (*muxModel, error) {
	if len(cfg.Devices) == 0 {
		return nil, ErrNoDevices
	}
	input := cfg.Input
	if input == nil {
		input = os.Stdin
	}
	output := cfg.Output
	if output == nil {
		output = os.Stdout
	}
	scrollback := cfg.Scrollback
	if scrollback == 0 {
		scrollback = DefaultScrollback
	}

	// The multiplexer's own context, not the caller's: shutdown cancels it, so
	// an attach that is still in flight when the user detaches is abandoned
	// instead of holding the whole process in `wg.Wait` until the daemon
	// answers.
	muxCtx, cancel := context.WithCancel(ctx)

	m := &muxModel{
		ctx:        muxCtx,
		cancel:     cancel,
		input:      input,
		output:     output,
		title:      cfg.Title,
		scrollback: scrollback,
		events:     make(chan tea.Msg, 64),
		done:       make(chan struct{}),
		// Until the first WindowSizeMsg arrives the panes are the classic
		// 80×24, so a device that writes before the size is known does not
		// land on a one-column screen.
		width:  80,
		height: 24,
	}
	m.copy = cfg.Copy
	if m.copy == nil {
		m.copy = osc52Writer(output)
	}
	for _, d := range cfg.Devices {
		m.panes = append(m.panes, &pane{
			name:   d.Name,
			open:   d.Open,
			screen: NewScreen(80, 22, scrollback),
		})
	}
	return m, nil
}

// shutdown closes every session and waits for the pumps. It is idempotent.
func (m *muxModel) shutdown() {
	m.closeOnce.Do(func() {
		// Order matters, and each step unblocks the next:
		//  1. close(done) releases a pump that is mid-emit, so it does not
		//     deadlock against a program that has stopped reading the channel;
		//  2. cancel() releases an attach that is still in flight, so a detach
		//     during a slow `ConnectTTY` does not hold the process here;
		//  3. closing the live sessions releases a pump blocked in Read.
		// Only then can the WaitGroup be waited on.
		close(m.done)
		m.cancel()
		for _, p := range m.panes {
			if p.session != nil {
				_ = p.session.Close()
			}
		}
		m.wg.Wait()

		// Every session this multiplexer ever opened, closed once more. Close
		// is idempotent by contract (`kathara.TTYSession`), and this is what
		// covers the session whose attach landed after the update loop had
		// stopped: draining the event channel cannot do it, because a stale
		// `nextEvent` command goroutine outlives the program and can take that
		// message off the channel first (bubbletea keeps command goroutines
		// running until their Cmd returns). A registry has no such race —
		// the entry is recorded before the message is emitted.
		m.openedMu.Lock()
		opened := m.opened
		m.opened = nil
		m.openedMu.Unlock()
		for _, s := range opened {
			_ = s.Close()
		}
	})
}

// osc52Writer is the default clipboard sink: an OSC 52 sequence written
// straight to the program's output.
func osc52Writer(w io.Writer) func(string) error {
	return func(text string) error {
		enc := base64.StdEncoding.EncodeToString([]byte(text))
		_, err := io.WriteString(w, "\x1b]52;c;"+enc+"\a")
		return err
	}
}

// emit hands a message to the update loop, or gives up if the multiplexer is
// shutting down. It is the only way a pump goroutine may talk to the model.
func (m *muxModel) emit(msg tea.Msg) bool {
	select {
	case m.events <- msg:
		return true
	case <-m.done:
		return false
	}
}

// nextEvent is the Cmd that feeds pump output into the update loop. It is
// re-issued on every message, which is bubbletea's channel-consumer idiom.
func (m *muxModel) nextEvent() tea.Cmd {
	return func() tea.Msg {
		select {
		case msg := <-m.events:
			return msg
		case <-m.done:
			return nil
		}
	}
}

// pump copies one session's output into the update loop until it ends.
func (m *muxModel) pump(idx, gen int, s Session) {
	defer m.wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := s.Read(buf)
		if n > 0 {
			// The buffer is reused, so the message must own its bytes.
			data := make([]byte, n)
			copy(data, buf[:n])
			if !m.emit(paneOutputMsg{Index: idx, Gen: gen, Data: data}) {
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			m.emit(paneClosedMsg{Index: idx, Gen: gen, Err: err})
			return
		}
	}
}

// openSession runs one device's Open off the update loop. A backend attach is
// a network round trip and must not freeze the window.
func (m *muxModel) openSession(idx, gen int) {
	defer m.wg.Done()
	p := m.panes[idx]
	s, err := p.open(m.ctx)
	if err != nil {
		m.emit(paneOpenedMsg{Index: idx, Gen: gen, Err: err})
		return
	}
	// Recorded before it is announced, which is what makes [muxModel.shutdown]
	// able to close it whatever happens to the message.
	m.openedMu.Lock()
	m.opened = append(m.opened, s)
	m.openedMu.Unlock()
	if !m.emit(paneOpenedMsg{Index: idx, Gen: gen, Session: s}) {
		// The multiplexer went away while the attach was in flight; the
		// session is ours to close or it leaks a stream.
		_ = s.Close()
	}
}

// muxModel is the bubbletea model. Pointer receivers throughout: it owns
// channels and a WaitGroup, which a value model would copy.
type muxModel struct {
	ctx    context.Context
	cancel context.CancelFunc
	input  io.Reader
	output io.Writer
	title  string

	panes  []*pane
	active int

	width, height int
	scrollback    int

	mode   uiMode
	prefix bool
	status string

	sel selection

	copy func(string) error

	events chan tea.Msg
	done   chan struct{}
	wg     sync.WaitGroup

	// opened is every session [muxModel.openSession] has handed over, the
	// authoritative list for shutdown. It is written by the open goroutines
	// and read once, after they have all finished.
	openedMu sync.Mutex
	opened   []Session

	closeOnce sync.Once
}

var _ tea.Model = (*muxModel)(nil)

// pane is one device's state.
type pane struct {
	name   string
	open   func(context.Context) (Session, error)
	screen *Screen

	session Session
	state   paneState
	err     error

	// gen counts attaches. It is what tells a message from the session a pane
	// is showing now apart from one an earlier session left in flight.
	gen int

	// offset is how many lines above the bottom the view is pinned, so 0 is
	// "follow the output".
	offset int
}

type paneState int

const (
	paneIdle paneState = iota
	paneOpening
	paneLive
	paneClosed
)

type uiMode int

const (
	// modeAttached forwards keys to the device.
	modeAttached uiMode = iota
	// modeScroll reads scrollback and selects lines to copy.
	modeScroll
	// modeHelp shows the key bindings.
	modeHelp
)

// selection is the copy-mode line range, in absolute [Screen] line
// coordinates.
type selection struct {
	active bool
	anchor int
	cursor int
}

func (s selection) bounds() (int, int) {
	if s.anchor <= s.cursor {
		return s.anchor, s.cursor
	}
	return s.cursor, s.anchor
}

// Messages the model exchanges with its pumps. They are unexported, like the
// model itself: everything outside this package talks to the multiplexer
// through [Config] and [Run], and the only message an embedder needs a name for
// is [DetachMsg].
type (
	// paneOutputMsg is a chunk of device output.
	paneOutputMsg struct {
		Index int
		Gen   int
		Data  []byte
	}
	// paneOpenedMsg reports the result of a lazy attach.
	paneOpenedMsg struct {
		Index   int
		Gen     int
		Session Session
		Err     error
	}
	// paneClosedMsg reports that a device's session ended.
	paneClosedMsg struct {
		Index int
		Gen   int
		Err   error
	}
)

type DetachMsg struct{}
