package kubernetes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// shlexVectors is testdata/shlex.json, taken from CPython's own `shlex.split`
// and `shlex.join`.
type shlexVectors struct {
	Split []struct {
		Input  string   `json:"input"`
		Output []string `json:"output"`
	} `json:"split"`
	SplitErrors []struct {
		Input string `json:"input"`
		Error string `json:"error"`
	} `json:"split_errors"`
	Join []struct {
		Input  []string `json:"input"`
		Output string   `json:"output"`
	} `json:"join"`
}

func loadShlexVectors(t *testing.T) shlexVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "shlex.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vectors shlexVectors
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	return vectors
}

// TestShlexSplit runs CPython's own answers.
func TestShlexSplit(t *testing.T) {
	for _, vector := range loadShlexVectors(t).Split {
		t.Run(vector.Input, func(t *testing.T) {
			got, err := ShlexSplit(vector.Input)
			if err != nil {
				t.Fatalf("ShlexSplit(%q): %v", vector.Input, err)
			}
			if strings.Join(got, "\x00") != strings.Join(vector.Output, "\x00") {
				t.Errorf("ShlexSplit(%q) = %q, want %q", vector.Input, got, vector.Output)
			}
		})
	}
}

func TestShlexSplitErrors(t *testing.T) {
	for _, vector := range loadShlexVectors(t).SplitErrors {
		t.Run(vector.Input, func(t *testing.T) {
			_, err := ShlexSplit(vector.Input)
			if vector.Error == "" {
				if err != nil {
					t.Fatalf("ShlexSplit(%q) = %v, want no error", vector.Input, err)
				}
				return
			}
			if !errors.Is(err, kerrors.ErrValue) {
				t.Fatalf("error = %v, want a ValueError", err)
			}
			if !strings.Contains(err.Error(), vector.Error) {
				t.Errorf("message = %q, want it to carry %q", err.Error(), vector.Error)
			}
		})
	}
}

// TestShlexJoin is `shlex.join(argv)`, which fills
// `MachineBinaryError.binary` on this backend (`KubernetesMachine.py:917`).
func TestShlexJoin(t *testing.T) {
	for _, vector := range loadShlexVectors(t).Join {
		t.Run(strings.Join(vector.Input, "|"), func(t *testing.T) {
			if got := ShlexJoin(vector.Input); got != vector.Output {
				t.Errorf("ShlexJoin(%q) = %q, want %q", vector.Input, got, vector.Output)
			}
		})
	}
}

// TestCommandWords is
// `command = shlex.split(command) if type(command) is str else command`: the
// list form is passed through and only the string form is split.
func TestCommandWords(t *testing.T) {
	t.Run("list form is not split", func(t *testing.T) {
		got, err := commandWords(kathara.NewCommand("echo", "a b"))
		if err != nil {
			t.Fatalf("commandWords: %v", err)
		}
		if len(got) != 2 || got[1] != "a b" {
			t.Errorf("got %q, want [echo, \"a b\"]", got)
		}
	})

	t.Run("string form is split", func(t *testing.T) {
		got, err := commandWords(kathara.NewShellCommand("echo 'a b'"))
		if err != nil {
			t.Fatalf("commandWords: %v", err)
		}
		if len(got) != 2 || got[1] != "a b" {
			t.Errorf("got %q, want [echo, \"a b\"]", got)
		}
	})

	t.Run("a bad string form fails", func(t *testing.T) {
		if _, err := commandWords(kathara.NewShellCommand(`echo "unterminated`)); !errors.Is(err, kerrors.ErrValue) {
			t.Fatalf("error = %v, want a ValueError", err)
		}
	})
}
