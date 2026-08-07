// This file is the other half of `logging.basicConfig(handlers=[RichHandler(…)])`:
// the ported packages below `cmd/kathara` emit their `logging` calls through
// `log/slog` on the default logger (labfile's duplicate-meta warning, the two
// backends' progress and warning lines), and [NewSlogHandler] is what routes
// them into the same [Console] the commands print through.
//
// Without it those records go to `log/slog`'s default text handler, which
// writes to **stderr** with a timestamp — visible in a Layer A recording as a
// missing stdout line and an unexpected stderr one.

package cliout

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// slogHandler renders a [log/slog] record the way `RichHandler` renders a
// `logging` one.
type slogHandler struct {
	console *Console
	attrs   []slog.Attr
	groups  []string
}

// NewSlogHandler returns the handler `cmd/kathara` installs as the default, so
// that every ported package's log call lands on the same console, at the same
// level threshold, on the stream the format dictates.
func NewSlogHandler(console *Console) slog.Handler {
	return &slogHandler{console: console}
}

// Enabled maps the `logging` threshold onto slog's.
func (h *slogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return fromSlogLevel(level) >= h.console.Level
}

// Handle renders the record.
//
// Structured attributes have no Python analogue — `logging.warning` takes an
// already-interpolated string — so they are appended as ` key=value`, which
// keeps them visible without inventing a message template. Where a ported
// package needed byte parity it interpolated the message itself
// (`labfile/labconf.go:163` is the one the `syn-env` golden asserts).
func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)

	write := func(a slog.Attr) bool {
		if a.Equal(slog.Attr{}) {
			return true
		}
		b.WriteString(" ")
		if len(h.groups) > 0 {
			b.WriteString(strings.Join(h.groups, "."))
			b.WriteString(".")
		}
		b.WriteString(a.Key)
		b.WriteString("=")
		b.WriteString(fmt.Sprint(a.Value.Resolve().Any()))
		return true
	}
	for _, a := range h.attrs {
		write(a)
	}
	r.Attrs(write)

	h.console.Log(fromSlogLevel(r.Level), "%s", b.String())
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &slogHandler{console: h.console, groups: h.groups}
	next.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return next
}

func (h *slogHandler) WithGroup(name string) slog.Handler {
	next := &slogHandler{console: h.console, attrs: h.attrs}
	next.groups = append(append([]string{}, h.groups...), name)
	return next
}

// fromSlogLevel maps slog's levels onto `logging`'s. slog has no CRITICAL, so
// anything above ERROR is one — the level the entrypoint's catch-all uses.
func fromSlogLevel(level slog.Level) Level {
	switch {
	case level >= slog.LevelError+4:
		return LevelCritical
	case level >= slog.LevelError:
		return LevelError
	case level >= slog.LevelWarn:
		return LevelWarning
	case level >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}
