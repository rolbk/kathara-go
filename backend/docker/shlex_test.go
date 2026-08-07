package docker

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// shlexVector is one row of testdata/shlex.json, produced by running
// `shlex.split` on the 3.8.3 interpreter (`/root/kathara/pyvenv/bin/python`).
// Out is null for the rows that raised, and Err then carries `str(e)`.
type shlexVector struct {
	In  string   `json:"in"`
	Out []string `json:"out"`
	Err string   `json:"err"`
}

// TestShlexSplitMatchesCPython is the whole of [ShlexSplit]'s contract: 55
// inputs run against the oracle and compared token for token.
//
// The corpus is chosen to cover every branch of CPython's state machine and the
// four call sites' real shapes — `/bin/bash -c 'echo hi'` is the shell label,
// `cat /tmp/EOS` is the startup probe, `sh -c "cd /x && tar c ."` is the shape
// of the startup script — plus the traps a hand-written splitter falls into:
// escapes inside single quotes (literal), escapes inside double quotes (only in
// front of `"` and `\`), an empty quoted string as a real token, `#` not being
// a comment, and the two ValueErrors.
func TestShlexSplitMatchesCPython(t *testing.T) {
	raw, err := os.ReadFile("testdata/shlex.json")
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vectors []shlexVector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	if len(vectors) < 50 {
		t.Fatalf("corpus shrank to %d rows", len(vectors))
	}

	for _, vector := range vectors {
		t.Run(vector.In, func(t *testing.T) {
			got, err := ShlexSplit(vector.In)

			if vector.Out == nil {
				if err == nil {
					t.Fatalf("ShlexSplit(%q) = %q, want error %q", vector.In, got, vector.Err)
				}
				if err.Error() != vector.Err {
					t.Errorf("ShlexSplit(%q) error = %q, want %q", vector.In, err.Error(), vector.Err)
				}
				if !errors.Is(err, kerrors.ErrValue) {
					t.Errorf("ShlexSplit(%q) error does not carry ErrValue", vector.In)
				}
				return
			}

			if err != nil {
				t.Fatalf("ShlexSplit(%q) = %v, want %q", vector.In, err, vector.Out)
			}
			// Python's [] and Go's nil are the same "no words".
			if len(got) == 0 && len(vector.Out) == 0 {
				return
			}
			if !reflect.DeepEqual(got, vector.Out) {
				t.Errorf("ShlexSplit(%q) = %q, want %q", vector.In, got, vector.Out)
			}
		})
	}
}

// TestCommandWordsKeepsTheUnion pins the one thing `kathara.Command` exists for
// (`DockerMachine.py:803`): a list is passed through untouched and a string is
// split by Kathará, never by the SDK. `NewCommand("echo 'a b'")` is therefore
// ONE word with the quotes intact, and `NewShellCommand` of the same text is
// two words with the quotes gone.
func TestCommandWordsKeepsTheUnion(t *testing.T) {
	list, err := commandWords(kathara.NewCommand("echo", "'a b'"))
	if err != nil {
		t.Fatalf("list form: %v", err)
	}
	if !reflect.DeepEqual(list, []string{"echo", "'a b'"}) {
		t.Errorf("list form = %q, want the words unchanged", list)
	}

	line, err := commandWords(kathara.NewShellCommand("echo 'a b'"))
	if err != nil {
		t.Fatalf("string form: %v", err)
	}
	if !reflect.DeepEqual(line, []string{"echo", "a b"}) {
		t.Errorf("string form = %q, want [echo, \"a b\"]", line)
	}

	if _, err := commandWords(kathara.NewShellCommand("'unterminated")); !errors.Is(err, kerrors.ErrValue) {
		t.Errorf("unterminated quote error = %v, want ErrValue", err)
	}
}
