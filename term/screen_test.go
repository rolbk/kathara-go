package term

import (
	"strings"
	"testing"
)

// write feeds a screen and returns it, so a table row can be one expression.
func write(s *Screen, chunks ...string) *Screen {
	for _, c := range chunks {
		if _, err := s.Write([]byte(c)); err != nil {
			panic(err)
		}
	}
	return s
}

// visible is the screen's rows as plain text, trailing blank rows trimmed.
func visible(s *Screen) []string {
	base := s.ScrollbackLen()
	_, rows := s.Size()
	lines := s.PlainLines(base, base+rows)
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func TestScreenPrintingAndControls(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cols  int
		rows  int
		input []string
		want  []string
	}{
		{
			name:  "plain text and CRLF",
			cols:  20,
			rows:  4,
			input: []string{"pc1:~# ip addr\r\nlo\r\neth0"},
			want:  []string{"pc1:~# ip addr", "lo", "eth0"},
		},
		{
			name: "a bare LF indexes without returning, as on a real terminal",
			cols: 20,
			rows: 3,
			// This is why the CLI's startup log has to be CRLF-converted
			// before it reaches a pane (see cmd/kathara's crlf helper).
			input: []string{"one\ntwo"},
			want:  []string{"one", "   two"},
		},
		{
			name:  "carriage return overwrites in place",
			cols:  20,
			rows:  2,
			input: []string{"progress 10%\rprogress 99%"},
			want:  []string{"progress 99%"},
		},
		{
			name:  "backspace and overwrite",
			cols:  20,
			rows:  2,
			input: []string{"abcX\b\bYZ"},
			want:  []string{"abYZ"},
		},
		{
			name:  "tab stops every eight columns",
			cols:  20,
			rows:  2,
			input: []string{"a\tb\tc"},
			want:  []string{"a       b       c"},
		},
		{
			name: "deferred wrap: a full-width line does not eat the next row",
			cols: 4,
			rows: 3,
			// "abcd" exactly fills the row; the wrap happens only when "e"
			// arrives, so there is no blank line between them.
			input: []string{"abcde"},
			want:  []string{"abcd", "e"},
		},
		{
			name:  "cursor positioning",
			cols:  10,
			rows:  3,
			input: []string{"\x1b[2;3Hxy"},
			want:  []string{"", "  xy"},
		},
		{
			name:  "erase to end of line",
			cols:  10,
			rows:  2,
			input: []string{"abcdefgh\r\x1b[3C\x1b[K"},
			want:  []string{"abc"},
		},
		{
			name:  "erase display clears everything",
			cols:  10,
			rows:  3,
			input: []string{"one\r\ntwo\x1b[2J\x1b[H", "done"},
			want:  []string{"done"},
		},
		{
			name:  "UTF-8 split across two writes is not mangled",
			cols:  10,
			rows:  2,
			input: []string{"caf\xc3", "\xa9"},
			want:  []string{"café"},
		},
		{
			name:  "OSC and DCS strings never reach the screen",
			cols:  20,
			rows:  2,
			input: []string{"\x1b]0;a title\x07\x1bPsomething\x1b\\visible"},
			want:  []string{"visible"},
		},
		{
			name:  "an unknown CSI is consumed, not printed",
			cols:  20,
			rows:  2,
			input: []string{"a\x1b[>4;2mb"},
			want:  []string{"ab"},
		},
		{
			name:  "insert and delete characters",
			cols:  10,
			rows:  2,
			input: []string{"abcdef\r\x1b[2P"},
			want:  []string{"cdef"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := write(NewScreen(tc.cols, tc.rows, 100), tc.input...)
			got := visible(s)
			if len(got) != len(tc.want) {
				t.Fatalf("screen =\n%q\nwant\n%q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("line %d = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestScreenTitle(t *testing.T) {
	s := write(NewScreen(20, 3, 10), "\x1b]0;pc1 — vtysh\x07")
	if got := s.Title(); got != "pc1 — vtysh" {
		t.Errorf("Title = %q", got)
	}
	// A control character in a device-supplied title must not survive: the tab
	// bar renders it.
	s = write(NewScreen(20, 3, 10), "\x1b]2;evil\x1b[31mtitle\x07")
	if strings.ContainsRune(s.Title(), 0x1b) {
		t.Errorf("Title kept an escape: %q", s.Title())
	}
}

func TestScreenScrollbackAccrues(t *testing.T) {
	s := NewScreen(10, 3, 100)
	for i := 0; i < 10; i++ {
		write(s, string(rune('0'+i))+"\r\n")
	}
	// Ten lines each followed by CRLF into three rows: the first two fill the
	// screen, the remaining eight each scroll one line off the top.
	if got := s.ScrollbackLen(); got != 8 {
		t.Fatalf("ScrollbackLen = %d, want 8", got)
	}
	if got := s.TotalLines(); got != 11 {
		t.Fatalf("TotalLines = %d, want 11 (8 scrolled + 3 visible)", got)
	}
	if got := s.Text(0, 3); got != "0\n1\n2" {
		t.Errorf("oldest scrollback = %q, want %q", got, "0\n1\n2")
	}
	if got := visible(s); len(got) != 2 || got[0] != "8" || got[1] != "9" {
		t.Errorf("visible = %q, want the tail [8 9]", got)
	}
}

func TestScreenScrollbackIsBounded(t *testing.T) {
	s := NewScreen(10, 2, 4)
	for i := 0; i < 50; i++ {
		write(s, "line\r\n")
	}
	if got := s.ScrollbackLen(); got != 4 {
		t.Errorf("ScrollbackLen = %d, want the cap 4", got)
	}
}

func TestScreenAlternateScreenDoesNotPolluteScrollback(t *testing.T) {
	s := NewScreen(10, 3, 100)
	write(s, "history\r\n")
	before := s.ScrollbackLen()

	// `less` and `vim` switch to the alternate screen; nothing they draw
	// belongs in the user's history.
	write(s, "\x1b[?1049h")
	for i := 0; i < 20; i++ {
		write(s, "pager\r\n")
	}
	if got := s.ScrollbackLen(); got != before {
		t.Errorf("alternate screen added %d scrollback lines", got-before)
	}

	write(s, "\x1b[?1049l")
	if got := visible(s); len(got) == 0 || got[0] != "history" {
		t.Errorf("primary screen not restored: %q", got)
	}
}

func TestScreenResizeSpillsIntoScrollback(t *testing.T) {
	s := NewScreen(10, 4, 100)
	write(s, "a\r\nb\r\nc\r\nd")
	before := s.ScrollbackLen()

	s.Resize(10, 2)
	if cols, rows := s.Size(); cols != 10 || rows != 2 {
		t.Fatalf("Size = %d×%d, want 10×2", cols, rows)
	}
	if s.ScrollbackLen() <= before {
		t.Error("shrinking the window discarded lines instead of keeping them in scrollback")
	}
	// Nothing is lost: every original line is still addressable.
	all := s.Text(0, s.TotalLines())
	for _, want := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(all, want) {
			t.Errorf("line %q lost across resize; have %q", want, all)
		}
	}
}

func TestScreenSGRIsPassedThroughAndReset(t *testing.T) {
	s := write(NewScreen(20, 2, 10), "\x1b[1;31mRED\x1b[0m plain")

	// The rendered line carries the device's own sequence and ends reset, so a
	// colour cannot leak into the multiplexer's chrome.
	line := s.RenderLines(0, 1)[0]
	if !strings.Contains(line, "\x1b[1;31m") {
		t.Errorf("rendered line lost the SGR: %q", line)
	}
	// A line that ends *inside* a style closes it, so the attribute cannot
	// bleed into the status bar below it.
	open := write(NewScreen(20, 2, 10), "\x1b[31mRED")
	if got := open.RenderLines(0, 1)[0]; !strings.HasSuffix(got, "\x1b[0m") {
		t.Errorf("unterminated style not closed: %q", got)
	}
	// Copy yields text, never escapes.
	if got := s.Text(0, 1); got != "RED plain" {
		t.Errorf("PlainLines = %q, want %q", got, "RED plain")
	}
}

func TestScreenSGRResetInsideACompoundSequence(t *testing.T) {
	// "0;1" must not accumulate on top of the previous state.
	s := write(NewScreen(20, 2, 10), "\x1b[31mA\x1b[0;1mB")
	line := s.RenderLines(0, 1)[0]
	if strings.Contains(line[strings.Index(line, "B"):], "31") {
		t.Errorf("the reset did not drop the earlier colour: %q", line)
	}
}

func TestScreenCursorRendering(t *testing.T) {
	s := write(NewScreen(10, 2, 10), "ab")
	withCursor := s.RenderView(0, 1, true)[0]
	if !strings.Contains(withCursor, "\x1b[7m") {
		t.Errorf("cursor not drawn: %q", withCursor)
	}
	without := s.RenderView(0, 1, false)[0]
	if strings.Contains(without, "\x1b[7m") {
		t.Errorf("cursor drawn when it was not asked for: %q", without)
	}

	// A device that hides the cursor is obeyed.
	write(s, "\x1b[?25l")
	if s.CursorVisible() {
		t.Error("CursorVisible after ?25l")
	}
	if strings.Contains(s.RenderView(0, 1, true)[0], "\x1b[7m") {
		t.Error("hidden cursor still drawn")
	}
}

func TestScreenScrollRegion(t *testing.T) {
	s := NewScreen(10, 5, 100)
	write(s, "\x1b[2;4r")           // scroll region = rows 2..4
	write(s, "\x1b[1;1Htop")        // outside the region
	write(s, "\x1b[5;1Hbottom")     // outside the region
	write(s, "\x1b[2;1Ha\r\nb\r\n") // fill the region
	write(s, "c\r\nd")              // forces the region to scroll

	lines := visible(s)
	if len(lines) < 5 {
		t.Fatalf("screen too short: %q", lines)
	}
	if lines[0] != "top" || lines[4] != "bottom" {
		t.Errorf("scrolling escaped the region: %q", lines)
	}
	if lines[3] != "d" {
		t.Errorf("region bottom = %q, want %q", lines[3], "d")
	}
}

// TestScreenPartialScrollRegionKeepsOutOfTheScrollback: an application that
// pins a status line — `ESC[1;Nr` with the bottom margin short of the screen —
// is scrolling a window of its own screen. xterm puts none of that in the
// user's history, and neither does this, even though the region starts at the
// top row.
func TestScreenPartialScrollRegionKeepsOutOfTheScrollback(t *testing.T) {
	s := NewScreen(10, 5, 100)
	write(s, "\x1b[1;3r") // region = rows 1..3, a status area below it
	write(s, "\x1b[5;1Hstatus")
	write(s, "\x1b[1;1H")
	for i := 0; i < 20; i++ {
		write(s, "row\r\n")
	}
	if got := s.ScrollbackLen(); got != 0 {
		t.Errorf("a partial scroll region added %d lines to the scrollback", got)
	}
	if lines := visible(s); lines[4] != "status" {
		t.Errorf("the status line was scrolled: %q", lines)
	}

	// The full-height region still accrues history, which is the common case.
	write(s, "\x1b[r") // reset to the whole screen
	for i := 0; i < 20; i++ {
		write(s, "row\r\n")
	}
	if s.ScrollbackLen() == 0 {
		t.Error("a full-height region stopped feeding the scrollback")
	}
}

// TestScreenResizeUnderTheAlternateScreen: a window shrunk while `less` has the
// alternate screen up must trim the *saved primary* screen around the primary
// screen's own cursor, and the rows it takes off the top are history like any
// other. Resizing it around the alternate screen's cursor instead cuts the
// wrong end — the shell's last output — and loses it outright.
func TestScreenResizeUnderTheAlternateScreen(t *testing.T) {
	s := NewScreen(10, 4, 100)
	write(s, "p1\r\np2\r\np3\r\np4") // the primary screen, cursor on the last row

	write(s, "\x1b[?1049h") // less starts
	write(s, "a1\r\na2")    // its cursor is near the top
	s.Resize(10, 2)         // the user shrinks the window
	write(s, "\x1b[?1049l") // less exits

	lines := visible(s)
	if len(lines) != 2 || lines[0] != "p3" || lines[1] != "p4" {
		t.Errorf("restored primary screen = %q, want the last two lines [p3 p4]", lines)
	}
	// Nothing was discarded, and nothing the pager drew became history.
	if got := s.ScrollbackLen(); got != 2 {
		t.Fatalf("ScrollbackLen = %d, want exactly the two primary lines", got)
	}
	if got := s.Text(0, s.TotalLines()); !strings.Contains(got, "p1") || !strings.Contains(got, "p2") {
		t.Errorf("the trimmed primary rows were lost: %q", got)
	} else if strings.Contains(got, "a1") || strings.Contains(got, "a2") {
		t.Errorf("the alternate screen leaked into the history: %q", got)
	}
}
