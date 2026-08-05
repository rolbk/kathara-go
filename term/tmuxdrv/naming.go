package tmuxdrv

import (
	"fmt"
	"strings"
)

// SessionPrefix namespaces every session this package creates.
//
// It is not decoration. "Never clobber" (spec §3.3) has to hold against the
// user's own sessions too: a network scenario named "work" must not adopt the
// unrelated tmux session "work" the user has been living in all afternoon, and
// a stale Kathara session must be recognizable in `tmux ls`.
const SessionPrefix = "kathara_"

// SessionName is the OQ-9 naming ruling: one tmux session per network scenario.
//
//	named scenario   -> "kathara_" + sanitized(name)
//	unnamed scenario -> "kathara_" + hash
//
// labHash is Lab.hash (the URL-safe md5 of the scenario's real path for
// path-parsed scenarios, of the name for named ones) — the same identity
// Kathara already uses for container labels, Docker network names and the
// Kubernetes namespace, so two invocations from the same directory always
// compute the same session.
//
// This replaces 3.8.3's behavior, where every path-parsed scenario (lab.name is
// None) shared one global session literally named "Kathara" and only vlab or
// API-named scenarios got their own. See docs/port/SPIKES/tmux.md.
//
// The name is sanitized here rather than left to tmux, because tmux silently
// rewrites '.' and ':' to '_' inside session names: an unsanitized probe would
// look for a name tmux never stored, conclude the session is absent, and try to
// create it forever.
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
//
//   - '.' and ':' become '_' (tmux does this itself; doing it here keeps the
//     name we probe with equal to the name tmux stores).
//   - control characters and whitespace become '_' (they would make the
//     newline-delimited -F output of list-sessions ambiguous).
//   - leading '-' becomes '_' so the name can never be read as a flag.
//
// It does not lower-case or truncate: tmux imposes no length limit and session
// names are case-sensitive.
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
//
// Kathara device names are validated as \w+ upstream, so this never fires in
// production; it exists so that a future caller cannot quietly break the
// listing format.
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
