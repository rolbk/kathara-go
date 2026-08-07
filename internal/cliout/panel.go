// This file is `rich/panel.py` and the two `rich/box.py` boxes the CLI names,
// reduced to the one configuration `cli/ui/utils.create_panel` ever builds:
// `expand=True`, `padding=(0, 1)`, no subtitle, and a title that only the
// deferred `linfo` command passes.

package cliout

import "strings"

// Box is one `rich.box.Box`, reduced to the seven characters a titleless,
// columnless panel and a plain table need.
//
// rich stores a box as an eight-row template and slices characters out of it by
// position; the fields below are those positions, named as rich names them.
type Box struct {
	TopLeft, Top, TopRight          string
	MidLeft, MidRight               string
	BottomLeft, Bottom, BottomRight string

	// HeadRowLeft/Mid/Cross/Right is row 3 of the template, the rule under a
	// table's header.
	HeadRowLeft, HeadRow, HeadRowCross, HeadRowRight string
	// RowLeft/Mid/Cross/Right is row 5, the rule between body rows that
	// `show_lines=True` draws.
	RowLeft, Row, RowCross, RowRight string
	// MidVertical is the column separator inside a body row.
	MidVertical string
	// TopCross and BottomCross are the column tees of the outer rules.
	TopCross, BottomCross string
}

// BoxSquare is `rich.box.SQUARE`, the default of `create_panel`.
var BoxSquare = Box{
	TopLeft: "┌", Top: "─", TopCross: "┬", TopRight: "┐",
	MidLeft: "│", MidVertical: "│", MidRight: "│",
	HeadRowLeft: "├", HeadRow: "─", HeadRowCross: "┼", HeadRowRight: "┤",
	RowLeft: "├", Row: "─", RowCross: "┼", RowRight: "┤",
	BottomLeft: "└", Bottom: "─", BottomCross: "┴", BottomRight: "┘",
}

// BoxDouble is `rich.box.DOUBLE`, which `create_lab_table` and
// `create_topology_table` use for their "nothing found" panels.
var BoxDouble = Box{
	TopLeft: "╔", Top: "═", TopCross: "╦", TopRight: "╗",
	MidLeft: "║", MidVertical: "║", MidRight: "║",
	HeadRowLeft: "╠", HeadRow: "═", HeadRowCross: "╬", HeadRowRight: "╣",
	RowLeft: "╠", Row: "═", RowCross: "╬", RowRight: "╣",
	BottomLeft: "╚", Bottom: "═", BottomCross: "╩", BottomRight: "╝",
}

// BoxSquareDoubleHead is `rich.box.SQUARE_DOUBLE_HEAD`, the box of both stats
// tables.
var BoxSquareDoubleHead = Box{
	TopLeft: "┌", Top: "─", TopCross: "┬", TopRight: "┐",
	MidLeft: "│", MidVertical: "│", MidRight: "│",
	HeadRowLeft: "╞", HeadRow: "═", HeadRowCross: "╪", HeadRowRight: "╡",
	RowLeft: "├", Row: "─", RowCross: "┼", RowRight: "┤",
	BottomLeft: "└", Bottom: "─", BottomCross: "┴", BottomRight: "┘",
}

// PanelOptions is the keyword surface of `cli/ui/utils.create_panel`.
type PanelOptions struct {
	// Box selects the border characters. The zero value is [BoxSquare], which
	// is `create_panel`'s default.
	Box *Box
	// Justify is the `justify=` keyword, applied to the panel's body.
	Justify Justify
	// Width is the console width the panel expands to. Zero means
	// [DefaultWidth].
	Width int
}

// Panel is `cli/ui/utils.create_panel` rendered: the lines of a
// `rich.panel.Panel` over a `Text`, with `box=SQUARE` unless overridden.
//
// The geometry is `Panel.__rich_console__` with `expand=True`: the panel fills
// the console width, the border takes one cell on each side, the `(0, 1)`
// padding one more, so the body wraps to `width - 4`. For the golden 80-column
// console that is 76, which is why "Starting Network Scenario" sits 26 columns
// in — 25 from `Lines.justify`'s floor-halved slack, one from the padding.
//
// No title is drawn. `create_panel` accepts one, but the only call sites that
// pass it are in `LinfoCommand`, and `linfo` answers `FeatureNotAvailable` in
// 1.0 (ERROR_CODES.md §5), so the titled top rule of `panel.py:238-249` has no
// reachable caller and is not reproduced.
func Panel(message string, opts PanelOptions) []string {
	box := opts.Box
	if box == nil {
		box = &BoxSquare
	}
	w := opts.Width
	if w <= 0 {
		w = DefaultWidth
	}
	if w < 4 {
		w = 4
	}

	body := Wrap(message, w-4, opts.Justify)

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, box.TopLeft+strings.Repeat(box.Top, w-2)+box.TopRight)
	for _, line := range body {
		lines = append(lines, box.MidLeft+" "+line+" "+box.MidRight)
	}
	lines = append(lines, box.BottomLeft+strings.Repeat(box.Bottom, w-2)+box.BottomRight)
	return lines
}
