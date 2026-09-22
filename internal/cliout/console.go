package cliout

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/term"
)

// DefaultWidth is rich's own non-TTY console width (`rich/console.py`, the
// `width or 80` fallback), and the width the Layer A harness pins with
// `COLUMNS=80` (NORMALIZATION.md §1).
const DefaultWidth = 80

type Format string

const (
	// FormatHuman is Python v3.8.3's output, byte-golden, everything on
	// stdout.
	FormatHuman Format = "human"
	// FormatJSON is one envelope object on stdout and nothing else.
	FormatJSON Format = "json"
	// FormatJSONL is a stream of newline-delimited event objects on stdout.
	FormatJSONL Format = "jsonl"
)

// Machine reports whether the format is one of the two machine-readable ones,
// where stdout carries protocol only and no UI is rendered.
func (f Format) Machine() bool { return f == FormatJSON || f == FormatJSONL }

// Level is a `logging` level. The names are `AVAILABLE_DEBUG_LEVELS`
// (`setting/Setting.py:17`) minus EXCEPTION, which is not a level at all — it
// selects `logging.exception` at the catch-all and maps to DEBUG for the logger
// itself (`src/kathara.py:118-119`).
type Level int

// The five `logging` levels, with CPython's numeric values so that comparisons
// order the same way.
const (
	LevelDebug    Level = 10
	LevelInfo     Level = 20
	LevelWarning  Level = 30
	LevelError    Level = 40
	LevelCritical Level = 50
)

// String is the level's name, which is also the text `RichHandler` puts in the
// eight-column gutter.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarning:
		return "WARNING"
	case LevelError:
		return "ERROR"
	case LevelCritical:
		return "CRITICAL"
	}
	return strconv.Itoa(int(l))
}

// ParseLevel maps a `debug_level` setting onto a logger level, reproducing
// `src/kathara.py:118-119`: EXCEPTION is not a level, it is DEBUG plus a
// traceback at the catch-all. An unknown name is DEBUG, which is the branch
// the entrypoint takes when the settings cannot even be read.
func ParseLevel(name string) Level {
	switch strings.ToUpper(name) {
	case "CRITICAL":
		return LevelCritical
	case "ERROR":
		return LevelError
	case "WARNING":
		return LevelWarning
	case "INFO":
		return LevelInfo
	}
	return LevelDebug
}

// Console is where the CLI writes: `Command.console` and the logging handler at
// once.
type Console struct {
	// Out is stdout. In human mode it takes everything; in json/jsonl it
	// takes the envelope and nothing else.
	Out io.Writer
	Err io.Writer
	// Width is the console width panels and tables expand to.
	Width int
	// TTY reports whether Out is a terminal, which is what decides whether a
	// live progress display is drawn at all.
	TTY bool
	// Format selects the renderer.
	Format Format
	// Level is the logging threshold.
	Level Level

	Traceback bool

	mu sync.Mutex
}

// New builds a Console over the process's own streams, sized the way rich sizes
// one (`Console.size`): a dumb terminal is 80 columns flat, otherwise the
// terminal's own width, which an explicit `COLUMNS` then overrides.
func New(out, errw io.Writer, format Format, level Level) *Console {
	c := &Console{
		Out:    out,
		Err:    errw,
		Width:  DefaultWidth,
		Format: format,
		Level:  level,
	}
	if f, ok := out.(*os.File); ok {
		fd := int(f.Fd())
		c.TTY = term.IsTerminal(fd)
		if os.Getenv("TERM") != "dumb" {
			if w, _, err := term.GetSize(fd); err == nil && w > 0 {
				c.Width = w
			}
		}
	}
	if cols := os.Getenv("COLUMNS"); cols != "" {
		if w, err := strconv.Atoi(cols); err == nil && w > 0 {
			c.Width = w
		}
	}
	return c
}

// Print writes one already-rendered line to stdout, with a newline. It is
// `Console.print` for a plain string: nothing is written in json/jsonl mode,
// where stdout carries protocol only.
func (c *Console) Print(line string) {
	if c.Format.Machine() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = fmt.Fprintln(c.Out, line)
}

// PrintLines writes a block, e.g. the output of [Panel].
func (c *Console) PrintLines(lines []string) {
	if c.Format.Machine() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, line := range lines {
		_, _ = fmt.Fprintln(c.Out, line)
	}
}

// PrintPanel is `self.console.print(create_panel(...))`.
func (c *Console) PrintPanel(message string, opts PanelOptions) {
	if c.Format.Machine() {
		return
	}
	if opts.Width == 0 {
		opts.Width = c.Width
	}
	c.PrintLines(Panel(message, opts))
}

// Log emits one `logging` record.
func (c *Console) Log(level Level, format string, args ...any) {
	if level < c.Level {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.Format.Machine() {
		_, _ = fmt.Fprintf(c.Err, "%s: %s\n", strings.ToLower(level.String()), msg)
		return
	}
	for i, row := range strings.Split(msg, "\n") {
		if i == 0 {
			_, _ = fmt.Fprintf(c.Out, "%-8s %s\n", level.String(), row)
			continue
		}
		_, _ = fmt.Fprintf(c.Out, "%-8s %s\n", "", row)
	}
}

// Debug, Info, Warning, Error and Critical are the five `logging` entry points
// the CLI calls.
func (c *Console) Debug(format string, args ...any) { c.Log(LevelDebug, format, args...) }

// Info is `logging.info`.
func (c *Console) Info(format string, args ...any) { c.Log(LevelInfo, format, args...) }

// Warning is `logging.warning`.
func (c *Console) Warning(format string, args ...any) { c.Log(LevelWarning, format, args...) }

// Error is `logging.error`.
func (c *Console) Error(format string, args ...any) { c.Log(LevelError, format, args...) }

// Critical is `logging.critical`.
func (c *Console) Critical(format string, args ...any) { c.Log(LevelCritical, format, args...) }

// WriteOut writes raw bytes to stdout with no framing. It is `exec`'s
// `sys.stdout.write` (`cli/command/ExecCommand.py:108`), which adds no newline
// and no styling.
func (c *Console) WriteOut(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.Out.Write(p)
}

// WriteErr writes raw bytes to stderr. It is `exec`'s `sys.stderr.write`
// (`ExecCommand.py:112`) — the one place Python's human mode writes to stderr
// at all.
func (c *Console) WriteErr(p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, _ = c.Err.Write(p)
}
