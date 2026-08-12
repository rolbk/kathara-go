// This file is the pane engine of the PORT_SPEC §3.3 item 1 rebuild: the VT
// screen model each multiplexer pane draws its device onto
// (docs/port/SPIKES/windows-terminal.md W6-8, "pane = Pty + VT screen model
// under bubbletea").
//
// It has no Python counterpart. Python spawned an OS terminal emulator per
// device and let *that* interpret the byte stream; the rebuilt multiplexer is
// the emulator, so the interpretation has to live somewhere. This is the
// smallest thing that can honestly be called one: a character grid, a cursor,
// a scroll region, SGR passthrough and a bounded scrollback.
//
// # Deliberate limits
//
// The model renders what a device shell, `ip`, `vtysh`, `less` and `vim` emit.
// It is not a conformance-complete DEC terminal and does not pretend to be:
//
//   - No terminal *replies*. DSR (`ESC[6n`), DA (`ESC[c`) and the OSC colour
//     queries are consumed and dropped rather than answered, because a pane has
//     no input path back to the device that the user is not also typing on.
//     Programs that block waiting for a reply (very few; the ones that matter
//     time out) degrade rather than corrupt.
//   - Combining marks are dropped instead of merged into the preceding cell.
//   - Resizing does not reflow wrapped lines, which is also what tmux does.
//   - No character-set designation (`ESC(0` line drawing) — the sequence is
//     consumed so it cannot leak to the screen, but the glyphs stay ASCII.
//
// Each of those is a rendering fidelity limit inside a sanctioned rebuild, not
// a behaviour difference against Python: Python had no renderer of its own at
// all.
package term

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

// DefaultScrollback is the number of scrolled-off lines a pane retains.
//
// One window per scenario times a few hundred cells per line puts a 20-device
// scenario at a few megabytes, which is the right trade for the "scrollback"
// requirement of §3.3 item 1.
const DefaultScrollback = 2000

// cell is one character position: the rune it shows and an index into the
// screen's style table.
//
// The style is interned rather than stored as a string per cell because a
// coloured `vtysh` screen has a handful of distinct SGR states and 80×24 cells;
// storing the sequence per cell would allocate on every printable character.
type cell struct {
	r     rune
	style uint16
}

// blank is the erased cell: a space in the default style. Erase operations
// write it rather than a zero rune so that a rendered line never contains NULs.
var blank = cell{r: ' '}

// Screen is one pane's terminal emulation state.
//
// It is an io.Writer: the pane pump copies device output into it. It is NOT
// safe for concurrent use — the multiplexer feeds it only from bubbletea's
// update loop, which is single-threaded, and that is the whole synchronisation
// story (see mux.go).
type Screen struct {
	cols, rows int

	grid           [][]cell
	curX           int
	curY           int
	savedX, savedY int
	savedStyle     uint16

	style      uint16
	styles     []string
	styleIndex map[string]uint16

	// scrollTop/scrollBot are the DECSTBM region, 0-based and inclusive.
	scrollTop, scrollBot int

	// wrapNext is the "cursor is past the last column" deferred-wrap state DEC
	// terminals keep: printing at column cols-1 leaves the cursor there and
	// only wraps when the *next* printable arrives. Without it, a line exactly
	// as wide as the screen produces a spurious blank line.
	wrapNext bool
	autoWrap bool

	scrollback    [][]cell
	maxScrollback int

	// alt holds the primary screen while the alternate screen (`?1049h`) is
	// active. Scrollback does not accrue from the alternate screen, matching
	// xterm: `less` must not push the file into the user's history.
	alt       [][]cell
	altActive bool
	altCurX   int
	altCurY   int

	cursorHidden bool

	// title is the last OSC 0/2 window title the device set. The multiplexer
	// shows it beside the device name in the tab bar when a device sets one.
	title string

	pending []byte // partial UTF-8 carried across Write calls

	state  parseState
	params []byte
	inter  []byte
}

type parseState int

const (
	stGround parseState = iota
	stEsc
	stCSI
	stOSC
	stStr     // DCS/SOS/PM/APC: consumed until ST
	stCharset // ESC ( ) * + : one more byte
)

// NewScreen returns a screen of the given geometry retaining scrollback lines
// of history. Zero or negative dimensions are clamped to 1, and a zero
// scrollback to [DefaultScrollback]; a screen with no dimensions is not a
// useful failure mode, it is just a crash waiting for the first Write.
func NewScreen(cols, rows, scrollback int) *Screen {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if scrollback == 0 {
		scrollback = DefaultScrollback
	}
	if scrollback < 0 {
		scrollback = 0
	}
	s := &Screen{
		cols:          cols,
		rows:          rows,
		maxScrollback: scrollback,
		autoWrap:      true,
		styles:        []string{""},
		styleIndex:    map[string]uint16{"": 0},
	}
	s.grid = newGrid(cols, rows)
	s.scrollTop, s.scrollBot = 0, rows-1
	return s
}

func newGrid(cols, rows int) [][]cell {
	g := make([][]cell, rows)
	for y := range g {
		g[y] = newLine(cols)
	}
	return g
}

func newLine(cols int) []cell {
	l := make([]cell, cols)
	for x := range l {
		l[x] = blank
	}
	return l
}

// Size reports the screen geometry in cells.
func (s *Screen) Size() (cols, rows int) { return s.cols, s.rows }

// Title is the last window title the device set with OSC 0 or OSC 2, or "".
func (s *Screen) Title() string { return s.title }

// CursorVisible reports whether the device has hidden the cursor (`?25l`).
func (s *Screen) CursorVisible() bool { return !s.cursorHidden }

// Cursor is the cursor position in cells, 0-based, column first.
func (s *Screen) Cursor() (x, y int) { return s.curX, s.curY }

// Write feeds device output into the screen. It never fails and never reports
// a short write: a terminal that refused bytes would wedge the pump, and there
// is nothing a caller could do about a malformed escape sequence anyway.
func (s *Screen) Write(p []byte) (int, error) {
	data := p
	if len(s.pending) > 0 {
		data = append(s.pending, p...)
		s.pending = nil
	}

	i := 0
	for i < len(data) {
		b := data[i]
		if s.state == stGround && b >= 0x80 {
			if !utf8.FullRune(data[i:]) {
				// A multi-byte rune split across two reads. Keep the tail and
				// decode it when the rest arrives — decoding it now would emit
				// U+FFFD, which is precisely OQ-20's chunk-boundary bug
				// (SPIKES/windows-terminal.md §9 row 3) reintroduced one layer
				// up.
				s.pending = append(s.pending[:0], data[i:]...)
				return len(p), nil
			}
			r, size := utf8.DecodeRune(data[i:])
			s.print(r)
			i += size
			continue
		}
		s.feed(b)
		i++
	}
	return len(p), nil
}

// feed advances the parser by one byte. Escape sequences are ASCII by
// construction, so only the ground state needs rune decoding.
func (s *Screen) feed(b byte) {
	switch s.state {
	case stGround:
		s.ground(b)
	case stEsc:
		s.escape(b)
	case stCSI:
		switch {
		case b >= 0x30 && b <= 0x3f:
			s.params = append(s.params, b)
		case b >= 0x20 && b <= 0x2f:
			s.inter = append(s.inter, b)
		case b >= 0x40 && b <= 0x7e:
			s.csi(b)
			s.state = stGround
		case b == 0x1b:
			// An aborted sequence: restart.
			s.state = stEsc
			s.params, s.inter = s.params[:0], s.inter[:0]
		default:
			// C0 controls execute in the middle of a sequence, as on a real
			// terminal, and do not end it.
			s.control(b)
		}
	case stOSC, stStr:
		switch b {
		case 0x07: // BEL terminates an OSC string.
			s.finishString()
		case 0x1b:
			// Assume ESC \ (ST). The backslash is swallowed by stEsc below.
			s.finishString()
			s.state = stEsc
		default:
			if s.state == stOSC && len(s.params) < 1024 {
				s.params = append(s.params, b)
			}
		}
	case stCharset:
		s.state = stGround
	}
}

func (s *Screen) finishString() {
	if s.state == stOSC {
		s.osc(string(s.params))
	}
	s.params = s.params[:0]
	s.state = stGround
}

func (s *Screen) ground(b byte) {
	switch {
	case b == 0x1b:
		s.state = stEsc
		s.params, s.inter = s.params[:0], s.inter[:0]
	case b < 0x20 || b == 0x7f:
		s.control(b)
	default:
		s.print(rune(b))
	}
}

func (s *Screen) control(b byte) {
	switch b {
	case '\n', 0x0b, 0x0c: // LF, VT, FF all index
		s.index()
	case '\r':
		s.curX = 0
		s.wrapNext = false
	case '\b':
		if s.wrapNext {
			s.wrapNext = false
		} else if s.curX > 0 {
			s.curX--
		}
	case '\t':
		s.wrapNext = false
		next := (s.curX/8 + 1) * 8
		if next >= s.cols {
			next = s.cols - 1
		}
		s.curX = next
	case 0x07, 0x00, 0x7f:
		// BEL, NUL and DEL are not rendered. Python's Windows path wrote a
		// literal NUL per chunk (OQ-19); dropping it here is the same fix one
		// layer up.
	}
}

func (s *Screen) escape(b byte) {
	s.state = stGround
	switch b {
	case '[':
		s.state = stCSI
		s.params, s.inter = s.params[:0], s.inter[:0]
	case ']':
		s.state = stOSC
		s.params = s.params[:0]
	case 'P', 'X', '^', '_':
		s.state = stStr
		s.params = s.params[:0]
	case '(', ')', '*', '+':
		s.state = stCharset
	case '7':
		s.savedX, s.savedY, s.savedStyle = s.curX, s.curY, s.style
	case '8':
		s.curX, s.curY, s.style = s.savedX, s.savedY, s.savedStyle
		s.clampCursor()
	case 'D':
		s.index()
	case 'M':
		s.reverseIndex()
	case 'E':
		s.curX = 0
		s.index()
	case 'c':
		s.reset()
	case '\\':
		// String terminator with no string open.
	}
}

// print puts one rune at the cursor, honouring deferred wrap.
func (s *Screen) print(r rune) {
	w := runeWidth(r)
	if w == 0 {
		// Combining marks and other zero-width runes are dropped rather than
		// merged into the preceding cell (documented limit, file header).
		return
	}
	if s.wrapNext && s.autoWrap {
		s.curX = 0
		s.index()
		s.wrapNext = false
	}
	if s.curX >= s.cols {
		s.curX = s.cols - 1
	}
	line := s.grid[s.curY]
	line[s.curX] = cell{r: r, style: s.style}
	// A double-width rune claims the following cell; blanking it keeps the
	// rendered line the same display width as the grid.
	if w == 2 && s.curX+1 < s.cols {
		line[s.curX+1] = cell{r: 0, style: s.style}
	}
	s.curX += w
	if s.curX >= s.cols {
		s.curX = s.cols - 1
		s.wrapNext = true
	}
}

// index is LF: move down one line, scrolling the region when at its bottom.
func (s *Screen) index() {
	s.wrapNext = false
	if s.curY == s.scrollBot {
		s.scrollUp(1)
		return
	}
	if s.curY < s.rows-1 {
		s.curY++
	}
}

func (s *Screen) reverseIndex() {
	s.wrapNext = false
	if s.curY == s.scrollTop {
		s.scrollDown(1)
		return
	}
	if s.curY > 0 {
		s.curY--
	}
}

// scrollUp moves the scroll region up by n lines. Lines leaving the top of a
// full-height region on the primary screen enter the scrollback.
//
// "Full-height" is both margins, not just the top one: an application that
// pins a status line with `ESC[1;23r` is scrolling a window of its own screen,
// and xterm puts none of that in the user's history.
func (s *Screen) scrollUp(n int) {
	if n <= 0 {
		return
	}
	fullHeight := s.scrollTop == 0 && s.scrollBot == s.rows-1
	for i := 0; i < n; i++ {
		top := s.grid[s.scrollTop]
		if !s.altActive && fullHeight && s.maxScrollback > 0 {
			s.scrollback = append(s.scrollback, top)
			if len(s.scrollback) > s.maxScrollback {
				drop := len(s.scrollback) - s.maxScrollback
				s.scrollback = append(s.scrollback[:0:0], s.scrollback[drop:]...)
			}
			top = newLine(s.cols)
		} else {
			for x := range top {
				top[x] = blank
			}
		}
		copy(s.grid[s.scrollTop:s.scrollBot], s.grid[s.scrollTop+1:s.scrollBot+1])
		s.grid[s.scrollBot] = top
	}
}

func (s *Screen) scrollDown(n int) {
	for i := 0; i < n; i++ {
		bot := s.grid[s.scrollBot]
		for x := range bot {
			bot[x] = blank
		}
		copy(s.grid[s.scrollTop+1:s.scrollBot+1], s.grid[s.scrollTop:s.scrollBot])
		s.grid[s.scrollTop] = bot
	}
}

func (s *Screen) csi(final byte) {
	private := len(s.params) > 0 && (s.params[0] == '?' || s.params[0] == '<' ||
		s.params[0] == '=' || s.params[0] == '>')
	raw := s.params
	if private {
		raw = raw[1:]
	}
	ps := parseParams(raw)
	arg := func(i, def int) int {
		if i < len(ps) && ps[i] > 0 {
			return ps[i]
		}
		if i < len(ps) && ps[i] == 0 && def == 0 {
			return 0
		}
		return def
	}

	if private {
		switch final {
		case 'h':
			s.setPrivateModes(ps, true)
		case 'l':
			s.setPrivateModes(ps, false)
		}
		s.params, s.inter = s.params[:0], s.inter[:0]
		return
	}

	switch final {
	case 'A': // CUU
		s.curY = maxInt(s.scrollTopBound(), s.curY-arg(0, 1))
		s.wrapNext = false
	case 'B': // CUD
		s.curY = minInt(s.scrollBotBound(), s.curY+arg(0, 1))
		s.wrapNext = false
	case 'C': // CUF
		s.curX = minInt(s.cols-1, s.curX+arg(0, 1))
		s.wrapNext = false
	case 'D': // CUB
		s.curX = maxInt(0, s.curX-arg(0, 1))
		s.wrapNext = false
	case 'E': // CNL
		s.curX = 0
		s.curY = minInt(s.rows-1, s.curY+arg(0, 1))
	case 'F': // CPL
		s.curX = 0
		s.curY = maxInt(0, s.curY-arg(0, 1))
	case 'G', '`': // CHA / HPA
		s.curX = clamp(arg(0, 1)-1, 0, s.cols-1)
		s.wrapNext = false
	case 'd': // VPA
		s.curY = clamp(arg(0, 1)-1, 0, s.rows-1)
		s.wrapNext = false
	case 'H', 'f': // CUP / HVP
		s.curY = clamp(arg(0, 1)-1, 0, s.rows-1)
		s.curX = clamp(arg(1, 1)-1, 0, s.cols-1)
		s.wrapNext = false
	case 'J':
		s.eraseDisplay(arg(0, 0))
	case 'K':
		s.eraseLine(arg(0, 0))
	case 'L': // IL
		s.insertLines(arg(0, 1))
	case 'M': // DL
		s.deleteLines(arg(0, 1))
	case 'P': // DCH
		s.deleteChars(arg(0, 1))
	case '@': // ICH
		s.insertChars(arg(0, 1))
	case 'X': // ECH
		n := clamp(arg(0, 1), 0, s.cols-s.curX)
		line := s.grid[s.curY]
		for x := s.curX; x < s.curX+n; x++ {
			line[x] = cell{r: ' ', style: s.style}
		}
	case 'S': // SU
		s.scrollUp(arg(0, 1))
	case 'T': // SD
		s.scrollDown(arg(0, 1))
	case 'm':
		s.sgr(raw)
	case 'r': // DECSTBM
		top := clamp(arg(0, 1)-1, 0, s.rows-1)
		bot := clamp(arg(1, s.rows)-1, 0, s.rows-1)
		if top < bot {
			s.scrollTop, s.scrollBot = top, bot
			s.curX, s.curY = 0, top
		}
	case 's':
		s.savedX, s.savedY, s.savedStyle = s.curX, s.curY, s.style
	case 'u':
		s.curX, s.curY, s.style = s.savedX, s.savedY, s.savedStyle
		s.clampCursor()
	}
	s.params, s.inter = s.params[:0], s.inter[:0]
}

// scrollTopBound / scrollBotBound keep cursor motion inside the scroll region
// when the cursor is already in it, and inside the screen when it is not —
// the DEC rule CUU/CUD follow.
func (s *Screen) scrollTopBound() int {
	if s.curY >= s.scrollTop {
		return s.scrollTop
	}
	return 0
}

func (s *Screen) scrollBotBound() int {
	if s.curY <= s.scrollBot {
		return s.scrollBot
	}
	return s.rows - 1
}

func (s *Screen) setPrivateModes(ps []int, set bool) {
	for _, p := range ps {
		switch p {
		case 7:
			s.autoWrap = set
		case 25:
			s.cursorHidden = !set
		case 47, 1047, 1049:
			s.setAltScreen(set)
		}
	}
}

func (s *Screen) setAltScreen(on bool) {
	if on == s.altActive {
		return
	}
	if on {
		s.alt = s.grid
		s.altCurX, s.altCurY = s.curX, s.curY
		s.grid = newGrid(s.cols, s.rows)
		s.curX, s.curY = 0, 0
		s.altActive = true
		return
	}
	s.grid = s.alt
	s.alt = nil
	s.curX, s.curY = s.altCurX, s.altCurY
	s.altActive = false
	s.clampCursor()
}

func (s *Screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(0)
		for y := s.curY + 1; y < s.rows; y++ {
			s.clearRow(y)
		}
	case 1:
		s.eraseLine(1)
		for y := 0; y < s.curY; y++ {
			s.clearRow(y)
		}
	case 2, 3:
		for y := 0; y < s.rows; y++ {
			s.clearRow(y)
		}
	}
}

func (s *Screen) clearRow(y int) {
	line := s.grid[y]
	for x := range line {
		line[x] = cell{r: ' ', style: s.style}
	}
}

func (s *Screen) eraseLine(mode int) {
	line := s.grid[s.curY]
	switch mode {
	case 0:
		for x := s.curX; x < s.cols; x++ {
			line[x] = cell{r: ' ', style: s.style}
		}
	case 1:
		for x := 0; x <= s.curX && x < s.cols; x++ {
			line[x] = cell{r: ' ', style: s.style}
		}
	case 2:
		for x := range line {
			line[x] = cell{r: ' ', style: s.style}
		}
	}
}

func (s *Screen) insertLines(n int) {
	if s.curY < s.scrollTop || s.curY > s.scrollBot {
		return
	}
	n = clamp(n, 0, s.scrollBot-s.curY+1)
	for i := 0; i < n; i++ {
		line := s.grid[s.scrollBot]
		for x := range line {
			line[x] = blank
		}
		copy(s.grid[s.curY+1:s.scrollBot+1], s.grid[s.curY:s.scrollBot])
		s.grid[s.curY] = line
	}
}

func (s *Screen) deleteLines(n int) {
	if s.curY < s.scrollTop || s.curY > s.scrollBot {
		return
	}
	n = clamp(n, 0, s.scrollBot-s.curY+1)
	for i := 0; i < n; i++ {
		line := s.grid[s.curY]
		for x := range line {
			line[x] = blank
		}
		copy(s.grid[s.curY:s.scrollBot], s.grid[s.curY+1:s.scrollBot+1])
		s.grid[s.scrollBot] = line
	}
}

func (s *Screen) deleteChars(n int) {
	line := s.grid[s.curY]
	n = clamp(n, 0, s.cols-s.curX)
	copy(line[s.curX:], line[s.curX+n:])
	for x := s.cols - n; x < s.cols; x++ {
		line[x] = cell{r: ' ', style: s.style}
	}
}

func (s *Screen) insertChars(n int) {
	line := s.grid[s.curY]
	n = clamp(n, 0, s.cols-s.curX)
	copy(line[s.curX+n:], line[s.curX:])
	for x := s.curX; x < s.curX+n; x++ {
		line[x] = cell{r: ' ', style: s.style}
	}
}

// sgr interns the whole parameter string rather than tracking attributes
// individually. The pane's job is to hand the sequence back to the user's real
// terminal unchanged; decomposing and recomposing it would only add a place to
// lose a colour.
//
// The one parameter that must be understood is the reset (empty, "0" or "00"),
// because it has to clear accumulated state rather than append to it.
func (s *Screen) sgr(raw []byte) {
	spec := string(raw)
	if spec == "" || spec == "0" || spec == "00" {
		s.style = 0
		return
	}
	cur := s.styles[s.style]
	next := cur + "\x1b[" + spec + "m"
	// A reset in the middle of a compound sequence ("0;1;31") makes everything
	// before it irrelevant.
	if idx := lastResetIndex(spec); idx >= 0 {
		next = "\x1b[" + spec[idx:] + "m"
	}
	s.style = s.internStyle(next)
}

// lastResetIndex returns the offset in a semicolon-separated SGR parameter
// string of the last "0" parameter, or -1. "38;5;0" must not match on its
// colour index, so the scan is per parameter.
func lastResetIndex(spec string) int {
	idx := -1
	start := 0
	for i := 0; i <= len(spec); i++ {
		if i == len(spec) || spec[i] == ';' {
			p := spec[start:i]
			if p == "" || p == "0" || p == "00" {
				idx = start
			}
			start = i + 1
		}
	}
	return idx
}

func (s *Screen) internStyle(seq string) uint16 {
	if i, ok := s.styleIndex[seq]; ok {
		return i
	}
	// The table is capped: a program emitting unbounded distinct SGR states
	// (a 24-bit-colour animation) must not grow it without limit. Past the cap
	// the sequence is still rendered, it just is not interned, so the cap costs
	// fidelity of *future* distinct styles rather than correctness of this one.
	if len(s.styles) >= int(^uint16(0)) {
		return 0
	}
	s.styles = append(s.styles, seq)
	i := uint16(len(s.styles) - 1)
	s.styleIndex[seq] = i
	return i
}

func (s *Screen) osc(body string) {
	// OSC 0 (icon+title) and OSC 2 (title) are the only ones acted on; the
	// rest — including OSC 52 clipboard writes from inside a device — are
	// dropped, because a pane must not be able to drive the host clipboard.
	num, rest, ok := strings.Cut(body, ";")
	if !ok {
		return
	}
	if num == "0" || num == "2" {
		s.title = sanitizeTitle(rest)
	}
}

// sanitizeTitle strips control characters from a device-supplied title so it
// cannot forge tab-bar structure or emit escape sequences of its own when the
// multiplexer renders it.
func sanitizeTitle(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() > 128 {
			break
		}
	}
	return b.String()
}

func (s *Screen) reset() {
	s.grid = newGrid(s.cols, s.rows)
	s.curX, s.curY = 0, 0
	s.style = 0
	s.autoWrap = true
	s.cursorHidden = false
	s.scrollTop, s.scrollBot = 0, s.rows-1
	s.altActive = false
	s.alt = nil
}

func (s *Screen) clampCursor() {
	s.curX = clamp(s.curX, 0, s.cols-1)
	s.curY = clamp(s.curY, 0, s.rows-1)
}

// Resize changes the geometry.
//
// Lines are not reflowed — a wrapped line stays wrapped where it was, which is
// tmux's behaviour too. Rows removed from the top of a shrinking *primary*
// screen go to the scrollback rather than being discarded, so shrinking a
// window never loses output — including when the resize arrives while `less`
// or `vim` has the alternate screen up and the primary is only the saved copy
// underneath it. Rows the alternate screen loses are gone, which is what every
// terminal does with them: the application redraws.
func (s *Screen) Resize(cols, rows int) {
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	if cols == s.cols && rows == s.rows {
		return
	}

	// Each grid is resized around *its own* cursor, which decides how much
	// comes off the top rather than the bottom. `s.alt` holds the saved
	// primary screen while the alternate one is active, and its cursor is
	// altCurX/altCurY, not the alternate screen's.
	if s.altActive {
		s.grid, s.curY = s.resizeGrid(s.grid, cols, rows, s.curY, false)
		s.alt, s.altCurY = s.resizeGrid(s.alt, cols, rows, s.altCurY, true)
		s.altCurX = clamp(s.altCurX, 0, cols-1)
		s.altCurY = clamp(s.altCurY, 0, rows-1)
	} else {
		s.grid, s.curY = s.resizeGrid(s.grid, cols, rows, s.curY, true)
	}
	// Scrollback keeps its own width so history is not truncated by a
	// temporary narrow window; rendering pads or cuts to the current width.
	s.cols, s.rows = cols, rows
	s.scrollTop, s.scrollBot = 0, rows-1
	s.clampCursor()
	s.wrapNext = false
}

// resizeGrid resizes one grid around curY and returns it with the cursor row
// the caller must now record for that grid. spill says whether rows taken off
// the top are history (the primary screen) or discarded (the alternate one).
func (s *Screen) resizeGrid(g [][]cell, cols, rows, curY int, spill bool) ([][]cell, int) {
	for y := range g {
		g[y] = resizeLine(g[y], cols)
	}
	switch {
	case rows > len(g):
		for i := len(g); i < rows; i++ {
			g = append(g, newLine(cols))
		}
	case rows < len(g):
		needed := len(g) - rows
		// Take rows off the bottom first — they are the ones a shell has not
		// written to — and only cut into the top when that would put the
		// cursor off the screen. Lines that do come off the top are kept, in
		// the scrollback.
		drop := clamp(curY-rows+1, 0, needed)
		if spill && s.maxScrollback > 0 {
			for i := 0; i < drop; i++ {
				s.scrollback = append(s.scrollback, g[i])
			}
			if len(s.scrollback) > s.maxScrollback {
				cut := len(s.scrollback) - s.maxScrollback
				s.scrollback = append(s.scrollback[:0:0], s.scrollback[cut:]...)
			}
		}
		g = g[drop:]
		if len(g) > rows {
			g = g[:rows]
		}
		curY -= drop
		for len(g) < rows {
			g = append(g, newLine(cols))
		}
	}
	return g, curY
}

func resizeLine(l []cell, cols int) []cell {
	switch {
	case cols == len(l):
		return l
	case cols < len(l):
		return l[:cols]
	default:
		out := make([]cell, cols)
		copy(out, l)
		for x := len(l); x < cols; x++ {
			out[x] = blank
		}
		return out
	}
}

// TotalLines is the number of addressable lines, scrollback first then the
// visible screen. It is the coordinate space [Screen.RenderLines] and
// [Screen.PlainLines] index into.
func (s *Screen) TotalLines() int { return len(s.scrollback) + s.rows }

// ScrollbackLen is the number of retained history lines.
func (s *Screen) ScrollbackLen() int { return len(s.scrollback) }

func (s *Screen) lineAt(i int) []cell {
	if i < len(s.scrollback) {
		return s.scrollback[i]
	}
	return s.grid[i-len(s.scrollback)]
}

// RenderLines returns lines [from, to) rendered with their SGR sequences,
// ready to be placed in a bubbletea view. Each line ends with a reset so a
// device's colour cannot leak into the multiplexer's own chrome.
func (s *Screen) RenderLines(from, to int) []string {
	return s.RenderView(from, to, false)
}

// RenderView is [Screen.RenderLines] with the text cursor drawn as a
// reverse-video cell when showCursor is set and the device has not hidden it.
//
// bubbletea hides the real terminal cursor for the whole program, so a pane
// that did not draw its own would leave the user typing blind — the single
// most noticeable way a multiplexer can feel broken.
func (s *Screen) RenderView(from, to int, showCursor bool) []string {
	from = clamp(from, 0, s.TotalLines())
	to = clamp(to, from, s.TotalLines())
	cursorLine := -1
	if showCursor && !s.cursorHidden {
		cursorLine = len(s.scrollback) + s.curY
	}
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		curX := -1
		if i == cursorLine {
			curX = s.curX
		}
		out = append(out, renderLine(s.lineAt(i), s.styles, s.cols, curX))
	}
	return out
}

func renderLine(line []cell, styles []string, width int, cursorX int) string {
	// Trailing blanks in the default style are dropped: emitting 80 spaces per
	// line would make every frame the full width even when nothing is there,
	// and bubbletea pads for us.
	end := len(line)
	for end > 0 && line[end-1].r == ' ' && line[end-1].style == 0 {
		end--
	}
	if cursorX >= 0 && cursorX < len(line) && cursorX+1 > end {
		end = cursorX + 1
	}
	if end == 0 {
		return ""
	}
	var b strings.Builder
	cur := uint16(0)
	for i := 0; i < end && i < width; i++ {
		c := line[i]
		if c.r == 0 {
			// The second half of a double-width rune: already emitted.
			continue
		}
		if i == cursorX {
			b.WriteString("\x1b[0m\x1b[7m")
			if c.r == ' ' || c.r == 0 {
				b.WriteRune(' ')
			} else {
				b.WriteRune(c.r)
			}
			b.WriteString("\x1b[0m")
			cur = 0
			continue
		}
		if c.style != cur {
			b.WriteString("\x1b[0m")
			if int(c.style) < len(styles) {
				b.WriteString(styles[c.style])
			}
			cur = c.style
		}
		b.WriteRune(c.r)
	}
	if cur != 0 {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// PlainLines returns lines [from, to) as text with no escape sequences and no
// trailing blanks. It is what copy yields and what the tests assert on.
func (s *Screen) PlainLines(from, to int) []string {
	from = clamp(from, 0, s.TotalLines())
	to = clamp(to, from, s.TotalLines())
	out := make([]string, 0, to-from)
	for i := from; i < to; i++ {
		out = append(out, plainLine(s.lineAt(i)))
	}
	return out
}

func plainLine(line []cell) string {
	end := len(line)
	for end > 0 && line[end-1].r == ' ' {
		end--
	}
	var b strings.Builder
	for i := 0; i < end; i++ {
		if line[i].r == 0 {
			continue
		}
		b.WriteRune(line[i].r)
	}
	return b.String()
}

// Text renders lines [from, to) as one newline-joined plain-text block.
func (s *Screen) Text(from, to int) string {
	return strings.Join(s.PlainLines(from, to), "\n")
}

// String is the visible screen as plain text, for tests and diagnostics.
func (s *Screen) String() string {
	base := len(s.scrollback)
	return s.Text(base, base+s.rows)
}

func parseParams(raw []byte) []int {
	if len(raw) == 0 {
		return nil
	}
	fields := strings.Split(string(raw), ";")
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		// Sub-parameters ("38:5:1") are not interpreted; the leading number is
		// all any handled sequence needs.
		if i := strings.IndexByte(f, ':'); i >= 0 {
			f = f[:i]
		}
		n, err := strconv.Atoi(f)
		if err != nil {
			n = 0
		}
		out = append(out, n)
	}
	return out
}

// runeWidth is the display width of one rune in cells.
//
// ASCII is answered without a call; everything else goes through lipgloss,
// which is already a `term` dependency (PACKAGE_GRAPH §5) and carries the
// east-asian-width and combining-mark tables. Adding `mattn/go-runewidth` as a
// direct dependency for the same answer would widen the pinned set.
func runeWidth(r rune) int {
	if r < 0x80 {
		if r < 0x20 || r == 0x7f {
			return 0
		}
		return 1
	}
	return lipgloss.Width(string(r))
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
