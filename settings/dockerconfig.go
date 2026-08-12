// This file is the one place a `docker_config_json` *path* becomes a
// `docker_config_json` *value*.
//
// The settings screen never stored what the user typed. It validated the path
// with `DockerConfigJsonValidator` and then ran
// `store_b64_docker_json_callback` (`KubernetesOptionsHandler.py:170-172`):
//
//	base64.b64encode(json.dumps(json.load(f)).encode()).decode()
//
// So the stored value is the base64 of CPython's *re-serialization* of the
// file, not of the file's own bytes: whitespace collapses to `", "`/`": "`,
// non-ASCII becomes `\uXXXX`, `1.50` becomes `1.5`. A Kubernetes secret built
// from either spelling works, but the two are not the same string, and the
// value lands in a config file that a 3.8.3 install reads back — so the
// re-serialization is reproduced rather than approximated.
//
// It lives in `settings/` and not in `cmd/kathara` because §3.2 item 4 puts the
// conversion where both entry paths can reach it: `kathara config set
// docker_config_json <path>` and the settings form call this same function.

package settings

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// EncodeDockerConfigJSON is `store_b64_docker_json_callback` with
// `DockerConfigJsonValidator` in front of it: read the file at path, require
// that it parses as JSON, and answer the base64 of CPython's `json.dumps` of
// what was parsed.
//
// `~` is expanded the way `os.path.expanduser` expands it, because
// [DefaultDockerConfigJSONPath] is the pre-filled answer the screen offered.
//
// The two failure classes are [ValidateDockerConfigJSON]'s: `OS` for the open,
// `Value` for the parse (ERROR_CODES.md §1.2).
func EncodeDockerConfigJSON(path string) (string, error) {
	data, err := os.ReadFile(expandUser(path))
	if err != nil {
		return "", kerrors.WrapOS(err, err.Error())
	}

	encoded, err := pyJSONDumps(data)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
}

// pyJSONDumps is `json.dumps(json.loads(data))`: a decode that keeps object key
// order, then an encode with CPython's default separators `", "` and `": "` and
// its `ensure_ascii=True` string escaping.
//
// It walks tokens rather than unmarshalling into `map[string]any` because a Go
// map has no order and CPython's dict has the file's. Duplicate keys follow
// dict assignment: the last value wins and the key keeps the position of its
// *first* appearance.
func pyJSONDumps(data []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()

	out, err := appendPyJSONValue(nil, dec)
	if err != nil {
		return nil, err
	}
	// `json.loads` rejects trailing content; `json.Decoder` would accept a
	// second document, so the check is explicit.
	if _, err := dec.Token(); err != io.EOF {
		return nil, kerrors.New(kerrors.ErrValue, "Extra data")
	}
	return out, nil
}

// appendPyJSONValue appends one value read from dec.
func appendPyJSONValue(dst []byte, dec *json.Decoder) ([]byte, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, kerrors.Wrap(kerrors.ErrValue, err, err.Error())
	}

	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return appendPyJSONObject(dst, dec)
		case '[':
			return appendPyJSONArray(dst, dec)
		}
		// `}` or `]` here means the document is unbalanced, which the decoder
		// reports on the next read; answering an error keeps the walk total.
		return nil, kerrors.New(kerrors.ErrValue, "Expecting value")
	case string:
		return appendPyJSONString(dst, v), nil
	case bool:
		if v {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case nil:
		return append(dst, "null"...), nil
	case json.Number:
		return append(dst, pyNumberRepr(string(v))...), nil
	}
	return nil, kerrors.New(kerrors.ErrValue, "Expecting value")
}

// appendPyJSONObject appends the rest of an object whose `{` was consumed.
func appendPyJSONObject(dst []byte, dec *json.Decoder) ([]byte, error) {
	type entry struct {
		key   string
		value []byte
	}
	var (
		entries []entry
		index   = map[string]int{}
	)

	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, kerrors.Wrap(kerrors.ErrValue, err, err.Error())
		}
		key, ok := tok.(string)
		if !ok {
			return nil, kerrors.New(kerrors.ErrValue, "Expecting property name")
		}
		value, err := appendPyJSONValue(nil, dec)
		if err != nil {
			return nil, err
		}
		if at, seen := index[key]; seen {
			entries[at].value = value
			continue
		}
		index[key] = len(entries)
		entries = append(entries, entry{key: key, value: value})
	}
	if _, err := dec.Token(); err != nil { // the closing `}`
		return nil, kerrors.Wrap(kerrors.ErrValue, err, err.Error())
	}

	dst = append(dst, '{')
	for i, e := range entries {
		if i > 0 {
			dst = append(dst, ", "...)
		}
		dst = appendPyJSONString(dst, e.key)
		dst = append(dst, ": "...)
		dst = append(dst, e.value...)
	}
	return append(dst, '}'), nil
}

// appendPyJSONArray appends the rest of an array whose `[` was consumed.
func appendPyJSONArray(dst []byte, dec *json.Decoder) ([]byte, error) {
	dst = append(dst, '[')
	for i := 0; dec.More(); i++ {
		if i > 0 {
			dst = append(dst, ", "...)
		}
		var err error
		if dst, err = appendPyJSONValue(dst, dec); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // the closing `]`
		return nil, kerrors.Wrap(kerrors.ErrValue, err, err.Error())
	}
	return append(dst, ']'), nil
}

// pyNumberRepr is how `json.dumps` writes a number `json.loads` produced.
//
// CPython's scanner splits on the literal's shape: a literal with a `.` or an
// exponent becomes a float, anything else an `int`. A float is written through
// `repr` ([pyFloatRepr]); an int is written through `int.__repr__`, which for a
// JSON integer literal is the literal itself — JSON forbids leading zeros and a
// leading `+`, so `int(s)` round-trips every spelling except `-0`, which
// CPython normalises to `0`. Python ints are unbounded, so a literal too long
// for any Go integer type is still exactly its own repr.
func pyNumberRepr(literal string) string {
	if strings.ContainsAny(literal, ".eE") {
		var f float64
		if err := json.Unmarshal([]byte(literal), &f); err != nil {
			return literal
		}
		return pyFloatRepr(f)
	}
	if strings.TrimLeft(literal, "-0") == "" {
		return "0"
	}
	return literal
}
