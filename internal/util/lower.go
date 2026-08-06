package util

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// pyLower is Python's `str.lower()`.
//
// [strings.ToLower] is not it. Go applies the *simple* case mapping, one rune
// in and one rune out, while CPython applies the full one: "İ" lowers to the
// two code points "i" + U+0307, and a final sigma lowers to "ς" rather than
// "σ" ("ΑΣ" is "ας", "ΣΑ" is "σα"). Both differences reach a user-visible
// string, because [StrToBool] quotes the *folded* value back in its error
// message and [architectureOf] does the same in HostArchitectureError.
//
// `cases.Lower(language.Und)` is the full mapping, including the Final_Sigma
// condition. It was swept against the oracle over the whole code space plus a
// set of sigma contexts: 1,112,073 comparisons, no difference.
//
// The Caser is built per call because x/text documents a Caser as stateful
// and not safe to share between goroutines, and every function in this package
// is called concurrently from the deploy fan-out.
func pyLower(s string) string {
	return cases.Lower(language.Und).String(s)
}
