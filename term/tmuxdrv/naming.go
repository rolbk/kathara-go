package tmuxdrv

import (
	"fmt"
	"strings"
)

// SessionPrefix namespaces every session this package creates.
const SessionPrefix = "kathara_"

// SessionName is the session naming behavior: one tmux session per network scenario.
func SessionName(labName, labHash string) string {
	ident := SanitizeName(labName)
	if ident == "" {
		ident = SanitizeName(labHash)
	}
	if ident == "" {
		// Both empty: the caller has no scenario identity at all. Fall back to
		// a fixed name rather than emitting a bare prefix that would collide
		// with every other identity-less scenario in a surprising way.
		ident = "default"
	}
	return SessionPrefix + ident
}

// SanitizeName makes s usable and round-trippable as a tmux session name.
func SanitizeName(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '.' || r == ':':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case r == ' ' || r == '\t':
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	out := b.String()
	if strings.HasPrefix(out, "-") {
		out = "_" + out[1:]
	}
	return out
}

// checkSessionName rejects names that tmux would reject or silently rewrite.
// EnsureSession calls it so that a caller who built a name by hand instead of
// through SessionName fails immediately rather than at the second invocation.
func checkSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty session name", ErrInvalidName)
	}
	if SanitizeName(name) != name {
		return fmt.Errorf("%w: session name %q would be rewritten by tmux; pass it through SessionName/SanitizeName",
			ErrInvalidName, name)
	}
	return nil
}

// checkWindowName rejects window names that would corrupt the tab-delimited
// -F output ListWindows parses, or that tmux would read as a flag.
func checkWindowName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty window name", ErrInvalidName)
	}
	if strings.ContainsAny(name, "\t\n\r") {
		return fmt.Errorf("%w: window name %q contains a tab or newline", ErrInvalidName, name)
	}
	if strings.HasPrefix(name, "-") {
		return fmt.Errorf("%w: window name %q starts with '-'", ErrInvalidName, name)
	}
	return nil
}

// sessionTarget renders an exact-match session target ("=name").
// Without the '=' tmux would prefix-match, and "lab" would resolve to "lab1".
func sessionTarget(session string) string { return "=" + session }

// windowTarget renders an exact-match window target ("=session:=window").
func windowTarget(session, window string) string {
	return "=" + session + ":=" + window
}
