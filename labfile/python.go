package labfile

import (
	"strings"
	"unicode"
)

// This file holds the CPython primitives the parsers feed raw file text into:
// `str.strip()`, the character classes of a `str` regex pattern, and the quote
// deletion both parsers apply to a value.
//
// They are restated here rather than imported because `model`'s copies are
// unexported and `internal/util`'s are the `int()` variants, which are a
// *different* set — `int()` rejects U+001C-U+001F and `strip()` removes them
// (see [util.PyInt]). Getting the two confused changes which lines parse.

// pySpace reports whether r is whitespace for `str.strip()` and for a `\s` in a
// `str` regex pattern. Both are CPython's `Py_UNICODE_ISSPACE`, so one
// predicate serves both.
//
// [unicode.IsSpace] is that set minus U+001C-U+001F, which Python does treat as
// whitespace. The difference is reachable from a lab.conf: a line indented with
// a U+001C strips to nothing on the oracle and would keep it here.
func pySpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1C && r <= 0x1F)
}

// pyStrip is `str.strip()` with no argument.
func pyStrip(s string) string { return strings.TrimFunc(s, pySpace) }

// isWordRune is `\w` in a `str` regex pattern: RULINGS.md OQ-14a pins it to
// Unicode `[\p{L}\p{N}_]`, not RE2's ASCII `\w`.
//
// It decides three things at once — which meta names are legal (`pc1[éth]`),
// which collision-domain names are legal (`pc1[0]=имя`) and which lab.dep
// device names are legal — and an ASCII class would turn every one of those
// lines into a syntax error.
func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// isDeviceNameRune is the `[a-z0-9_]` of the lab.conf device-line pattern.
//
// It is a literal character range, not a class with a Unicode meaning, which is
// the whole reason device names are ASCII-only while everything around them is
// not (README SURPRISE 16-17).
func isDeviceNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
}

// isQuote reports whether r is one of the two characters the lab.conf value
// class excludes and the optional quote group accepts.
func isQuote(r rune) bool { return r == '"' || r == '\'' }

// stripQuotes is the `.replace('"', …).replace("'", …)` pair that deletes both
// quote characters, wherever in the string they appear.
//
// On a lab.conf *machine* value it is dead code — the matched value class
// `[^"']+` already excludes both characters, so `pc1[image]="ka"tha"ra"` fails
// to match rather than losing its inner quotes (README SURPRISE 4). On a `LAB_*`
// value and on a `-o` option it really fires, which is why `LAB_AUTHOR='O"Brien'`
// stores `OBrien`.
var stripQuotes = strings.NewReplacer(`"`, "", `'`, "").Replace
