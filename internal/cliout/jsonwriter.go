// This file is the encoder half of JSON_CLI_CONTRACT.md §1.3: compact UTF-8,
// HTML escaping off, exactly one `\n` per object, and — the part `encoding/json`
// cannot be told to do on a map — the pinned key emission order of every
// envelope in §3 to §5.
//
// A struct with tagged fields would give the order for free, but not the
// conditional presence the contract also pins (`lstart` emits `checks` OR
// `machines`+`links`; the `lab` object grows five metadata keys only when at
// least one of them is set; `errors` appears only for a batch). Encoding
// through an explicit builder makes both properties one readable line each.

package cliout

import (
	"bytes"
	"encoding/json"
	"strings"
)

// jobj builds one JSON object, in the order its keys are added.
type jobj struct {
	buf   bytes.Buffer
	count int
	err   error
}

func newObj() *jobj {
	o := &jobj{}
	o.buf.WriteByte('{')
	return o
}

func (o *jobj) key(name string) {
	if o.count > 0 {
		o.buf.WriteByte(',')
	}
	o.count++
	o.writeValue(name)
	o.buf.WriteByte(':')
}

// writeValue encodes v with `SetEscapeHTML(false)`, which is what makes `<`,
// `>` and `&` appear literally (§1.3), and strips the newline the encoder adds.
func (o *jobj) writeValue(v any) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		if o.err == nil {
			o.err = err
		}
		o.buf.WriteString("null")
		return
	}
	o.buf.WriteString(strings.TrimRight(b.String(), "\n"))
}

// Any adds a key whose value is encoded by `encoding/json`.
func (o *jobj) Any(name string, v any) *jobj {
	o.key(name)
	o.writeValue(v)
	return o
}

// Str adds a string key.
func (o *jobj) Str(name, v string) *jobj { return o.Any(name, v) }

// NullableStr adds a string-or-null key; the empty string is NOT null, which
// matters for `lab.name` (a scenario may legitimately be named "").
func (o *jobj) NullableStr(name string, v *string) *jobj {
	o.key(name)
	if v == nil {
		o.buf.WriteString("null")
		return o
	}
	o.writeValue(*v)
	return o
}

// Bool adds a boolean key.
func (o *jobj) Bool(name string, v bool) *jobj { return o.Any(name, v) }

// Int adds an integer key.
func (o *jobj) Int(name string, v int) *jobj { return o.Any(name, v) }

// Strings adds an array-of-string key. A nil slice is emitted as `[]`, never
// as `null`: every array in the contract is "empty when nothing matched"
// (§3.2, §3.4, §3.9), and a client that has to tell `null` from `[]` would be
// reading a distinction the contract does not make.
func (o *jobj) Strings(name string, v []string) *jobj {
	if v == nil {
		v = []string{}
	}
	return o.Any(name, v)
}

// Raw adds a key whose value is an already-encoded object, which is how the
// composite envelopes (`lrestart`, the error batch) nest one builder inside
// another.
func (o *jobj) Raw(name string, raw []byte) *jobj {
	o.key(name)
	o.buf.Write(raw)
	return o
}

// RawArray adds a key whose value is an array of already-encoded objects.
func (o *jobj) RawArray(name string, items [][]byte) *jobj {
	o.key(name)
	o.buf.WriteByte('[')
	for i, item := range items {
		if i > 0 {
			o.buf.WriteByte(',')
		}
		o.buf.Write(item)
	}
	o.buf.WriteByte(']')
	return o
}

// Bytes closes the object and returns it. The result carries no trailing
// newline; [Console.Emit] adds the single one §1.3 requires.
func (o *jobj) Bytes() []byte {
	out := make([]byte, 0, o.buf.Len()+1)
	out = append(out, o.buf.Bytes()...)
	out = append(out, '}')
	return out
}

// Err reports the first encoding failure, which can only come from a value the
// standard encoder refuses (an unsupported type, a NaN). No envelope builds
// one; the method exists so that a future field cannot fail silently.
func (o *jobj) Err() error { return o.err }
