// This file is the schema itself: the ordered table of keys that
// `Setting._to_dict` and the two addons' `_to_dict` spell as dict literals,
// plus the read/write/serialize machinery hung off it.
//
// It is one table rather than three because the key order *is* the file format
// (ORDERING.tsv:111, :113) and a second list would be a second place to get it
// wrong. Everything that needs to walk the schema — the loader, the writer,
// `kathara config list`, the settings form — walks this.

package settings

import (
	"encoding/json"
	"math"
	"strconv"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// Kind is the JSON type a key carries in `kathara.conf`. It is what `kathara
// config set` needs in order to turn a command-line word into a schema value,
// and what the settings form needs in order to pick a widget.
type Kind int

const (
	// KindString is a JSON string.
	KindString Kind = iota
	// KindBool is a JSON boolean.
	KindBool
	// KindFloat is a JSON number read as a float (`last_checked`).
	KindFloat
	// KindInt is a JSON number read as an integer (`shared_cds`).
	KindInt
	// KindNullableString is a JSON string or `null`.
	KindNullableString
)

// label is the phrase the type-mismatch message uses. The messages are
// port-new — Python has none, because it assigns whatever the file holds to
// the attribute and lets the wrong type surface later — but they render
// through the frozen `SettingsError` wrapper.
func (k Kind) label() string {
	switch k {
	case KindBool:
		return "a boolean"
	case KindFloat:
		return "a number"
	case KindInt:
		return "an integer"
	case KindNullableString:
		return "a string or null"
	default:
		return "a string"
	}
}

// keyDesc is one row of the schema.
//
// ptr hands out a pointer to the field in a given [Settings], which is what
// lets one table serve reading, writing, resetting and copying without a
// generated switch per operation. Its dynamic type is the discriminator
// everywhere below, and [Kind] must agree with it — TestKeyKindsMatchFields
// pins that.
//
// validate is the restriction the settings screen enforced on the value, or
// nil for a key it let the user type freely. It runs on [Settings.Set] and
// [Settings.SetString], and deliberately *not* on load: Python validates on
// input and at `check()` time, never when reading the file, and a file that
// fails validation has to stay loadable so that `check()` is the thing that
// reports it.
type keyDesc struct {
	name     string
	kind     Kind
	ptr      func(*Settings) any
	validate func(*Settings, any) error
}

// baseKeys is `Setting._to_dict` (Setting.py:298), in its literal order. The
// order is the file's key order for the first twelve entries and is frozen by
// §0.4.
var baseKeys = []keyDesc{
	{name: "image", kind: KindString, ptr: func(s *Settings) any { return &s.Image }},
	{name: "manager_type", kind: KindString, ptr: func(s *Settings) any { return &s.ManagerType }, validate: validateManagerTypeValue},
	{name: "terminal", kind: KindString, ptr: func(s *Settings) any { return &s.Terminal }, validate: validateTerminalValue},
	{name: "open_terminals", kind: KindBool, ptr: func(s *Settings) any { return &s.OpenTerminals }},
	{name: "device_shell", kind: KindString, ptr: func(s *Settings) any { return &s.DeviceShell }},
	{name: "net_prefix", kind: KindString, ptr: func(s *Settings) any { return &s.NetPrefix }, validate: validateNetPrefixValue},
	{name: "device_prefix", kind: KindString, ptr: func(s *Settings) any { return &s.DevicePrefix }, validate: validateDevicePrefixValue},
	{name: "debug_level", kind: KindString, ptr: func(s *Settings) any { return &s.DebugLevel }, validate: validateDebugLevelValue},
	{name: "print_startup_log", kind: KindBool, ptr: func(s *Settings) any { return &s.PrintStartupLog }},
	{name: "enable_ipv6", kind: KindBool, ptr: func(s *Settings) any { return &s.EnableIPv6 }},
	{name: "volume_mount_policy", kind: KindString, ptr: func(s *Settings) any { return &s.VolumeMountPolicy }, validate: validateVolumeMountPolicyValue},
	{name: "last_checked", kind: KindFloat, ptr: func(s *Settings) any { return &s.LastChecked }},
}

// decode applies one raw JSON value from the file to the receiver.
//
// Python has no type checking here at all: `setattr` stores whatever
// `json.load` produced, so `"open_terminals": "yes"` loads, saves back as the
// string `"yes"`, and is truthy everywhere it is read. A typed struct cannot
// express that, and silently coercing would be worse than refusing, so a
// mismatch is reported as an invalid settings file. DIVERGENCES.md records the
// difference.
func (d keyDesc) decode(s *Settings, raw json.RawMessage) error {
	fail := func() error {
		return kerrors.NewSettingsInvalid("Setting `" + d.name + "` must be " + d.kind.label() + ".")
	}

	switch p := d.ptr(s).(type) {
	case *string:
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail()
		}
		*p = v
	case *bool:
		var v bool
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail()
		}
		*p = v
	case *float64:
		var v float64
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail()
		}
		*p = v
	case *SharedCollisionDomains:
		var v int
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail()
		}
		*p = SharedCollisionDomains(v)
	case **string:
		if string(raw) == "null" {
			*p = nil
			return nil
		}
		var v string
		if err := json.Unmarshal(raw, &v); err != nil {
			return fail()
		}
		*p = &v
	}

	return nil
}

// appendValue appends the key's value in the frozen serialization: CPython's
// `json.dumps` for the type, not Go's.
func (d keyDesc) appendValue(dst []byte, s *Settings) []byte {
	switch p := d.ptr(s).(type) {
	case *string:
		return appendPyJSONString(dst, *p)
	case *bool:
		if *p {
			return append(dst, "true"...)
		}
		return append(dst, "false"...)
	case *float64:
		return append(dst, pyFloatRepr(*p)...)
	case *SharedCollisionDomains:
		return append(dst, strconv.Itoa(int(*p))...)
	case **string:
		if *p == nil {
			return append(dst, "null"...)
		}
		return appendPyJSONString(dst, **p)
	}
	return dst
}

// get returns the value as the schema type: a string, a bool, a float64, a
// [SharedCollisionDomains], or nil for a nullable key holding `null`. The
// untyped nil is deliberate — it is what marshals back to `null` in the
// `kathara config get` envelope without the caller unwrapping a pointer.
func (d keyDesc) get(s *Settings) any {
	switch p := d.ptr(s).(type) {
	case *string:
		return *p
	case *bool:
		return *p
	case *float64:
		return *p
	case *SharedCollisionDomains:
		return *p
	case **string:
		if *p == nil {
			return nil
		}
		return **p
	}
	return nil
}

// set assigns an already-typed value, after running the key's restriction.
func (d keyDesc) set(s *Settings, value any) error {
	typed := func() error {
		return kerrors.NewSettingsInvalid("Setting `" + d.name + "` must be " + d.kind.label() + ".")
	}

	switch p := d.ptr(s).(type) {
	case *string:
		v, ok := value.(string)
		if !ok {
			return typed()
		}
		if d.validate != nil {
			if err := d.validate(s, v); err != nil {
				return err
			}
		}
		*p = v
	case *bool:
		v, ok := value.(bool)
		if !ok {
			return typed()
		}
		*p = v
	case *float64:
		v, ok := value.(float64)
		if !ok {
			return typed()
		}
		*p = v
	case *SharedCollisionDomains:
		v, ok := toSharedCollisionDomains(value)
		if !ok {
			return typed()
		}
		if d.validate != nil {
			if err := d.validate(s, v); err != nil {
				return err
			}
		}
		*p = v
	case **string:
		v, ok := toNullableString(value)
		if !ok {
			return typed()
		}
		if d.validate != nil {
			if err := d.validate(s, v); err != nil {
				return err
			}
		}
		*p = v
	}

	return nil
}

func toSharedCollisionDomains(value any) (SharedCollisionDomains, bool) {
	switch v := value.(type) {
	case SharedCollisionDomains:
		return v, true
	case int:
		return SharedCollisionDomains(v), true
	}
	return 0, false
}

func toNullableString(value any) (*string, bool) {
	switch v := value.(type) {
	case nil:
		return nil, true
	case string:
		return &v, true
	case *string:
		return v, true
	}
	return nil, false
}

// parse turns one command-line word into a schema value, so that `kathara
// config set` and the settings form share one conversion and one set of
// restrictions (§3.2 item 4).
//
// The empty string means `null` for a nullable key: that is the shell spelling
// of the settings screen's "Reset value to default" items, which set those
// keys to None rather than to "" (`DockerOptionsHandler.py:252`,
// `KubernetesOptionsHandler.py:46,81,198`).
func (d keyDesc) parse(raw string) (any, error) {
	switch d.kind {
	case KindBool:
		v, err := util.StrToBool(raw)
		if err != nil {
			return nil, kerrors.NewSettingsInvalid("Setting `" + d.name + "` must be " + d.kind.label() + ".")
		}
		return v, nil
	case KindFloat:
		v, err := strconv.ParseFloat(raw, 64)
		// `strconv.ParseFloat` accepts "inf", "infinity" and "nan" in any
		// case, which `pyFloatRepr` would then write as CPython's `Infinity`
		// or `NaN` — a document this port cannot read back (DIVERGENCES.md:
		// Go's JSON scanner rejects all three where CPython's accepts them).
		// A validated `config set` may not brick the file it writes, so the
		// three spellings are refused here, at the one entry point that turns
		// a command-line word into a number.
		if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
			return nil, kerrors.NewSettingsInvalid("Setting `" + d.name + "` must be " + d.kind.label() + ".")
		}
		return v, nil
	case KindInt:
		v, err := strconv.Atoi(raw)
		if err != nil {
			return nil, kerrors.NewSettingsInvalid("Setting `" + d.name + "` must be " + d.kind.label() + ".")
		}
		return SharedCollisionDomains(v), nil
	case KindNullableString:
		if raw == "" {
			return nil, nil
		}
		return raw, nil
	default:
		return raw, nil
	}
}

// copy overwrites the key's field in dst with src's, which is how the
// addon-reset half of [Settings.LoadFromJSON] spells "a fresh addon object".
func (d keyDesc) copy(dst, src *Settings) {
	switch p := d.ptr(dst).(type) {
	case *string:
		*p = *(d.ptr(src).(*string))
	case *bool:
		*p = *(d.ptr(src).(*bool))
	case *float64:
		*p = *(d.ptr(src).(*float64))
	case *SharedCollisionDomains:
		*p = *(d.ptr(src).(*SharedCollisionDomains))
	case **string:
		*p = *(d.ptr(src).(**string))
	}
}

// schema is the full ordered key list for the receiver's `manager_type`: the
// twelve base keys, then the active addon's.
func (s Settings) schema() ([]keyDesc, error) {
	addon, err := addonKeys(s.ManagerType)
	if err != nil {
		return nil, err
	}
	out := make([]keyDesc, 0, len(baseKeys)+len(addon))
	out = append(out, baseKeys...)
	return append(out, addon...), nil
}

// lookup finds a key by name among the ones the current `manager_type` makes
// visible. A key belonging to the *other* backend is not found, exactly as
// `SettingsAddon.get` raises `AttributeError` for it.
func (s *Settings) lookup(name string) (keyDesc, error) {
	schema, err := s.schema()
	if err != nil {
		return keyDesc{}, err
	}
	for _, d := range schema {
		if d.name == name {
			return d, nil
		}
	}
	return keyDesc{}, kerrors.NewSettingsInvalid("Setting `" + name + "` not found.")
}

// Keys returns the keys of the active schema in file order: the twelve base
// keys, then the active addon's. It is the ordering `kathara config list` and
// the settings form both render.
func (s *Settings) Keys() ([]string, error) {
	schema, err := s.schema()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(schema))
	for _, d := range schema {
		out = append(out, d.name)
	}
	return out, nil
}

// Kind reports the JSON type of a key of the active schema.
func (s *Settings) Kind(name string) (Kind, error) {
	d, err := s.lookup(name)
	if err != nil {
		return 0, err
	}
	return d.kind, nil
}

// Get returns one key's value as its schema type — string, bool, float64,
// [SharedCollisionDomains], or an untyped nil for a nullable key holding
// `null`.
//
// An unknown key, or one belonging to the other backend, is
// [kerrors.SettingsInvalidError] (JSON_CLI_CONTRACT.md §3.12: code `Settings`).
func (s *Settings) Get(name string) (any, error) {
	d, err := s.lookup(name)
	if err != nil {
		return nil, err
	}
	return d.get(s), nil
}

// Set assigns one key from an already-typed value and runs the restriction the
// settings screen enforced on it, so that the scriptable path and the form
// cannot disagree (§3.2 item 4).
//
// Accepted dynamic types follow the key's [Kind]: string, bool, float64,
// `int`/[SharedCollisionDomains], and for a nullable key nil, string or
// *string. The receiver is unchanged when the value is rejected.
//
// Setting `manager_type` to a different backend also resets the newly selected
// addon's keys to their defaults, which is what
// `cli/ui/setting/utils.update_setting_value` does with its `reload` flag
// (`cli/ui/setting/utils.py:57-64`): the settings screen builds a fresh addon
// object on the switch, so the values the previous backend's file carried do
// not leak into the new one's.
func (s *Settings) Set(name string, value any) error {
	d, err := s.lookup(name)
	if err != nil {
		return err
	}

	previousManager := s.ManagerType
	if err := d.set(s, value); err != nil {
		return err
	}

	if d.name == "manager_type" && s.ManagerType != previousManager {
		if _, err := s.resetAddon(); err != nil {
			return err
		}
	}
	return nil
}

// SetString is [Settings.Set] for a value that arrived as a command-line word.
// The empty string clears a nullable key to `null`.
func (s *Settings) SetString(name string, value string) error {
	d, err := s.lookup(name)
	if err != nil {
		return err
	}
	typed, err := d.parse(value)
	if err != nil {
		return err
	}
	return s.Set(name, typed)
}

// Encode returns the exact bytes [Settings.Save] writes: `json.dumps(...,
// indent=True)`, which is one space of indent per level, `": "` after each
// key, `",\n"` between entries and **no trailing newline** (SYNTHESIS.md
// §1.5). Every value is spelled the way CPython spells it; see pyjson.go.
func (s *Settings) Encode() ([]byte, error) {
	schema, err := s.schema()
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 512)
	out = append(out, "{\n"...)
	for i, d := range schema {
		if i > 0 {
			out = append(out, ",\n"...)
		}
		out = append(out, ' ')
		out = appendPyJSONString(out, d.name)
		out = append(out, ": "...)
		out = d.appendValue(out, s)
	}
	return append(out, "\n}"...), nil
}

// MarshalJSON is [Settings.Encode] without the indentation: the same keys in
// the same order, compact, for the `{"settings":{…}}` envelope of `kathara
// config list` and `config reset` (JSON_CLI_CONTRACT.md §3.12).
//
// `encoding/json` re-escapes what a [json.Marshaler] returns when it embeds it,
// but only when HTML escaping is on. The envelope writer turns it off
// (`SetEscapeHTML(false)`, JSON_CLI_CONTRACT.md §1.3), so `<`, `>` and `&`
// inside a value stay literal there, as they do in the on-disk file. Nothing
// here may assume otherwise: the envelope is not the frozen artifact, the file
// is, and only the file's own escaping (`ensure_ascii`, in pyjson.go) is
// byte-compared against CPython.
//
// The receiver is a value so that both `Settings` and `*Settings` marshal
// through here. With a pointer receiver, `json.Marshal(settings)` on a
// non-addressable value would silently fall back to field-by-field encoding
// and emit Go field names in declaration order.
func (s Settings) MarshalJSON() ([]byte, error) {
	schema, err := s.schema()
	if err != nil {
		return nil, err
	}

	out := make([]byte, 0, 512)
	out = append(out, '{')
	for i, d := range schema {
		if i > 0 {
			out = append(out, ',')
		}
		out = appendPyJSONString(out, d.name)
		out = append(out, ':')
		out = d.appendValue(out, &s)
	}
	return append(out, '}'), nil
}

// UnmarshalJSON is [Settings.LoadFromJSON], so that a document produced by
// [Settings.MarshalJSON] decodes back through the schema instead of through
// `encoding/json`'s field-name matching — which would happily accept an
// `"Image"` key, ignore `manager_type`'s effect on which addon keys are legal,
// and skip the addon reset.
//
// It carries `load_from_dict`'s semantics whole, including that the receiver
// is *overlaid*: unmarshalling into a zero [Settings] leaves every absent base
// key at its zero value and fails on the empty `manager_type`. Start from
// [Defaults] or use [Load].
func (s *Settings) UnmarshalJSON(data []byte) error {
	return s.LoadFromJSON(data)
}
