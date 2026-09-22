package cliout

import (
	"bytes"
	"strings"
	"testing"
)

func newFileConsole(out *bytes.Buffer) *Console {
	return &Console{Out: out, Err: out, Width: 80, TTY: false, Format: FormatHuman, Level: LevelInfo}
}

// TestProgressBarFinalRowMatchesRich pins the row a golden now compares. rich
// prints one render per bar on a non-terminal, at stop, and HandleProgressBar's
// columns (description, spinner, BarColumn(bar_width=None), MofNCompleteColumn,
// expand=True) carry no clock — so the row is a pure function of the
// description, the counts and the console width. The oracle's own bytes for a
// 3-device lab at COLUMNS=80 are reproduced here exactly: 19 columns of
// description, three spaces (one separator, the blank spinner cell, one
// separator), 54 bar glyphs, a space, "3/3", filling the width.
func TestProgressBarFinalRowMatchesRich(t *testing.T) {
	var buf bytes.Buffer
	b := NewProgressBar("Deploying devices", newFileConsole(&buf))
	b.Init(3)
	for i := 0; i < 3; i++ {
		b.Advance()
	}
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	want := "[Deploying devices]   " + strings.Repeat("━", 54) + " 3/3\n"
	if buf.String() != want {
		t.Fatalf("final row = %q\n          want %q", buf.String(), want)
	}
	if got := len([]rune(strings.TrimSuffix(buf.String(), "\n"))); got != 80 {
		t.Fatalf("final row is %d columns, want 80", got)
	}
}

// TestProgressBarUnfinishedRowCarriesSpinner covers a compatibility case found
// during Layer A review. `SpinnerColumn` blanks its cell on a *finished*
// task — rich's `task.finished` is `completed >= total` — not on the last
// render, so a display torn down with items still outstanding prints a braille
// frame even at stop. This renderer blanked the cell whenever the render was
// the final one, which emitted a spinner-free row exactly where Python emits a
// time-derived one: NORMALIZATION.md §6.6 drops Python's row and would have
// kept the Go row, so a failure part-way through a deploy diffed against a
// golden that has no such line.
func TestProgressBarUnfinishedRowCarriesSpinner(t *testing.T) {
	var buf bytes.Buffer
	b := NewProgressBar("Deploying devices", newFileConsole(&buf))
	b.Init(3)
	b.Advance()
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	if !strings.ContainsAny(got, string(spinnerFrames)) {
		t.Fatalf("unfinished final row carries no spinner frame: %q", got)
	}
	if !strings.Contains(got, " 1/3") {
		t.Fatalf("unfinished final row lost its counts: %q", got)
	}
}

// TestProgressBarZeroItemsIsFinished: `total=0` makes rich's `completed >=
// total` true from the start, so an empty deploy prints a blank spinner cell,
// not a frame.
func TestProgressBarZeroItemsIsFinished(t *testing.T) {
	var buf bytes.Buffer
	b := NewProgressBar("Deploying devices", newFileConsole(&buf))
	b.Init(0)
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(buf.String(), string(spinnerFrames)) {
		t.Fatalf("a finished zero-item bar rendered a spinner frame: %q", buf.String())
	}
}

// TestProgressBarDrawsNothingIntermediateOnAFile is rich's non-terminal Live
// arm: every intermediate refresh is suppressed and only the render at stop is
// printed.
func TestProgressBarDrawsNothingIntermediateOnAFile(t *testing.T) {
	var buf bytes.Buffer
	b := NewProgressBar("Deploying devices", newFileConsole(&buf))
	b.Init(2)
	b.Advance()
	if buf.Len() != 0 {
		t.Fatalf("intermediate render reached a file: %q", buf.String())
	}
	b.Advance()
	if err := b.Finish(); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("%d rows printed, want 1: %q", n, buf.String())
	}
}
