package settings_test

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// TestEncodeDockerConfigJSONMatchesCPython pins the whole of
// `store_b64_docker_json_callback`: the stored value is the base64 of
// `json.dumps(json.load(f))`, not of the file's bytes.
//
// Every `want` below is the literal output of CPython 3 for the same input,
// captured from `json.dumps(json.loads(src))` — which is where the
// `", "`/`": "` separators, the `\uXXXX` escaping of non-ASCII, the literal
// `<&>`, the `2.50` → `2.5` narrowing, the `-0` → `0` normalisation and the
// last-wins-first-position duplicate-key rule all come from.
func TestEncodeDockerConfigJSONMatchesCPython(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "a real docker config",
			src:  `{"auths": {"registry.example.com": {"auth": "dXNlcjpwYXNz"}}}`,
			want: `{"auths": {"registry.example.com": {"auth": "dXNlcjpwYXNz"}}}`,
		},
		{
			name: "whitespace collapses and 2.50 narrows",
			src:  `{ "a" : 1 , "b" : [ 1 , 2.50 , true , false , null ] }`,
			want: `{"a": 1, "b": [1, 2.5, true, false, null]}`,
		},
		{
			name: "ensure_ascii escapes above U+007E but not <&>",
			src:  `{"x": "héllo ✓", "y": "<&>"}`,
			want: `{"x": "h\u00e9llo \u2713", "y": "<&>"}`,
		},
		{
			name: "numbers follow int/float classification",
			src:  `{"n": 1e3, "m": -0, "k": 12345678901234567890123456789}`,
			want: `{"n": 1000.0, "m": 0, "k": 12345678901234567890123456789}`,
		},
		{
			name: "float repr is CPython's, not Go's %g",
			src:  `{"deep": {"a": [{"b": 0.0001}, {"c": 1e-05}]}}`,
			want: `{"deep": {"a": [{"b": 0.0001}, {"c": 1e-05}]}}`,
		},
		{
			name: "a later duplicate key wins but keeps the first position",
			src:  `{"dup": 1, "z": 2, "dup": 3}`,
			want: `{"dup": 3, "z": 2}`,
		},
		{name: "empty object", src: `{}`, want: `{}`},
		{name: "empty array", src: `[]`, want: `[]`},
		{name: "a bare scalar is a document", src: "  3.14  ", want: `3.14`},
		{name: "a bare string is a document", src: `"just a string"`, want: `"just a string"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, tc.src)

			got, err := settings.EncodeDockerConfigJSON(path)
			if err != nil {
				t.Fatalf("EncodeDockerConfigJSON: %v", err)
			}
			decoded, err := base64.StdEncoding.DecodeString(got)
			if err != nil {
				t.Fatalf("the stored value is not base64: %v", err)
			}
			if string(decoded) != tc.want {
				t.Errorf("json.dumps mismatch\n got: %s\nwant: %s", decoded, tc.want)
			}
		})
	}
}

// TestEncodeDockerConfigJSONErrors pins the two failure classes ERROR_CODES.md
// §1.2 assigns: `OS` for the open, `Value` for the parse.
func TestEncodeDockerConfigJSONErrors(t *testing.T) {
	t.Run("missing file is an OS error", func(t *testing.T) {
		_, err := settings.EncodeDockerConfigJSON(filepath.Join(t.TempDir(), "absent.json"))
		if !errors.Is(err, kerrors.ErrOS) {
			t.Fatalf("err = %v, want ErrOS", err)
		}
	})

	for _, src := range []string{"not json at all", `{"a": 1`, `{"a": 1} trailing`, `{1: 2}`} {
		t.Run("unparseable "+src, func(t *testing.T) {
			_, err := settings.EncodeDockerConfigJSON(writeTemp(t, src))
			if !errors.Is(err, kerrors.ErrValue) {
				t.Fatalf("err = %v, want ErrValue", err)
			}
		})
	}
}

// TestValidateDockerConfigJSONAgreesWithEncode is the invariant the settings
// screen relied on: it validated the path with one function and then read it
// with another, so the two must accept exactly the same files.
func TestValidateDockerConfigJSONAgreesWithEncode(t *testing.T) {
	for _, src := range []string{`{"auths": {}}`, `[1, 2]`, `bad`, `{`} {
		path := writeTemp(t, src)
		validated := settings.ValidateDockerConfigJSON(path) == nil
		_, err := settings.EncodeDockerConfigJSON(path)
		if encoded := err == nil; encoded != validated {
			t.Errorf("%q: validate = %t, encode = %t", src, validated, encoded)
		}
	}
}

func writeTemp(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
