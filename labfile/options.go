package labfile

import (
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// ParseOptions is `OptionParser.parse` (`parser/netkit/OptionParser.py:8`): the
// `-o/--pass` values of `lstart`, which become the scenario's global machine
// metadata.
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
