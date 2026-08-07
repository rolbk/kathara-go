// This file is `cli/ui/utils.create_lab_table` and `create_topology_table`
// plus the slice of `rich.table.Table` they reach: the column-width solver
// (`Table._calculate_column_widths`, `_collapse_widths`, `ratio_distribute`,
// `ratio_reduce`) and the row renderer.
//
// Neither table is covered by a Layer A golden — no recorded scenario runs
// `kathara list` — so the port of the solver is here for behavioural fidelity
// rather than to satisfy a byte diff. It is reproduced rather than approximated
// because a hand-rolled "divide the width evenly" would put the columns in
// different places for every device count, and `kathara list` is the command
// users read most.

package cliout

import (
	"math"
	"strings"
	"time"
)

// Table is the subset of `rich.table.Table` the two CLI tables configure.
type Table struct {
	// Title is rendered centred above the table, at the table's own width.
	Title string
	// Box is the border set. Both CLI tables use [BoxSquareDoubleHead].
	Box *Box
	// ShowLines is `show_lines=True`: a rule between every pair of body rows.
	ShowLines bool
	// Expand is `expand=True`: grow the columns to fill the console.
	Expand bool
	// Columns are the header cells, in order.
	Columns []string
	// Rows are the body cells, one slice per row, aligned with Columns.
	Rows [][]string
}

// Render lays the table out for a console of the given width and returns its
// lines.
func (t *Table) Render(width int) []string {
	if width <= 0 {
		width = DefaultWidth
	}
	box := t.Box
	if box == nil {
		box = &BoxSquareDoubleHead
	}
	if len(t.Columns) == 0 {
		return nil
	}

	widths := t.columnWidths(width)
	tableWidth := sum(widths) + t.extraWidth()

	var out []string
	if t.Title != "" {
		out = append(out, Wrap(t.Title, tableWidth, JustifyCenter)...)
	}

	out = append(out, rule(box.TopLeft, box.Top, box.TopCross, box.TopRight, widths))
	out = append(out, t.renderRow(box, t.Columns, widths)...)
	out = append(out, rule(box.HeadRowLeft, box.HeadRow, box.HeadRowCross, box.HeadRowRight, widths))
	for i, row := range t.Rows {
		out = append(out, t.renderRow(box, row, widths)...)
		if t.ShowLines && i != len(t.Rows)-1 {
			out = append(out, rule(box.RowLeft, box.Row, box.RowCross, box.RowRight, widths))
		}
	}
	out = append(out, rule(box.BottomLeft, box.Bottom, box.BottomCross, box.BottomRight, widths))
	return out
}

// extraWidth is `Table._extra_width`: the two edges plus one divider between
// each pair of columns.
func (t *Table) extraWidth() int { return 2 + len(t.Columns) - 1 }

// cellPadding is rich's default `padding=(0, 1)`, i.e. one cell on each side.
const cellPadding = 2

// columnWidths is `Table._calculate_column_widths`, restricted to the
// configuration the CLI builds: no fixed widths, no ratios, no `no_wrap`, and
// every column wrappable.
func (t *Table) columnWidths(maxWidth int) []int {
	widths := make([]int, len(t.Columns))
	for i := range t.Columns {
		lo, hi := t.measureColumn(i)
		_ = lo
		if hi < 1 {
			hi = 1
		}
		widths[i] = hi
	}

	extra := t.extraWidth()
	tableWidth := sum(widths) + extra

	switch {
	case tableWidth > maxWidth:
		wrapable := make([]bool, len(widths))
		for i := range wrapable {
			wrapable[i] = true
		}
		widths = collapseWidths(widths, wrapable, maxWidth-extra)
		if sum(widths)+extra > maxWidth {
			excess := sum(widths) + extra - maxWidth
			ones := make([]int, len(widths))
			for i := range ones {
				ones[i] = 1
			}
			widths = ratioReduce(excess, ones, widths, widths)
		}
	case tableWidth < maxWidth && t.Expand:
		pad := ratioDistribute(maxWidth-tableWidth, widths, nil)
		for i := range widths {
			widths[i] += pad[i]
		}
	}

	for i := range widths {
		if widths[i] < cellPadding+1 {
			widths[i] = cellPadding + 1
		}
	}
	return widths
}

// measureColumn is `Table._measure_column`: the minimum is the widest single
// word, the maximum the widest whole line, both including the cell padding.
func (t *Table) measureColumn(i int) (minWidth, maxWidth int) {
	cells := make([]string, 0, len(t.Rows)+1)
	cells = append(cells, t.Columns[i])
	for _, row := range t.Rows {
		if i < len(row) {
			cells = append(cells, row[i])
		}
	}
	for _, cell := range cells {
		for _, line := range strings.Split(cell, "\n") {
			if n := CellLen(line); n > maxWidth {
				maxWidth = n
			}
			for _, word := range strings.Fields(line) {
				if n := CellLen(word); n > minWidth {
					minWidth = n
				}
			}
		}
	}
	return minWidth + cellPadding, maxWidth + cellPadding
}

// renderRow renders one row's cells, wrapping each to its column and padding
// the short ones down to the row's height.
func (t *Table) renderRow(box *Box, cells []string, widths []int) []string {
	columns := make([][]string, len(widths))
	height := 1
	for i := range widths {
		content := ""
		if i < len(cells) {
			content = cells[i]
		}
		lines := Wrap(content, widths[i]-cellPadding, JustifyDefault)
		if len(lines) == 0 {
			lines = []string{strings.Repeat(" ", widths[i]-cellPadding)}
		}
		columns[i] = lines
		if len(lines) > height {
			height = len(lines)
		}
	}

	out := make([]string, 0, height)
	for row := 0; row < height; row++ {
		var b strings.Builder
		b.WriteString(box.MidLeft)
		for i := range widths {
			if i > 0 {
				b.WriteString(box.MidVertical)
			}
			line := strings.Repeat(" ", widths[i]-cellPadding)
			if row < len(columns[i]) {
				line = columns[i][row]
			}
			b.WriteString(" ")
			b.WriteString(line)
			b.WriteString(" ")
		}
		b.WriteString(box.MidRight)
		out = append(out, b.String())
	}
	return out
}

// rule draws one horizontal border row.
func rule(left, mid, cross, right string, widths []int) string {
	var b strings.Builder
	b.WriteString(left)
	for i, w := range widths {
		if i > 0 {
			b.WriteString(cross)
		}
		b.WriteString(strings.Repeat(mid, w))
	}
	b.WriteString(right)
	return b.String()
}

func sum(xs []int) int {
	total := 0
	for _, x := range xs {
		total += x
	}
	return total
}

// ratioDistribute is `rich._ratio.ratio_distribute`.
func ratioDistribute(total int, ratios []int, minimums []int) []int {
	work := make([]int, len(ratios))
	copy(work, ratios)
	if minimums != nil {
		for i := range work {
			if minimums[i] == 0 {
				work[i] = 0
			}
		}
	}
	totalRatio := sum(work)
	remaining := total
	out := make([]int, len(work))
	for i, ratio := range work {
		minimum := 0
		if minimums != nil {
			minimum = minimums[i]
		}
		distributed := remaining
		if totalRatio > 0 {
			distributed = int(math.Ceil(float64(ratio) * float64(remaining) / float64(totalRatio)))
			if distributed < minimum {
				distributed = minimum
			}
		}
		out[i] = distributed
		totalRatio -= ratio
		remaining -= distributed
	}
	return out
}

// ratioReduce is `rich._ratio.ratio_reduce`.
//
// It rounds with CPython's `round()`, which is round-half-to-even, not Go's
// `math.Round`, which rounds half away from zero. The difference shows on the
// top-level help table, whose second column reduces by an exact half-integer.
func ratioReduce(total int, ratios, maximums, values []int) []int {
	work := make([]int, len(ratios))
	copy(work, ratios)
	for i := range work {
		if maximums[i] == 0 {
			work[i] = 0
		}
	}
	totalRatio := sum(work)
	out := make([]int, len(values))
	if totalRatio == 0 {
		copy(out, values)
		return out
	}
	remaining := total
	for i, ratio := range work {
		if ratio == 0 || totalRatio <= 0 {
			out[i] = values[i]
			continue
		}
		distributed := int(math.RoundToEven(float64(ratio) * float64(remaining) / float64(totalRatio)))
		if distributed > maximums[i] {
			distributed = maximums[i]
		}
		out[i] = values[i] - distributed
		remaining -= distributed
		totalRatio -= ratio
	}
	return out
}

// collapseWidths is `Table._collapse_widths`: shrink the widest wrappable
// column down to the second widest, repeatedly, until the table fits.
func collapseWidths(widths []int, wrapable []bool, maxWidth int) []int {
	out := make([]int, len(widths))
	copy(out, widths)

	anyWrapable := false
	for _, w := range wrapable {
		anyWrapable = anyWrapable || w
	}
	if !anyWrapable {
		return out
	}

	total := sum(out)
	excess := total - maxWidth
	for total > 0 && excess > 0 {
		maxColumn := 0
		for i, w := range out {
			if wrapable[i] && w > maxColumn {
				maxColumn = w
			}
		}
		second := 0
		for i, w := range out {
			if wrapable[i] && w != maxColumn && w > second {
				second = w
			}
		}
		difference := maxColumn - second

		ratios := make([]int, len(out))
		any := false
		for i, w := range out {
			if w == maxColumn && wrapable[i] {
				ratios[i] = 1
				any = true
			}
		}
		if !any || difference == 0 {
			break
		}
		maxReduce := make([]int, len(out))
		for i := range maxReduce {
			maxReduce[i] = min(excess, difference)
		}
		out = ratioReduce(excess, ratios, maxReduce, out)
		total = sum(out)
		excess = total - maxWidth
	}
	return out
}

// Timestamp is `f"TIMESTAMP: {datetime.now()}"`, the title of both tables.
//
// CPython's `str(datetime)` writes microseconds only when they are non-zero,
// and never a timezone for a naive `datetime.now()`; both are reproduced so
// that a port's table header cannot be told from Python's by its shape.
func Timestamp(now time.Time) string {
	if now.Nanosecond()/1000 == 0 {
		return "TIMESTAMP: " + now.Format("2006-01-02 15:04:05")
	}
	return "TIMESTAMP: " + now.Format("2006-01-02 15:04:05.000000")
}

// EmptyBlock is the `rich.console.Group` that `create_lab_table` and
// `create_topology_table` return when nothing matched: a centred italic
// timestamp line, then a DOUBLE-boxed red panel carrying message.
func EmptyBlock(timestamp, message string, width int) []string {
	if width <= 0 {
		width = DefaultWidth
	}
	out := Wrap(timestamp, width, JustifyCenter)
	out = append(out, Panel(message, PanelOptions{Box: &BoxDouble, Justify: JustifyCenter, Width: width})...)
	for i, line := range out {
		out[i] = strings.TrimRight(line, " ")
	}
	return out
}

// ColumnHeader is `x.replace('_', ' ').upper()`, the transform
// `create_lab_table` applies to every `to_dict()` key
// (`cli/ui/utils.py:82`).
func ColumnHeader(key string) string {
	return strings.ToUpper(strings.ReplaceAll(key, "_", " "))
}

// RenderPlainTable is `Kathara.strings.formatted_strings()`: a two-column
// `rich.Table` with `show_header=False, show_edge=False, show_lines=False,
// box=None`, laid out to the console width.
//
// With no box the table has no extra width at all (`Table._extra_width` adds
// nothing when `self.box` is None), so the two columns divide the whole width
// between them — at 80 that is 10 and 70, i.e. content widths of 8 and 68,
// which is where `lconfig`'s description folds after "in a".
func RenderPlainTable(rows [][]string, width int) []string {
	if width <= 0 {
		width = DefaultWidth
	}
	if len(rows) == 0 {
		return nil
	}

	columns := len(rows[0])
	t := &Table{Columns: make([]string, columns), Rows: rows}
	widths := make([]int, columns)
	for i := range widths {
		_, hi := t.measureColumn(i)
		widths[i] = hi
	}
	// `columnWidths` measures the header row too; a boxless table has none, so
	// the widths are recomputed here over the body only, and then reduced with
	// the same solver.
	if total := sum(widths); total > width {
		wrapable := make([]bool, columns)
		for i := range wrapable {
			wrapable[i] = true
		}
		widths = collapseWidths(widths, wrapable, width)
	}

	var out []string
	for _, row := range rows {
		cells := make([][]string, columns)
		height := 1
		for i := range widths {
			content := ""
			if i < len(row) {
				content = row[i]
			}
			cells[i] = Wrap(content, widths[i]-cellPadding, JustifyDefault)
			if len(cells[i]) > height {
				height = len(cells[i])
			}
		}
		for line := 0; line < height; line++ {
			var b strings.Builder
			for i := range widths {
				text := strings.Repeat(" ", widths[i]-cellPadding)
				if line < len(cells[i]) {
					text = cells[i][line]
				}
				b.WriteString(" ")
				b.WriteString(text)
				b.WriteString(" ")
			}
			out = append(out, b.String())
		}
	}
	return out
}
