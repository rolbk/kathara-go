package util

import (
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// pyLower is Python's `str.lower()`.
func pyLower(s string) string {
	return cases.Lower(language.Und).String(s)
}
