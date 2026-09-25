// This file is `rich/panel.py` and the two `rich/box.py` boxes the CLI names,
// reduced to the one configuration `cli/ui/utils.create_panel` ever builds:
// `expand=True`, `padding=(0, 1)`, no subtitle, and the title used by `linfo`.

package cliout

import (
	"strings"
	"unicode/utf8"
)

// Box is one `rich.box.Box`, reduced to the seven characters a titleless,
// columnless panel and a plain table need.
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
	// Title is centered in the top border, as linfo's panels render it.
	Title string
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
	top := box.TopLeft + strings.Repeat(box.Top, w-2) + box.TopRight
	if opts.Title != "" {
		title := " " + opts.Title + " "
		if titleWidth := utf8.RuneCountInString(title); titleWidth <= w-2 {
			left := (w - 2 - titleWidth) / 2
			top = box.TopLeft + strings.Repeat(box.Top, left) + title +
				strings.Repeat(box.Top, w-2-left-titleWidth) + box.TopRight
		}
	}
	lines = append(lines, top)
	for _, line := range body {
		lines = append(lines, box.MidLeft+" "+line+" "+box.MidRight)
	}
	lines = append(lines, box.BottomLeft+strings.Repeat(box.Bottom, w-2)+box.BottomRight)
	return lines
}
