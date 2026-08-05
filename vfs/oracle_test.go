package vfs

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// The two tables in testdata/ were produced BY the oracle, not by this port:
// pysearch_matrix.json is `bool(re.search(pattern, line))` under CPython 3.13,
// and pyreadlines.json is `fs.open(path, "r").readlines()` on a pyfilesystem2
// mem:// filesystem holding those exact bytes. They pin the two emulation
// layers that the hand-written tables only sample.

func TestPySearchAgainstOracleMatrix(t *testing.T) {
	var rows []struct {
		Pattern string `json:"pattern"`
		Line    string `json:"line"`
		Match   bool   `json:"match"`
	}
	loadOracle(t, "testdata/pysearch_matrix.json", &rows)
	if len(rows) < 500 {
		t.Fatalf("oracle matrix has only %d rows", len(rows))
	}

	cache := map[string]*regexp.Regexp{}
	for _, r := range rows {
		re, ok := cache[r.Pattern]
		if !ok {
			var err error
			re, err = regexp.Compile(r.Pattern)
			if err != nil {
				t.Fatalf("pattern %q does not compile under RE2: %v", r.Pattern, err)
			}
			cache[r.Pattern] = re
		}
		if got := pySearch(re, r.Line); got != r.Match {
			t.Errorf("pySearch(%q, %q) = %v, Python re.search says %v",
				r.Pattern, r.Line, got, r.Match)
		}
	}
	t.Logf("%d pattern/line pairs agree with Python re.search", len(rows))
}

func TestSplitLinesAgainstOracleReadlines(t *testing.T) {
	var rows []struct {
		In    string   `json:"in"`
		Lines []string `json:"lines"`
	}
	loadOracle(t, "testdata/pyreadlines.json", &rows)

	for _, r := range rows {
		got := SplitLines([]byte(r.In))
		if !equalStrings(got, r.Lines) {
			t.Errorf("SplitLines(%q) = %q, pyfilesystem readlines says %q", r.In, got, r.Lines)
		}
	}
	t.Logf("%d byte strings split identically to pyfilesystem readlines", len(rows))
}

func loadOracle(t *testing.T, path string, into any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatal(err)
	}
}
