package labfile

import (
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// ParseOptions is `OptionParser.parse` (`parser/netkit/OptionParser.py:8`): the
// `-o/--pass` values of `lstart`, which become the scenario's global machine
// metadata.
//
// The result is ordered because insertion order is what `Lab.add_option` and
// `Lab.add_global_machine_metadata` preserve, and a repeated key keeps the
// position of its first appearance while taking the last value.
//
// The parse is one `str.split("=")` per value after every quote character has
// been deleted from the whole string, which has three consequences the vectors
// pin: quotes anywhere disappear (`"image=kathara/base"` is a valid pair), the
// empty key is accepted (`-o =value` yields `{"": "value"}`), and an empty
// value is fine (`-o mem=`) even though the same spelling is a syntax error in
// a lab.conf machine meta.
//
// nil and an empty slice both parse to an empty result: Python's guard is a
// truthiness test, so `-o` never given and `-o` given no values are the same.
//
// The error is [kerrors.ErrValue], carrying `Option parameter not valid: {inner}.`
// where inner is CPython's own unpacking message. Only the outer sentence is
// portable; the inner text is hard-coded here because Go's runtime cannot
// produce it (DIVERGENCES.md 8, README SURPRISE 27).
func ParseOptions(options []string) (*model.OrderedMap[string, string], error) {
	parsed := model.NewOrderedMap[string, string]()
	if len(options) == 0 {
		return parsed, nil
	}

	for _, option := range options {
		parts := strings.Split(stripQuotes(option), "=")
		switch {
		case len(parts) < 2:
			return nil, kerrors.NewValueOptionParameter(notEnoughValues(len(parts)))
		case len(parts) > 2:
			return nil, kerrors.NewValueOptionParameter(tooManyValues)
		}
		parsed.Set(parts[0], parts[1])
	}

	return parsed, nil
}
