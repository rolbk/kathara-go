// Package cliout is the output layer of `cmd/kathara` (PACKAGE_GRAPH.md row
// 12): the three renderers of PORT_SPEC §5 — `human`, `json`, `jsonl` — plus
// the event subscribers that draw progress while a deploy runs.
//
// # Why a rich reimplementation lives here
//
// The `human` renderer is under a byte-parity obligation. PORT_SPEC §9 Layer A
// captures Python's stdout and diffs it, and what Python writes there is
// `rich`: panels laid out to the console width, `Text` word-wrapped by
// `rich/_wrap.py`, a `Tree` for the volume prompt, and `RichHandler`'s
// eight-column log level gutter. text.go and panel.go are therefore a faithful
// port of the *subset* of rich the CLI reaches, not a general layout library:
// [Wrap] is `Text.wrap` + `Lines.justify` + `Text.rstrip_end`, and [Panel] is
// `Panel.__rich_console__` with `expand=True` and `padding=(0, 1)`.
//
// PACKAGE_GRAPH.md's dependency table names `lipgloss` for this row. It is not
// used: lipgloss wraps with `muesli/reflow`, whose fold points differ from
// `divide_line`'s on the very first golden that exercises them (the lab
// metadata panel of `18-quagga-bgp-announcement` folds after a trailing comma
// and *keeps* the trailing space up to the fold width, which reflow strips), so
// a lipgloss panel would have failed Layer A. DIVERGENCES.md records the
// substitution.
//
// # Stream assignment
//
// JSON_CLI_CONTRACT.md A1 pins the split this package implements:
//
//   - [FormatHuman] — everything on stdout, Python's assignment, including log
//     records, panels, tables, progress bars and prompts.
//   - [FormatJSON] / [FormatJSONL] — stdout carries protocol only (one envelope,
//     or the event stream); log records go to stderr as plain text with no
//     markup; progress bars, spinners and prompts are not rendered at all, and
//     the prompts answer themselves per §1.5.
//
// # What is deliberately not wrapped
//
// A log record is emitted as one logical line. Python's `RichHandler` folds the
// message into `width - 9` columns and indents the continuations, and the
// golden harness undoes exactly that (NORMALIZATION.md §6.3) because the fold
// column depends on host paths inside the message. Emitting the unfolded line
// is the same thing after normalization and cannot drift with the console
// width; DIVERGENCES.md records it.
package cliout
