package settings

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// allKeys is every row of the schema, both addons included, for the
// invariants that are not per-manager.
func allKeys() []keyDesc {
	out := append([]keyDesc(nil), baseKeys...)
	out = append(out, dockerKeys...)
	return append(out, kubernetesKeys...)
}

// TestKeyKindsMatchFields pins the one invariant the table cannot enforce by
// construction: [Kind] and the dynamic type of `ptr` must agree, or the CLI
// parses a value into a type the setter refuses.
func TestKeyKindsMatchFields(t *testing.T) {
	s := Defaults()

	want := map[Kind]reflect.Type{
		KindString:         reflect.TypeOf((*string)(nil)),
		KindBool:           reflect.TypeOf((*bool)(nil)),
		KindFloat:          reflect.TypeOf((*float64)(nil)),
		KindInt:            reflect.TypeOf((*SharedCollisionDomains)(nil)),
		KindNullableString: reflect.TypeOf((**string)(nil)),
	}

	for _, d := range allKeys() {
		got := reflect.TypeOf(d.ptr(s))
		if got != want[d.kind] {
			t.Errorf("key %q: kind %v points at %v, want %v", d.name, d.kind, got, want[d.kind])
		}
	}
}

// TestKeyNamesAreUnique guards against a copy-paste in the tables that would
// make one key shadow another in [Settings.lookup].
func TestKeyNamesAreUnique(t *testing.T) {
	for _, addon := range [][]keyDesc{dockerKeys, kubernetesKeys} {
		seen := map[string]bool{}
		for _, d := range append(append([]keyDesc(nil), baseKeys...), addon...) {
			if seen[d.name] {
				t.Errorf("duplicate key %q", d.name)
			}
			seen[d.name] = true
		}
	}
}

// TestKeyPointersAreDistinct guards against two rows aiming at one field,
// which would make the file lose a key's value without any test noticing.
func TestKeyPointersAreDistinct(t *testing.T) {
	s := Defaults()
	seen := map[any]string{}
	for _, d := range allKeys() {
		p := d.ptr(s)
		if prev, ok := seen[p]; ok {
			t.Errorf("keys %q and %q point at the same field", prev, d.name)
		}
		seen[p] = d.name
	}
}

// TestGet returns schema types, with an untyped nil for a `null`.
func TestGet(t *testing.T) {
	s, err := Load(writeConf(t, `{"manager_type":"docker","image":"kathara/frr","last_checked":12.5,
	                              "shared_cds":2,"open_terminals":false,"remote_url":null,
	                              "cert_path":"/etc/ca.pem"}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, tc := range []struct {
		key  string
		want any
	}{
		{"image", "kathara/frr"},
		{"open_terminals", false},
		{"last_checked", 12.5},
		{"shared_cds", SharedBetweenLabs},
		{"remote_url", nil},
		{"cert_path", "/etc/ca.pem"},
	} {
		got, err := s.Get(tc.key)
		if err != nil {
			t.Fatalf("Get(%q): %v", tc.key, err)
		}
		if got != tc.want {
			t.Errorf("Get(%q) = %#v, want %#v", tc.key, got, tc.want)
		}
	}
}

// TestGetUnknownKey covers both shapes of "not a key of this configuration":
// a name nothing declares, and a name the *other* backend declares. Python's
// `SettingsAddon.get` raises AttributeError for both.
func TestGetUnknownKey(t *testing.T) {
	s := Defaults()
	for _, key := range []string{"nope", "api_token", ""} {
		_, err := s.Get(key)
		if !errors.Is(err, kerrors.ErrSettings) {
			t.Errorf("Get(%q) = %v, want a Settings error", key, err)
		}
	}

	s.ManagerType = "kubernetes"
	if _, err := s.Get("hosthome_mount"); !errors.Is(err, kerrors.ErrSettings) {
		t.Error("a docker key is visible under manager_type kubernetes")
	}
	if _, err := s.Get("api_token"); err != nil {
		t.Errorf("api_token should be visible under kubernetes: %v", err)
	}
}

// TestSetString is the `kathara config set` conversion: one word in, a schema
// value out, and the settings screen's restriction enforced on the way.
func TestSetString(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value string
		want  any
	}{
		{"image", "kathara/frr", "kathara/frr"},
		{"open_terminals", "false", false},
		{"open_terminals", "yes", true},
		{"print_startup_log", "0", false},
		{"debug_level", "DEBUG", "DEBUG"},
		{"net_prefix", "my_prefix", "my_prefix"},
		{"volume_mount_policy", "Never", "Never"},
		{"shared_cds", "3", SharedBetweenUsers},
		{"image_update_policy", "Always", "Always"},
		{"network_plugin", "kathara/katharanp", "kathara/katharanp"},
		{"remote_url", "tcp://host:2375", "tcp://host:2375"},
		{"remote_url", "", nil},
		{"last_checked", "12.5", 12.5},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			s := Defaults()
			if err := s.SetString(tc.key, tc.value); err != nil {
				t.Fatalf("SetString: %v", err)
			}
			got, err := s.Get(tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got != tc.want {
				t.Errorf("= %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestSetStringRejections(t *testing.T) {
	for _, tc := range []struct {
		key   string
		value string
	}{
		{"open_terminals", "maybe"},
		{"last_checked", "soon"},

		{"last_checked", "inf"},
		{"last_checked", "-Infinity"},
		{"last_checked", "NaN"},
		{"shared_cds", "two"},
		{"shared_cds", "7"},
		{"debug_level", "LOUD"},
		{"net_prefix", "BAD"},
		{"device_prefix", "a"},
		{"manager_type", "podman"},
		{"volume_mount_policy", "Sometimes"},
		{"image_update_policy", "Sometimes"},
		{"network_plugin", "kathara/other"},
		{"nope", "x"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			s := Defaults()
			before, _ := s.Encode()

			err := s.SetString(tc.key, tc.value)
			if err == nil {
				t.Fatal("expected a rejection")
			}
			if !errors.Is(err, kerrors.ErrSettings) {
				t.Errorf("err = %v, want a Settings error", err)
			}

			after, _ := s.Encode()
			if string(before) != string(after) {
				t.Error("the receiver changed despite the rejection")
			}
		})
	}
}

// TestSetTypedValues covers the API the settings form uses, where the value is
// already typed.
func TestSetTypedValues(t *testing.T) {
	s := Defaults()

	if err := s.Set("open_terminals", false); err != nil {
		t.Fatalf("Set bool: %v", err)
	}
	if s.OpenTerminals {
		t.Error("open_terminals unchanged")
	}
	if err := s.Set("shared_cds", SharedBetweenUsers); err != nil {
		t.Fatalf("Set enum: %v", err)
	}
	if err := s.Set("shared_cds", 2); err != nil {
		t.Fatalf("Set enum from int: %v", err)
	}
	if err := s.Set("remote_url", nil); err != nil {
		t.Fatalf("Set nil: %v", err)
	}
	if s.RemoteURL != nil {
		t.Error("remote_url is not nil")
	}
	if err := s.Set("remote_url", "tcp://x:1"); err != nil {
		t.Fatalf("Set nullable from string: %v", err)
	}
	if s.RemoteURL == nil || *s.RemoteURL != "tcp://x:1" {
		t.Error("remote_url not set")
	}

	if err := s.Set("open_terminals", "true"); err == nil {
		t.Error("a string should not be accepted for a boolean key")
	}
	if err := s.Set("image", 3); err == nil {
		t.Error("an int should not be accepted for a string key")
	}
}

// TestKindLookup is what `kathara config set` reads to decide how to parse its
// argument.
func TestKindLookup(t *testing.T) {
	s := Defaults()
	for _, tc := range []struct {
		key  string
		want Kind
	}{
		{"image", KindString},
		{"open_terminals", KindBool},
		{"last_checked", KindFloat},
		{"shared_cds", KindInt},
		{"remote_url", KindNullableString},
	} {
		got, err := s.Kind(tc.key)
		if err != nil {
			t.Fatalf("Kind(%q): %v", tc.key, err)
		}
		if got != tc.want {
			t.Errorf("Kind(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
	if _, err := s.Kind("nope"); err == nil {
		t.Error("Kind on an unknown key should fail")
	}
}

func TestMarshalJSONKeepsOrder(t *testing.T) {
	pinClock(t, 1785923124.0260758)
	s := Defaults()

	data, err := json.Marshal(struct {
		Settings *Settings `json:"settings"`
	}{s})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var probe struct {
		Settings map[string]json.RawMessage `json:"settings"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("the envelope is not valid JSON: %v (%s)", err, data)
	}

	keys, err := s.Keys()
	if err != nil {
		t.Fatalf("Keys: %v", err)
	}
	if len(probe.Settings) != len(keys) {
		t.Fatalf("envelope has %d keys, schema has %d", len(probe.Settings), len(keys))
	}

	// Order, checked on the bytes rather than on the decoded map.
	prev := -1
	for _, key := range keys {
		at := indexOf(string(data), `"`+key+`":`)
		if at < 0 {
			t.Fatalf("key %q missing from the envelope", key)
		}
		if at < prev {
			t.Errorf("key %q is out of schema order", key)
		}
		prev = at
	}

	if containsSubstring(string(data), "\n") || containsSubstring(string(data), `": `) {
		t.Error("MarshalJSON is indented; the envelope must be compact")
	}
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// TestEncodeRejectsUnknownManager: the schema is derived from `manager_type`,
// so a value with no addon has no file format at all.
func TestEncodeRejectsUnknownManager(t *testing.T) {
	s := Defaults()
	s.ManagerType = "podman"

	if _, err := s.Encode(); !errors.Is(err, kerrors.ErrSettingsManagerType) {
		t.Errorf("Encode = %v, want ErrSettingsManagerType", err)
	}
	if _, err := s.Keys(); !errors.Is(err, kerrors.ErrSettingsManagerType) {
		t.Errorf("Keys = %v, want ErrSettingsManagerType", err)
	}
	if _, err := json.Marshal(s); err == nil {
		t.Error("Marshal should fail for a manager with no addon")
	}
}

// TestSetManagerTypeReloadsAddon is the `reload` flag of
// `cli/ui/setting/utils.update_setting_value`: switching backends builds a
// fresh addon, so the previous backend's values cannot leak into the file the
// new one writes.
func TestSetManagerTypeReloadsAddon(t *testing.T) {
	s, err := Load(writeConf(t, `{"manager_type":"kubernetes","last_checked":1.0,
	                              "api_token":"secret","host_shared":false,
	                              "image_pull_policy":"Always"}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if err := s.Set("manager_type", "docker"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Set("manager_type", "kubernetes"); err != nil {
		t.Fatalf("Set back: %v", err)
	}

	if s.APIToken != nil {
		t.Errorf("api_token = %q, want nil after the addon reload", *s.APIToken)
	}
	if !s.HostShared {
		t.Error("host_shared kept the old value across the reload")
	}
	if s.ImagePullPolicy == nil || *s.ImagePullPolicy != "IfNotPresent" {
		t.Errorf("image_pull_policy = %v, want the default", s.ImagePullPolicy)
	}
}

// TestSetManagerTypeToSameValueKeepsAddon: `update_setting_value` only reloads
// when the value actually changes, so re-setting the current backend is not a
// way to reset its keys.
func TestSetManagerTypeToSameValueKeepsAddon(t *testing.T) {
	s := Defaults()
	s.HosthomeMount = true

	if err := s.Set("manager_type", "docker"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !s.HosthomeMount {
		t.Error("the addon was reloaded for an unchanged manager_type")
	}
}

// TestMarshalUnmarshalSymmetry pins that both spellings of the receiver
// marshal through the schema, and that a document decodes back through
// [Settings.LoadFromJSON] rather than through encoding/json's field-name
// matching.
func TestMarshalUnmarshalSymmetry(t *testing.T) {
	pinClock(t, 1785923124.0260758)
	s := Defaults()
	remote := "tcp://host:2375"
	s.RemoteURL = &remote

	byValue, err := json.Marshal(*s)
	if err != nil {
		t.Fatalf("Marshal value: %v", err)
	}
	byPointer, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("Marshal pointer: %v", err)
	}
	if string(byValue) != string(byPointer) {
		t.Errorf("value and pointer disagree\n value: %s\n ptr:   %s", byValue, byPointer)
	}
	if containsSubstring(string(byValue), `"Image"`) {
		t.Error("Go field names leaked into the output")
	}

	decoded := Defaults()
	if err := json.Unmarshal(byValue, decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	again, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-Marshal: %v", err)
	}
	if string(again) != string(byValue) {
		t.Errorf("round trip differs\n got: %s\nwant: %s", again, byValue)
	}

	// The schema, not the Go field names.
	if err := json.Unmarshal([]byte(`{"Image":"x"}`), decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Image == "x" {
		t.Error(`"Image" was accepted; only the schema's "image" is a key`)
	}
}
