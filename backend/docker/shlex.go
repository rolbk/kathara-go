// This file is `shlex.split(s)` from the CPython standard library, which the
// Docker backend applies to four different things and which the Go SDK does
// not provide: the device shell (`DockerMachine.py:673,675`), the entrypoint
// (`:358`), the `args` meta (`:361`) and a string `command` handed to `exec`
// (`:803`). docker-py applies it again to the string form of an exec command
// (`utils.split_command`), which is why `_wait_startup_execution`'s literal
// `"cat /tmp/EOS"` becomes two words.

package docker

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
		// empty", which is the whole of the `''` case. CPython tracks it as
		// `self.token != ''` plus the quote state; a bool is the same thing.
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

// commandWords resolves a [kathara.Command] to the argv the daemon gets:
// the words as given for the list form, and [ShlexSplit] of the line for the
// string form.
func commandWords(command kathara.Command) ([]string, error) {
	if argv, isList := command.Argv(); isList {
		return argv, nil
	}
	line, _ := command.Line()
	return ShlexSplit(line)
}
