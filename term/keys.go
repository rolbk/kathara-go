// This file turns bubbletea's parsed key events back into the bytes a device
// shell expects.

package term

import (
	tea "github.com/charmbracelet/bubbletea"
)

// specialKeys is the escape sequence for every non-textual key bubbletea can
// report. Keys absent from the table produce no bytes at all rather than a
// guess — sending the wrong sequence to a router's CLI is worse than sending
// nothing.
var specialKeys = map[tea.KeyType]string{
	tea.KeyUp:    "\x1b[A",
	tea.KeyDown:  "\x1b[B",
	tea.KeyRight: "\x1b[C",
	tea.KeyLeft:  "\x1b[D",

	tea.KeyShiftTab: "\x1b[Z",
	tea.KeyHome:     "\x1b[H",
	tea.KeyEnd:      "\x1b[F",
	tea.KeyPgUp:     "\x1b[5~",
	tea.KeyPgDown:   "\x1b[6~",
	tea.KeyDelete:   "\x1b[3~",
	tea.KeyInsert:   "\x1b[2~",

	tea.KeyCtrlUp:     "\x1b[1;5A",
	tea.KeyCtrlDown:   "\x1b[1;5B",
	tea.KeyCtrlRight:  "\x1b[1;5C",
	tea.KeyCtrlLeft:   "\x1b[1;5D",
	tea.KeyCtrlHome:   "\x1b[1;5H",
	tea.KeyCtrlEnd:    "\x1b[1;5F",
	tea.KeyCtrlPgUp:   "\x1b[5;5~",
	tea.KeyCtrlPgDown: "\x1b[6;5~",

	tea.KeyShiftUp:    "\x1b[1;2A",
	tea.KeyShiftDown:  "\x1b[1;2B",
	tea.KeyShiftRight: "\x1b[1;2C",
	tea.KeyShiftLeft:  "\x1b[1;2D",
	tea.KeyShiftHome:  "\x1b[1;2H",
	tea.KeyShiftEnd:   "\x1b[1;2F",

	tea.KeyCtrlShiftUp:    "\x1b[1;6A",
	tea.KeyCtrlShiftDown:  "\x1b[1;6B",
	tea.KeyCtrlShiftRight: "\x1b[1;6C",
	tea.KeyCtrlShiftLeft:  "\x1b[1;6D",
	tea.KeyCtrlShiftHome:  "\x1b[1;6H",
	tea.KeyCtrlShiftEnd:   "\x1b[1;6F",

	// F1–F4 are SS3, F5 upwards are CSI ~ with xterm's numbering (there is no
	// 16 and no 22, which is why the list is spelled out rather than computed).
	tea.KeyF1:  "\x1bOP",
	tea.KeyF2:  "\x1bOQ",
	tea.KeyF3:  "\x1bOR",
	tea.KeyF4:  "\x1bOS",
	tea.KeyF5:  "\x1b[15~",
	tea.KeyF6:  "\x1b[17~",
	tea.KeyF7:  "\x1b[18~",
	tea.KeyF8:  "\x1b[19~",
	tea.KeyF9:  "\x1b[20~",
	tea.KeyF10: "\x1b[21~",
	tea.KeyF11: "\x1b[23~",
	tea.KeyF12: "\x1b[24~",
	tea.KeyF13: "\x1b[25~",
	tea.KeyF14: "\x1b[26~",
	tea.KeyF15: "\x1b[28~",
	tea.KeyF16: "\x1b[29~",
	tea.KeyF17: "\x1b[31~",
	tea.KeyF18: "\x1b[32~",
	tea.KeyF19: "\x1b[33~",
	tea.KeyF20: "\x1b[34~",
}

// encodeKey renders a key event as the bytes a terminal would have sent.
func encodeKey(k tea.KeyMsg) []byte {
	var body []byte

	switch {
	case k.Type == tea.KeyRunes:
		body = []byte(string(k.Runes))
	case k.Type == tea.KeySpace:
		body = []byte(" ")
	case k.Type >= 0 && k.Type <= 127:
		body = []byte{byte(k.Type)}
	default:
		seq, ok := specialKeys[k.Type]
		if !ok {
			return nil
		}
		body = []byte(seq)
	}

	if k.Alt {
		return append([]byte{0x1b}, body...)
	}
	return body
}
