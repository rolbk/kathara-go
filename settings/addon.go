package settings

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// addonRegistry maps the capitalized `manager_type` to the addon's keys, in
// their `_to_dict` literal order.
var addonRegistry = map[string][]keyDesc{
	"Docker":     dockerKeys,
	"Kubernetes": kubernetesKeys,
}

// addonKeys is `SettingsAddonFactory().create_instance(class_args=(manager_type.capitalize(),))`
// (Setting.py:294).
func addonKeys(managerType string) ([]keyDesc, error) {
	keys, ok := addonRegistry[pyCapitalize(managerType)]
	if !ok {
		return nil, kerrors.ErrSettingsManagerType
	}
	return keys, nil
}

// pyCapitalize is `str.capitalize()`: the first character title-cased, every
// other character lower-cased. Go has no equivalent — [strings.Title] is
// per-word and deprecated, and [strings.ToTitle] upper-cases everything.
func pyCapitalize(s string) string {
	if s == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(first)) + strings.ToLower(s[size:])
}
