// This file is `shlex.split(s)` and `shlex.join(argv)` from the CPython
// standard library, neither of which client-go provides and both of which this
// backend needs: `split` for the device shell (`KubernetesMachine.py:723,725`),
// the entrypoint (`:463`), the `args` meta (`:466`) and a string `command`
// handed to `exec` (`:815`); `join` for the `binary` field of
// `MachineBinaryError` (`:917`).

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
func ShlexJoin(argv []string) string {
	quoted := make([]string, 0, len(argv))
	for _, arg := range argv {
		quoted = append(quoted, shlexQuote(arg))
	}
	return strings.Join(quoted, " ")
}

// shlexQuote is `shlex.quote(s)` (CPython `Lib/shlex.py`):
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
func commandWords(command kathara.Command) ([]string, error) {
	if argv, isList := command.Argv(); isList {
		return argv, nil
	}
	line, _ := command.Line()
	return ShlexSplit(line)
}
