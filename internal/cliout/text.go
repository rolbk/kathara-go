// This file is `rich/_wrap.py` plus the three `rich/text.py` methods the CLI's
// renderables reach through it — `Text.wrap`, `Text.rstrip_end` and
// `Lines.justify` (`rich/containers.py:111`). Every branch below has a line
// citation into the installed 3.8.3 oracle's rich, because the fold points are
// a Layer A assertion: the lab metadata panel of a real scenario folds
// mid-author-list and the golden records the exact column.

package cliout

import (
	"strings"
	"unicode"

	"golang.org/x/text/width"
)

// Justify is rich's `JustifyMethod`, restricted to the three values the CLI
// ever passes: the `justify=` keyword of `cli/ui/utils.create_panel` is either
// absent or "center", and rich's own default for a `Text` is "default".
type Justify int

const (
	// JustifyDefault is rich's "default": `Lines.justify` matches none of its
	// four arms and does nothing, so the line keeps its own length and is
	// padded to the render width later by `Segment.set_shape`
	// (`rich/console.py`, `render_lines(pad=True)`). Observably that is the
	// same as [JustifyLeft]; the two are kept apart because the Python call
	// sites are.
	JustifyDefault Justify = iota

	// JustifyLeft is rich's "left": `truncate(width, pad=True)`.
	JustifyLeft

	// JustifyCenter is rich's "center": strip the trailing whitespace, then
	// pad both sides, left first with the floor of the halved slack.
	JustifyCenter
)

// CellLen is `rich.cells.cell_len`: the number of terminal columns a string
// occupies.
func CellLen(s string) int {
	n := 0
	for _, r := range s {
		n += CellWidth(r)
	}
	return n
}

// CellWidth is `rich.cells.get_character_cell_size` for one rune: 0 for a
// combining mark, 2 for an East Asian Wide or Fullwidth character, 1 otherwise.
func CellWidth(r rune) int {
	if r < 0x300 {
		// rich's fast path: everything below U+0300 is one cell.
		return 1
	}
	if unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf) {
		return 0
	}
	switch width.LookupRune(r).Kind() {
	case width.EastAsianWide, width.EastAsianFullwidth:
		return 2
	}
	return 1
}

// chopCells is `rich.cells.chop_cells`: split text into chunks each of at most
// width cells, breaking wherever the next character would overflow.
func chopCells(text []rune, w int) [][]rune {
	var lines [][]rune
	var line []rune
	total := 0
	for _, r := range text {
		cw := CellWidth(r)
		if total+cw > w {
			lines = append(lines, line)
			line = nil
			total = 0
		}
		line = append(line, r)
		total += cw
	}
	if len(line) > 0 {
		lines = append(lines, line)
	}
	return lines
}

// wordSpan is one match of `rich._wrap.re_word` (`\s*\S+\s*`): a run of
// non-space characters together with the whitespace on either side of it.
type wordSpan struct {
	start int
	word  []rune
}

// splitWords is `rich._wrap.words`. It walks the runes rather than running a
// regexp because the offsets it yields are rune offsets into the line, which is
// what `Text.divide` slices at.
func splitWords(text []rune) []wordSpan {
	var out []wordSpan
	i := 0
	for i < len(text) {
		start := i
		for i < len(text) && unicode.IsSpace(text[i]) {
			i++
		}
		if i >= len(text) {
			// Trailing whitespace with no word after it does not match
			// `\s*\S+\s*`, so rich's iterator stops. So does this.
			break
		}
		for i < len(text) && !unicode.IsSpace(text[i]) {
			i++
		}
		for i < len(text) && unicode.IsSpace(text[i]) {
			i++
		}
		out = append(out, wordSpan{start: start, word: text[start:i]})
	}
	return out
}

// rstripRunes drops the trailing whitespace of a rune slice, which is
// `str.rstrip()` on the `word` of `divide_line`.
func rstripRunes(r []rune) []rune {
	end := len(r)
	for end > 0 && unicode.IsSpace(r[end-1]) {
		end--
	}
	return r[:end]
}

// divideLine is `rich._wrap.divide_line` with `fold=True`, which is the only
// value the CLI reaches (`overflow` is never set on a panel's Text, so it is
// rich's DEFAULT_OVERFLOW, "fold").
func divideLine(text []rune, w int) []int {
	var breaks []int
	cellOffset := 0

	for _, ws := range splitWords(text) {
		start := ws.start
		word := ws.word
		wordLength := CellLen(string(rstripRunes(word)))
		if w-cellOffset >= wordLength {
			cellOffset += CellLen(string(word))
			continue
		}
		if wordLength > w {
			folded := chopCells(word, w)
			for i, line := range folded {
				if start != 0 {
					breaks = append(breaks, start)
				}
				if i == len(folded)-1 {
					cellOffset = CellLen(string(line))
				} else {
					start += len(line)
				}
			}
			continue
		}
		if cellOffset != 0 && start != 0 {
			breaks = append(breaks, start)
			cellOffset = CellLen(string(word))
		}
	}

	return breaks
}

// rstripEnd is `Text.rstrip_end(size)`: remove trailing whitespace, but only as
// much of it as overflows size. A folded line that ends exactly at the fold
// width keeps its trailing space, which is why the metadata panel's
// `… F. Ricci,` row has none and its predecessor's would.
func rstripEnd(r []rune, size int) []rune {
	if len(r) <= size {
		return r
	}
	excess := len(r) - size
	ws := 0
	for ws < len(r) && unicode.IsSpace(r[len(r)-1-ws]) {
		ws++
	}
	if ws == 0 {
		return r
	}
	crop := min(ws, excess)
	return r[:len(r)-crop]
}

// padTo appends spaces until the string measures w cells. It is
// `Text.truncate(w, pad=True)`'s padding half; the cropping half cannot fire
// because [Wrap] never produces a line longer than w.
func padTo(s string, w int) string {
	if n := CellLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// Wrap is `Text.wrap(console, width, justify=…)` followed by the
// `Segment.set_shape` padding that `Console.render_lines` applies: the result
// is the block of lines a renderable contributes, each measuring exactly width
// cells.
func Wrap(text string, w int, justify Justify) []string {
	if w <= 0 {
		return nil
	}

	var out []string
	for _, logical := range strings.Split(text, "\n") {
		runes := []rune(logical)
		offsets := divideLine(runes, w)

		start := 0
		pieces := make([][]rune, 0, len(offsets)+1)
		for _, off := range offsets {
			if off > len(runes) {
				off = len(runes)
			}
			if off < start {
				off = start
			}
			pieces = append(pieces, runes[start:off])
			start = off
		}
		pieces = append(pieces, runes[start:])

		for _, piece := range pieces {
			line := string(rstripEnd(piece, w))
			switch justify {
			case JustifyCenter:
				line = strings.TrimRightFunc(line, unicode.IsSpace)
				slack := w - CellLen(line)
				if slack > 0 {
					line = strings.Repeat(" ", slack/2) + line
				}
				line = padTo(line, w)
			default:
				line = padTo(line, w)
			}
			out = append(out, line)
		}
	}
	return out
}
