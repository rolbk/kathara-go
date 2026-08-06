// This file replaces `foundation/setting/SettingsAddon.py` and
// `foundation/setting/SettingsAddonFactory.py` with the explicit registry
// §0.2 #7 calls for.
//
// What the Python did: `SettingsAddonFactory` built the module path
// `Kathara.setting.addon.<Manager>SettingsAddon` out of
// `manager_type.capitalize()`, imported it, and instantiated whatever class
// came back. `SettingsAddon.merge` then folded the addon's `_to_dict` into the
// base one, which is where the addon keys' position *after* the base keys in
// the saved file comes from (ORDERING.tsv:113), and `SettingsAddon.load`
// applied the file's values by attribute name.
//
// What replaces it: a map from the same capitalized name to the addon's key
// table. Two behaviours of the reflection survive because they are observable,
// and only those two — the declared order (SYNTHESIS C-5) and the case
// folding.

package settings

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// addonRegistry maps the capitalized `manager_type` to the addon's keys, in
// their `_to_dict` literal order.
//
// Keyed by the capitalized name, not the lower-case one, because that is what
// the factory looked classes up by and it is the reason the lookup tolerates
// case: `manager_type` of "DOCKER" capitalizes to "Docker", finds
// `DockerSettingsAddon`, and loads. It then fails `check()` with "Manager Type
// not allowed.", because that check compares against `AVAILABLE_MANAGERS`
// exactly. Both halves are reproduced: a case-mangled `manager_type` loads and
// saves, and is rejected at check time with the frozen message.
var addonRegistry = map[string][]keyDesc{
	"Docker":     dockerKeys,
	"Kubernetes": kubernetesKeys,
}

// addonKeys is `SettingsAddonFactory().create_instance(class_args=(manager_type.capitalize(),))`
// (Setting.py:294).
//
// Python answers an unknown manager with `ClassNotFoundError`, raised out of
// `load_from_disk` before `check()` ever runs, and `kathara.py` does not catch
// it — the run ends in a traceback. `ClassNotFoundError` has no Go
// representation on purpose (ERROR_CODES.md §1.1: the registry removed every
// site that could raise it), so the answer here is the settings error the same
// value produces one call later in Python, with the same frozen text.
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
//
// It is spelled out rather than replaced by a case-insensitive comparison
// because the two are not the same function outside ASCII, and the input is a
// value from a file that anybody can edit.
func pyCapitalize(s string) string {
	if s == "" {
		return ""
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToTitle(first)) + strings.ToLower(s[size:])
}
