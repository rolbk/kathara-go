package labfile

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testdata/labfile_oracle.json was produced BY CPython and Kathará 3.8.3, not
// by this port:
//	/root/kathara/pyvenv/bin/python tools/vectorcheck/labfile_probe.py \
//	    > labfile/testdata/labfile_oracle.json
// The 142 vectors of `testdata/vectors` pin the parsers' observable behaviour;
// this file is the differential underneath them, for the five primitives the
// port had to rewrite rather than translate:
//   - the lab.conf device-line pattern, whose `\3` backreference RE2 cannot
//     express, over a generated line corpus — [matchDeviceLine] must agree on
//     whether each line matches AND on all three captures;
//   - `^\w+$` over collision-domain names, which is Unicode-aware where RE2's
//     `\w` is not;
//   - the lab.dep line pattern, same reason;
//   - `bytes.decode('utf-8')`, whose failure position, span and reason this implementation
//     reproduces by hand;
//   - `depgen.has_loop` and `depgen.flatten`, whose within-level order is
//     lab.dep line order rather than a canonical topological one.
// Python is the reference implementation for these compatibility checks;
// investigate any difference.

type oracleDocument struct {
	DeviceLines []oracleDeviceLine `json:"device_lines"`
	CDNames     []oracleCDName     `json:"cd_names"`
	DepLines    []oracleDepLine    `json:"dep_lines"`
	UTF8        []oracleUTF8       `json:"utf8"`
	DepGen      []oracleDepGen     `json:"depgen"`
}

type oracleDeviceLine struct {
	Line    string `json:"line"`
	Matched bool   `json:"matched"`
	Key     string `json:"key"`
	Arg     string `json:"arg"`
	Value   string `json:"value"`
}

type oracleCDName struct {
	Name    string `json:"name"`
	Matched bool   `json:"matched"`
}

type oracleDepLine struct {
	Line    string   `json:"line"`
	Matched bool     `json:"matched"`
	Key     string   `json:"key"`
	Deps    []string `json:"deps"`
}

type oracleUTF8 struct {
	Bytes   string `json:"bytes"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	Reason  string `json:"reason"`
}

type oracleDepGen struct {
	Graph   []oracleDepEdge `json:"graph"`
	HasLoop bool            `json:"has_loop"`
	Flatten []string        `json:"flatten"`
}

type oracleDepEdge struct {
	Key  string   `json:"key"`
	Deps []string `json:"deps"`
}

func loadOracle(t *testing.T) oracleDocument {
	t.Helper()

	content, err := os.ReadFile("testdata/labfile_oracle.json")
	if err != nil {
		t.Fatalf("reading the oracle fixture: %v", err)
	}
	var document oracleDocument
	if err := json.Unmarshal(content, &document); err != nil {
		t.Fatalf("decoding the oracle fixture: %v", err)
	}
	return document
}

// TestDeviceLineAgainstOracle is the differential for the hand-rolled
// backreference matcher. A disagreement here is a lab.conf that parses
// differently in the two implementations and therefore needs investigation.
func TestDeviceLineAgainstOracle(t *testing.T) {
	cases := loadOracle(t).DeviceLines
	if len(cases) == 0 {
		t.Fatal("the oracle fixture carries no device lines")
	}

	for _, want := range cases {
		device, matched := matchDeviceLine(pyStrip(want.Line))
		if matched != want.Matched {
			t.Errorf("matchDeviceLine(%q) matched = %v, oracle says %v",
				want.Line, matched, want.Matched)
			continue
		}
		if !matched {
			continue
		}
		if device.key != want.Key || device.arg != want.Arg || device.value != want.Value {
			t.Errorf("matchDeviceLine(%q) = (%q, %q, %q), oracle says (%q, %q, %q)",
				want.Line, device.key, device.arg, device.value,
				want.Key, want.Arg, want.Value)
		}
	}
}

func TestCollisionDomainNameAgainstOracle(t *testing.T) {
	cases := loadOracle(t).CDNames
	if len(cases) == 0 {
		t.Fatal("the oracle fixture carries no collision-domain names")
	}

	for _, want := range cases {
		if got := isCollisionDomainName(want.Name); got != want.Matched {
			t.Errorf("isCollisionDomainName(%q) = %v, oracle says %v",
				want.Name, got, want.Matched)
		}
	}
}

func TestDepLineAgainstOracle(t *testing.T) {
	cases := loadOracle(t).DepLines
	if len(cases) == 0 {
		t.Fatal("the oracle fixture carries no lab.dep lines")
	}

	for _, want := range cases {
		matches := depLineRegex.FindStringSubmatch(pyStrip(want.Line))
		if (matches != nil) != want.Matched {
			t.Errorf("depLineRegex on %q matched = %v, oracle says %v",
				want.Line, matches != nil, want.Matched)
			continue
		}
		if matches == nil {
			continue
		}

		key := pyStrip(matches[1])
		var deps []string
		for _, dep := range strings.Split(matches[2], " ") {
			deps = append(deps, pyStrip(dep))
		}
		if key != want.Key || !slices.Equal(deps, want.Deps) {
			t.Errorf("depLineRegex on %q = (%q, %v), oracle says (%q, %v)",
				want.Line, key, deps, want.Key, want.Deps)
		}
	}
}

// TestDecodeUTF8AgainstOracle sweeps the decoder over every one-byte string,
// every two-byte start and a systematic sample of the three- and four-byte
// space. The span a failure reports is not derivable from "the first bad byte"
// — a truncated sequence runs to the end of the input and a bad third byte is
// reported as a two-byte failure — so the message is compared in full.
func TestDecodeUTF8AgainstOracle(t *testing.T) {
	cases := loadOracle(t).UTF8
	if len(cases) == 0 {
		t.Fatal("the oracle fixture carries no byte strings")
	}

	for _, want := range cases {
		raw, err := hex.DecodeString(want.Bytes)
		if err != nil {
			t.Fatalf("decoding fixture bytes %q: %v", want.Bytes, err)
		}

		decoded, err := decodeUTF8(raw)
		if want.OK {
			if err != nil {
				t.Errorf("decodeUTF8(%s) failed with %v, oracle decoded it", want.Bytes, err)
			} else if decoded != string(raw) {
				t.Errorf("decodeUTF8(%s) = %q, want %q", want.Bytes, decoded, string(raw))
			}
			continue
		}

		var failure *UnicodeDecodeError
		if !errors.As(err, &failure) {
			t.Errorf("decodeUTF8(%s) = %v, oracle says UnicodeDecodeError", want.Bytes, err)
			continue
		}
		if failure.Error() != want.Message {
			t.Errorf("decodeUTF8(%s) message = %q, oracle says %q",
				want.Bytes, failure.Error(), want.Message)
		}
		if failure.Start != want.Start || failure.End != want.End || failure.Reason != want.Reason {
			t.Errorf("decodeUTF8(%s) = (%d, %d, %q), oracle says (%d, %d, %q)",
				want.Bytes, failure.Start, failure.End, failure.Reason,
				want.Start, want.End, want.Reason)
		}
	}
}

// TestDepGenAgainstOracle sweeps HasLoop and Flatten over random small graphs.
// Flatten is only asked about acyclic ones, which is the only kind Python can
// answer for: a cycle makes `_order` recurse until RecursionError.
func TestDepGenAgainstOracle(t *testing.T) {
	cases := loadOracle(t).DepGen
	if len(cases) == 0 {
		t.Fatal("the oracle fixture carries no dependency graphs")
	}

	for _, want := range cases {
		graph := NewDepGraph()
		for _, edge := range want.Graph {
			graph.Set(edge.Key, edge.Deps)
		}

		if got := HasLoop(graph); got != want.HasLoop {
			t.Errorf("HasLoop(%v) = %v, oracle says %v", want.Graph, got, want.HasLoop)
			continue
		}
		if want.HasLoop {
			continue
		}
		if got := Flatten(graph); !reflect.DeepEqual(got, want.Flatten) {
			t.Errorf("Flatten(%v) = %v, oracle says %v", want.Graph, got, want.Flatten)
		}
	}
}
