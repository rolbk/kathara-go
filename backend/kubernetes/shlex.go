// This file is `shlex.split(s)` and `shlex.join(argv)` from the CPython
// standard library, neither of which client-go provides and both of which this
// backend needs: `split` for the device shell (`KubernetesMachine.py:723,725`),
// the entrypoint (`:463`), the `args` meta (`:466`) and a string `command`
// handed to `exec` (`:815`); `join` for the `binary` field of
// `MachineBinaryError` (`:917`).
//
// The `split` half is character-for-character the copy `backend/docker` carries
// (PACKAGE_GRAPH.md §1.2 gives neither backend an edge to the other, so the two
// are pinned separately against the same CPython source; shlex_test.go here
// runs the same vectors). The `join` half has no Docker counterpart: the Docker
// backend reads the missing binary out of the OCI regexp's capture groups,
// while Megalos never sees the binary name and quotes the whole command
// instead.

package kubernetes

import (
	"strings"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// The four character classes of `shlex.shlex(s, posix=True)` with
// `whitespace_split = True`, which is what `shlex.split` configures
// (CPython `Lib/shlex.py`).
const (
	// shlexWhitespace is `shlex.whitespace`, the default " \t\r\n". Note it is
	// ASCII-only: a NBSP is an ordinary word character, in Python and here.
	shlexWhitespace = " \t\r\n"
	// shlexQuotes is `shlex.quotes`.
	shlexQuotes = "'\""
	// shlexEscape is `shlex.escape`, the backslash.
	shlexEscape = '\\'
	// shlexEscapedQuotes is `shlex.escapedquotes`: the quote character inside
	// which the escape character is still special. Only the double quote is
	// listed, which is why `'a\'` is a complete two-character token and
	// `"a\"` is an unterminated string.
	shlexEscapedQuotes = `"`
)

// ShlexSplit is `shlex.split(s)`: POSIX word splitting with quote and escape
// handling, comments off.
//
// The rules, each of which is observable through `kathara exec pc1 "…"`:
//
//   - Runs of `shlex.whitespace` separate words and are otherwise discarded.
//   - `'…'` is literal: no escape has any meaning inside it, so `'a\b'` is the
//     four characters `a\b`.
//   - `"…"` honours the backslash, but only in front of a `"` or another `\`
//     ([shlexEscapedQuotes] + [shlexEscape]); `"a\b"` keeps both characters.
//   - Outside quotes, `\` takes the next character literally, whatever it is,
//     including a newline — CPython does NOT do line continuation here.
//   - An empty quoted string is a real, empty word.
//   - `#` is not a comment: `shlex.split` passes `comments=False`.
//
// Errors are the two ValueErrors CPython raises, with its exact messages:
// "No closing quotation" for an unterminated quote and "No escaped character"
// for a trailing backslash. Both carry [kerrors.ErrValue], which is where
// ERROR_CODES.md §1.2 buckets a bare ValueError.
func ShlexSplit(s string) ([]string, error) {
	var (
		tokens  []string
		current strings.Builder
		// started distinguishes "no token yet" from "a token that is so far
		// empty", which is the whole of the `''` case.
		started bool
	)

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]

		switch {
		case strings.ContainsRune(shlexWhitespace, r):
			if started {
				tokens = append(tokens, current.String())
				current.Reset()
				started = false
			}

		case r == shlexEscape:
			if i+1 >= len(runes) {
				return nil, kerrors.NewValue("No escaped character")
			}
			i++
			current.WriteRune(runes[i])
			started = true

		case strings.ContainsRune(shlexQuotes, r):
			quote := r
			started = true
			closed := false
			for i++; i < len(runes); i++ {
				c := runes[i]
				if c == quote {
					closed = true
					break
				}
				if c == shlexEscape && strings.ContainsRune(shlexEscapedQuotes, quote) {
					if i+1 >= len(runes) {
						return nil, kerrors.NewValue("No escaped character")
					}
					next := runes[i+1]
					// CPython: only a character that is itself the enclosing
					// quote or the escape character is consumed by the escape;
					// anything else keeps the backslash AND the character.
					if next == quote || next == shlexEscape {
						i++
						current.WriteRune(next)
					} else {
						current.WriteRune(c)
					}
					continue
				}
				current.WriteRune(c)
			}
			if !closed {
				return nil, kerrors.NewValue("No closing quotation")
			}

		default:
			current.WriteRune(r)
			started = true
		}
	}

	if started {
		tokens = append(tokens, current.String())
	}
	return tokens, nil
}

// ShlexJoin is `shlex.join(argv)`:
// `' '.join(shlex.quote(arg) for arg in argv)`.
//
// It is what fills `MachineBinaryError.binary` on this backend
// (`KubernetesMachine.py:917`), which is a field the JSON envelope names and
// kathara-lab-checker reads (PORT_SPEC §4.3) — so the whole command, quoted,
// lands where the Docker backend puts a bare executable name. That asymmetry is
// Python's: Megalos learns only that the exec failed, never which word of it
// was missing.
func ShlexJoin(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shlexQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// shlexQuote is `shlex.quote(s)` (CPython `Lib/shlex.py`):
//
//	if not s: return "''"
//	if _find_unsafe(s) is None: return s
//	return "'" + s.replace("'", "'\"'\"'") + "'"
//
// where `_find_unsafe` is `re.compile(r'[^\w@%+=:,./-]', re.ASCII).search`. The
// `re.ASCII` flag is the detail worth spelling out: `\w` is `[A-Za-z0-9_]` and
// nothing else, so a non-ASCII letter is UNSAFE and gets quoted — `é` comes
// back as `'é'` (oracle-verified).
func shlexQuote(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsFunc(s, shlexUnsafe) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// shlexUnsafe is `_find_unsafe`'s character class, negated: true for a rune
// that forces quoting.
func shlexUnsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z',
		r >= 'A' && r <= 'Z',
		r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("_@%+=:,./-", r)
}

// commandWords resolves a [kathara.Command] to the argv the API server gets:
// the words as given for the list form, and [ShlexSplit] of the line for the
// string form.
//
// This is `command = shlex.split(command) if type(command) is str else command`
// (`KubernetesMachine.py:815`) — Kathará splits, the SDK never does.
func commandWords(command kathara.Command) ([]string, error) {
	if argv, isList := command.Argv(); isList {
		return argv, nil
	}
	line, _ := command.Line()
	return ShlexSplit(line)
}
