// The bubbletea model behind the built-in multiplexer (PORT_SPEC §3.3 item 1).
// See mux.go for the shape and the detach contract.
//
// # Key bindings (the "documented keybinding" §3.3 item 1 asks for)
//
// Everything the user types goes to the device except a prefix key, exactly as
// tmux does it — a multiplexer that stole plain keys would make `Ctrl-C` in a
// router CLI unreachable.
//
//	ctrl+b          prefix
//	ctrl+b ctrl+b   send a literal ctrl+b to the device
//	ctrl+b d,q      detach (containers keep running)
//	ctrl+b n,p      next / previous device
//	ctrl+b 0..9     device by position
//	ctrl+b r        re-attach a device whose session ended
//	ctrl+b [        scrollback / copy mode
//	ctrl+b ?        help
//
// In scrollback mode:
//
//	up/down k/j     move            pgup/pgdn   page (ctrl+b also pages up)
//	home/end g/G    ends            v           start / clear a line selection
//	y               copy selection (or the whole visible pane) and leave
//	esc/q           leave

package term

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// prefixKey is the one key the multiplexer keeps for itself.
const prefixKey = tea.KeyCtrlB

var (
	activeTabStyle   = lipgloss.NewStyle().Reverse(true).Bold(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
	closedTabStyle   = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	statusStyle      = lipgloss.NewStyle().Faint(true)
	// Styling is bold/faint/reverse only, never a colour: the port pins
	// lipgloss's background detection off (`internal/charmguard`), so an
	// adaptive colour would be picked against a guessed background.
	selectedLineStyle = lipgloss.NewStyle().Reverse(true)
)

func (m *muxModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.nextEvent(), m.activate(0)}
	if m.title != "" {
		cmds = append(cmds, tea.SetWindowTitle(m.title))
	}
	return tea.Batch(cmds...)
}

// activate switches to a tab, opening its session on first use.
func (m *muxModel) activate(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.panes) {
		return nil
	}
	m.active = idx
	p := m.panes[idx]
	if p.state != paneIdle {
		return nil
	}
	p.state = paneOpening
	p.gen++
	m.wg.Add(1)
	go m.openSession(idx, p.gen)
	return nil
}

func (m *muxModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m, m.resize(msg.Width, msg.Height)

	case tea.KeyMsg:
		return m, m.key(msg)

	case paneOutputMsg:
		p := m.paneAt(msg.Index)
		if p != nil && p.gen == msg.Gen {
			// A pane the user has scrolled up in stays put. The offset counts
			// lines from the bottom of the coordinate space, so it has to grow
			// by exactly what the write added to that space — otherwise a
			// chatty device drags the view out from under someone reading it.
			//
			// A saturated scrollback adds nothing (the oldest line drops as
			// the newest arrives), so the delta is zero and the view slides,
			// which is unavoidable: the lines it was showing no longer exist.
			before := p.screen.TotalLines()
			_, _ = p.screen.Write(msg.Data)
			if p.offset > 0 {
				total := p.screen.TotalLines()
				p.offset = clamp(p.offset+total-before, 0, maxInt(0, total-m.paneRows()))
			}
		}
		return m, m.nextEvent()

	case paneOpenedMsg:
		p := m.paneAt(msg.Index)
		if p == nil || p.gen != msg.Gen {
			// A session that arrived for a pane that has since moved on. It is
			// ours to close or it leaks a stream.
			if msg.Session != nil {
				_ = msg.Session.Close()
			}
			return m, m.nextEvent()
		}
		if msg.Err != nil {
			p.state = paneClosed
			p.err = msg.Err
			m.writeSystem(p, "cannot attach to "+p.name+": "+msg.Err.Error())
			return m, m.nextEvent()
		}
		p.session = msg.Session
		p.state = paneLive
		cols, rows := p.screen.Size()
		_ = p.session.Resize(uint16(cols), uint16(rows))
		m.wg.Add(1)
		go m.pump(msg.Index, msg.Gen, msg.Session)
		return m, m.nextEvent()

	case paneClosedMsg:
		p := m.paneAt(msg.Index)
		if p == nil || p.gen != msg.Gen {
			// The previous session's EOF, arriving after a re-attach.
			return m, m.nextEvent()
		}
		p.state = paneClosed
		p.err = msg.Err
		if p.session != nil {
			_ = p.session.Close()
			p.session = nil
		}
		if msg.Err != nil {
			m.writeSystem(p, "session ended: "+msg.Err.Error())
		} else {
			m.writeSystem(p, "session ended. ctrl+b r re-attaches, ctrl+b d detaches.")
		}
		if m.everySessionEndedCleanly() {
			return m, tea.Quit
		}
		return m, m.nextEvent()

	case DetachMsg:
		return m, tea.Quit
	}

	return m, nil
}

// everySessionEndedCleanly reports whether there is nothing left to look at:
// every tab has been attached and every one of those sessions has since ended
// without an error.
//
// It is what closes a one-tab multiplexer when the user types `exit` in the
// shell, which is the behaviour `kathara connect` had before the multiplexer
// became its renderer (PORT_SPEC §3.3 item 4, "unchanged behaviour"). Two
// deliberate exclusions: a tab that was never activated (its device is still
// there to be looked at, so the window stays) and a tab that ended with an
// error (the user has to be able to read it — `TestMuxOpenFailureIsReportedInThePane`).
func (m *muxModel) everySessionEndedCleanly() bool {
	for _, p := range m.panes {
		if p.state != paneClosed || p.err != nil {
			return false
		}
	}
	return true
}

// resize propagates a window size to every pane and, for the live ones, to the
// device behind it.
//
// Every pane is resized, not only the visible one: a device whose tab is in
// the background must not discover a stale geometry the moment it is shown,
// which is the same "size before I/O" property the Unix console adapter has
// always had (SPIKES/windows-terminal.md §7, OQ-18).
func (m *muxModel) resize(width, height int) tea.Cmd {
	if width < 1 || height < 1 {
		return nil
	}
	m.width, m.height = width, height
	rows := maxInt(1, height-2)
	for _, p := range m.panes {
		p.screen.Resize(width, rows)
		if p.session != nil {
			_ = p.session.Resize(uint16(width), uint16(rows))
		}
	}
	return nil
}

func (m *muxModel) paneAt(i int) *pane {
	if i < 0 || i >= len(m.panes) {
		return nil
	}
	return m.panes[i]
}

func (m *muxModel) current() *pane { return m.panes[m.active] }

// writeSystem puts a multiplexer message in a pane's own scrollback, so it
// lands where the user is looking rather than on a status line they may have
// already replaced.
func (m *muxModel) writeSystem(p *pane, text string) {
	_, _ = p.screen.Write([]byte("\r\n\x1b[0m[kathara] " + text + "\r\n"))
}

// key routes one keystroke.
func (m *muxModel) key(k tea.KeyMsg) tea.Cmd {
	if m.prefix {
		m.prefix = false
		return m.prefixCommand(k)
	}
	if k.Type == prefixKey && !k.Alt {
		m.prefix = true
		m.status = "prefix"
		return nil
	}

	switch m.mode {
	case modeHelp:
		m.mode = modeAttached
		m.status = ""
		return nil
	case modeScroll:
		return m.scrollKey(k)
	default:
		return m.sendKey(k)
	}
}

// sendKey forwards a keystroke to the active device.
func (m *muxModel) sendKey(k tea.KeyMsg) tea.Cmd {
	p := m.current()
	// Typing into a pane always returns it to the live tail; nothing is more
	// confusing than a shell that echoes somewhere off-screen.
	p.offset = 0
	if p.state != paneLive || p.session == nil {
		return nil
	}
	b := encodeKey(k)
	if len(b) == 0 {
		return nil
	}
	if _, err := p.session.Write(b); err != nil {
		return m.closePane(m.active, err)
	}
	return nil
}

func (m *muxModel) closePane(idx int, err error) tea.Cmd {
	gen := m.panes[idx].gen
	return func() tea.Msg { return paneClosedMsg{Index: idx, Gen: gen, Err: err} }
}

// prefixCommand handles the key after the prefix.
func (m *muxModel) prefixCommand(k tea.KeyMsg) tea.Cmd {
	m.status = ""

	if k.Type == prefixKey {
		if m.mode == modeScroll {
			// In scrollback mode there is no device to send anything to — the
			// whole point of the mode is that keys do not reach it — so the
			// doubled prefix is the page-up tmux's copy-mode gives it, and not
			// a silent drop back to the tail with a stray 0x02 on the wire.
			return m.scrollKey(k)
		}
		// prefix prefix sends the literal byte, as `send-prefix` does.
		m.mode = modeAttached
		return m.sendKey(k)
	}

	switch k.String() {
	case "d", "q":
		return func() tea.Msg { return DetachMsg{} }
	case "n", "right", "tab":
		m.mode = modeAttached
		return m.activate((m.active + 1) % len(m.panes))
	case "p", "left", "shift+tab":
		m.mode = modeAttached
		return m.activate((m.active - 1 + len(m.panes)) % len(m.panes))
	case "[":
		m.enterScroll()
		return nil
	case "?":
		m.mode = modeHelp
		return nil
	case "r":
		return m.reattach()
	}

	if n, err := strconv.Atoi(k.String()); err == nil && n >= 0 && n <= 9 {
		// Tabs are numbered from 1 in the bar, and 0 selects the tenth, which
		// is the convention every multiplexer uses.
		idx := n - 1
		if n == 0 {
			idx = 9
		}
		if idx < len(m.panes) {
			m.mode = modeAttached
			return m.activate(idx)
		}
		m.status = "no device " + k.String()
	}
	return nil
}

// reattach re-opens a session whose device ended, without restarting the
// multiplexer. The container was never touched, so this is a fresh attach to a
// device that is still running.
func (m *muxModel) reattach() tea.Cmd {
	p := m.current()
	if p.state != paneClosed {
		m.status = "device " + p.name + " is already attached"
		return nil
	}
	p.state = paneIdle
	p.err = nil
	m.writeSystem(p, "re-attaching to "+p.name+"…")
	return m.activate(m.active)
}

func (m *muxModel) enterScroll() {
	m.mode = modeScroll
	p := m.current()
	m.sel = selection{}
	// Start at the bottom of what is visible, which is where the user's eye is.
	m.sel.cursor = maxInt(0, p.screen.TotalLines()-1-p.offset)
	m.status = "scrollback — v select, y copy, esc leave"
}

func (m *muxModel) scrollKey(k tea.KeyMsg) tea.Cmd {
	p := m.current()
	rows := m.paneRows()
	total := p.screen.TotalLines()

	move := func(delta int) {
		m.sel.cursor = clamp(m.sel.cursor+delta, 0, maxInt(0, total-1))
		if !m.sel.active {
			m.sel.anchor = m.sel.cursor
		}
		// Keep the cursor inside the viewport by pushing the offset.
		bottom := total - 1 - p.offset
		top := bottom - rows + 1
		switch {
		case m.sel.cursor > bottom:
			p.offset = maxInt(0, total-1-m.sel.cursor)
		case m.sel.cursor < top:
			p.offset = clamp(total-rows-m.sel.cursor, 0, maxInt(0, total-rows))
		}
	}

	switch k.String() {
	case "esc", "q":
		m.mode = modeAttached
		m.sel = selection{}
		m.status = ""
		p.offset = 0
	case "up", "k":
		move(-1)
	case "down", "j":
		move(1)
	case "pgup", "ctrl+b":
		move(-rows)
	case "pgdown", "ctrl+f", " ":
		move(rows)
	case "home", "g":
		move(-total)
	case "end", "G":
		move(total)
	case "v":
		m.sel.active = !m.sel.active
		m.sel.anchor = m.sel.cursor
		if m.sel.active {
			m.status = "selecting — y copies"
		} else {
			m.status = "selection cleared"
		}
	case "y":
		m.yank()
	}
	return nil
}

// yank copies the selection, or the whole visible pane when nothing is
// selected, and leaves scrollback mode.
func (m *muxModel) yank() {
	p := m.current()
	total := p.screen.TotalLines()
	rows := m.paneRows()

	from, to := m.sel.bounds()
	if !m.sel.active {
		to = maxInt(0, total-1-p.offset)
		from = maxInt(0, to-rows+1)
	}
	text := p.screen.Text(from, to+1)

	m.mode = modeAttached
	m.sel = selection{}
	p.offset = 0

	if err := m.copy(text); err != nil {
		m.status = "copy failed: " + err.Error()
		return
	}
	n := to - from + 1
	m.status = fmt.Sprintf("copied %d line%s", n, plural(n))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// paneRows is the height available to a device, i.e. the window minus the tab
// bar and the status line.
func (m *muxModel) paneRows() int { return maxInt(1, m.height-2) }

func (m *muxModel) View() string {
	var b strings.Builder
	b.WriteString(m.tabBar())
	b.WriteByte('\n')

	if m.mode == modeHelp {
		b.WriteString(m.helpBody())
	} else {
		b.WriteString(strings.Join(m.paneBody(), "\n"))
	}
	b.WriteByte('\n')
	b.WriteString(m.statusBar())
	return b.String()
}

// paneBody renders exactly paneRows lines of the active device.
func (m *muxModel) paneBody() []string {
	p := m.current()
	rows := m.paneRows()
	total := p.screen.TotalLines()

	bottom := total - p.offset
	top := maxInt(0, bottom-rows)
	// Follow mode shows the live screen, which is the tail of the coordinate
	// space; the cursor is only drawn there.
	showCursor := m.mode == modeAttached && p.offset == 0 && p.state == paneLive
	lines := p.screen.RenderView(top, bottom, showCursor)

	if m.mode == modeScroll {
		lines = m.highlightSelection(lines, top)
	}

	for len(lines) < rows {
		lines = append(lines, "")
	}
	return lines[:rows]
}

func (m *muxModel) highlightSelection(lines []string, top int) []string {
	from, to := m.sel.bounds()
	if !m.sel.active {
		from, to = m.sel.cursor, m.sel.cursor
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		abs := top + i
		if abs < from || abs > to {
			out[i] = line
			continue
		}
		if line == "" {
			line = " "
		}
		// The line already carries the device's own SGR runs; reset first so
		// the highlight is not merged into whatever colour ended the line.
		out[i] = selectedLineStyle.Render(stripANSI(line))
	}
	return out
}

// stripANSI removes escape sequences from a rendered line. It is used only
// where the multiplexer must impose its own attribute over the whole line
// (selection highlight), because nesting reverse-video inside arbitrary device
// SGR state produces different results on different terminals.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
				i = j
				continue
			}
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func (m *muxModel) tabBar() string {
	labels := make([]string, len(m.panes))
	for i, p := range m.panes {
		label := strconv.Itoa(i+1) + ":" + p.name
		if title := p.screen.Title(); title != "" && title != p.name {
			label += " (" + title + ")"
		}
		switch {
		case i == m.active:
			labels[i] = activeTabStyle.Render(" " + label + " ")
		case p.state == paneClosed:
			labels[i] = closedTabStyle.Render(" " + label + " ")
		default:
			labels[i] = inactiveTabStyle.Render(" " + label + " ")
		}
	}
	return truncateToWidth(joinAround(labels, m.active, m.width), m.width)
}

// joinAround concatenates labels, dropping the ones that do not fit while
// always keeping the active tab visible. A 40-device scenario would otherwise
// push the tab the user is looking at off the end of the bar.
func joinAround(labels []string, active, width int) string {
	if width <= 0 {
		return strings.Join(labels, "")
	}
	lo, hi := active, active+1
	total := lipgloss.Width(labels[active])
	for lo > 0 || hi < len(labels) {
		grew := false
		if hi < len(labels) && total+lipgloss.Width(labels[hi]) <= width {
			total += lipgloss.Width(labels[hi])
			hi++
			grew = true
		}
		if lo > 0 && total+lipgloss.Width(labels[lo-1]) <= width {
			lo--
			total += lipgloss.Width(labels[lo])
			grew = true
		}
		if !grew {
			break
		}
	}
	return strings.Join(labels[lo:hi], "")
}

func (m *muxModel) statusBar() string {
	p := m.current()
	var left string
	switch {
	case m.prefix:
		left = "prefix"
	case m.mode == modeScroll:
		left = "scrollback"
	case m.mode == modeHelp:
		left = "help — any key returns"
	case p.state == paneOpening:
		left = "attaching to " + p.name + "…"
	case p.state == paneClosed:
		left = p.name + " detached — ctrl+b r re-attach, ctrl+b d detach"
	default:
		left = "ctrl+b ? keys · ctrl+b d detach"
	}
	if m.status != "" && m.mode != modeHelp {
		left = m.status + " · " + left
	}
	if m.mode == modeScroll {
		left += fmt.Sprintf(" · line %d/%d", m.sel.cursor+1, p.screen.TotalLines())
	}
	return statusStyle.Render(truncateToWidth(left, m.width))
}

func (m *muxModel) helpBody() string {
	rows := []string{
		"kathara built-in terminal multiplexer",
		"",
		"  ctrl+b          prefix",
		"  ctrl+b ctrl+b   send a literal ctrl+b to the device",
		"  ctrl+b d / q    detach — the devices keep running",
		"  ctrl+b n / p    next / previous device",
		"  ctrl+b 1..9,0   select a device by position",
		"  ctrl+b r        re-attach a device whose session ended",
		"  ctrl+b [        scrollback and copy mode",
		"  ctrl+b ?        this help",
		"",
		"  scrollback: up/down k/j move · pgup/pgdn page (ctrl+b pages up) · g/G ends",
		"              v select lines · y copy · esc leave",
		"",
		"  Detaching leaves every container running. `kathara connect <device>`",
		"  re-attaches, and `kathara lclean` is what actually stops them.",
	}
	for len(rows) < m.paneRows() {
		rows = append(rows, "")
	}
	return strings.Join(rows[:m.paneRows()], "\n")
}

// truncateToWidth cuts a rendered string to width display cells, counting the
// way the terminal does (escape sequences are free).
func truncateToWidth(s string, width int) string {
	if width <= 0 || lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	used := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := i + 1
			if j < len(s) && s[j] == '[' {
				j++
				for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
					j++
				}
				if j < len(s) {
					j++
				}
				b.WriteString(s[i:j])
				i = j
				continue
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		w := runeWidth(r)
		if used+w > width {
			break
		}
		b.WriteRune(r)
		used += w
		i += size
	}
	b.WriteString("\x1b[0m")
	return b.String()
}
