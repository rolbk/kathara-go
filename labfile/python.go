package labfile

import (
	"strings"
	"unicode"
)

// This file holds the CPython primitives the parsers feed raw file text into:
// `str.strip()`, the character classes of a `str` regex pattern, and the quote
// deletion both parsers apply to a value.
// They are restated here rather than imported because `model`'s copies are
// unexported and `internal/util`'s are the `int()` variants, which are a
// *different* set — `int()` rejects U+001C-U+001F and `strip()` removes them
// (see [util.PyInt]). Getting the two confused changes which lines parse.

// pySpace reports whether r is whitespace for `str.strip()` and for a `\s` in a
// `str` regex pattern. Both are CPython's `Py_UNICODE_ISSPACE`, so one
// predicate serves both.
func pySpace(r rune) bool {
	return unicode.IsSpace(r) || (r >= 0x1C && r <= 0x1F)
}

// pyStrip is `str.strip()` with no argument.
func pyStrip(s string) string { return strings.TrimFunc(s, pySpace) }

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// isDeviceNameRune is the `[a-z0-9_]` of the lab.conf device-line pattern.
func isDeviceNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
}

// isQuote reports whether r is one of the two characters the lab.conf value
// class excludes and the optional quote group accepts.
func isQuote(r rune) bool { return r == '"' || r == '\'' }

// stripQuotes is the `.replace('"', …).replace("'", …)` pair that deletes both
// quote characters, wherever in the string they appear.
var stripQuotes = strings.NewReplacer(`"`, "", `'`, "").Replace
