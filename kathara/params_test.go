package kathara

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// TestLabRefRequireSingle is `check_required_single_not_none_var(lab_hash=…,
// lab_name=…, lab=…)` (`utils.py:117`), the guard that opens eleven methods in
// each backend.
func TestLabRefRequireSingle(t *testing.T) {
	t.Parallel()

	lab := model.NewLab("guard", model.DefaultDefaults())

	tests := []struct {
		name    string
		ref     LabRef
		wantMsg string
	}{
		{name: "none", ref: LabRef{}, wantMsg: "You must specify a parameter among lab_hash, lab_name, lab"},
		{name: "hash", ref: LabRef{Hash: "h"}},
		{name: "name", ref: LabRef{Name: "n"}},
		{name: "lab", ref: LabRef{Lab: lab}},
		{name: "hash and name", ref: LabRef{Hash: "h", Name: "n"}, wantMsg: "You must specify only a parameter among lab_hash, lab_name, lab"},
		{name: "hash and lab", ref: LabRef{Hash: "h", Lab: lab}, wantMsg: "You must specify only a parameter among lab_hash, lab_name, lab"},
		{name: "name and lab", ref: LabRef{Name: "n", Lab: lab}, wantMsg: "You must specify only a parameter among lab_hash, lab_name, lab"},
		{name: "all three", ref: LabRef{Hash: "h", Name: "n", Lab: lab}, wantMsg: "You must specify only a parameter among lab_hash, lab_name, lab"},

		{name: "empty strings are absent", ref: LabRef{Hash: "", Name: ""}, wantMsg: "You must specify a parameter among lab_hash, lab_name, lab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.ref.RequireSingle()
			if tt.wantMsg == "" {
				if err != nil {
					t.Fatalf("RequireSingle() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("RequireSingle() = nil, want an InvocationError")
			}
			if !errors.Is(err, kerrors.ErrInvocation) {
				t.Errorf("RequireSingle() = %v, want an InvocationError", err)
			}
			if got := err.Error(); got != tt.wantMsg {
				t.Errorf("message = %q, want %q", got, tt.wantMsg)
			}
			if got := Code(err); got != CodeInvocation {
				t.Errorf("code = %q, want %q", got, CodeInvocation)
			}
		})
	}
}

// TestLabRefAtMostOne is `check_single_not_none_var` (`utils.py:110`), the
// looser guard the plural getters use — where "none of the three" is not an
// error but "every scenario of this user".
func TestLabRefAtMostOne(t *testing.T) {
	t.Parallel()

	lab := model.NewLab("guard", model.DefaultDefaults())

	tests := []struct {
		name    string
		ref     LabRef
		wantErr bool
	}{
		{name: "none is allowed here", ref: LabRef{}},
		{name: "hash", ref: LabRef{Hash: "h"}},
		{name: "name", ref: LabRef{Name: "n"}},
		{name: "lab", ref: LabRef{Lab: lab}},
		{name: "two", ref: LabRef{Hash: "h", Lab: lab}, wantErr: true},
		{name: "three", ref: LabRef{Hash: "h", Name: "n", Lab: lab}, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.ref.AtMostOne()
			if tt.wantErr {
				if err == nil {
					t.Fatal("AtMostOne() = nil, want an InvocationError")
				}
				if got, want := err.Error(), "You must specify only a parameter among lab_hash, lab_name, lab"; got != want {
					t.Errorf("message = %q, want %q", got, want)
				}
				return
			}
			if err != nil {
				t.Errorf("AtMostOne() = %v, want nil", err)
			}
		})
	}
}

// TestLabRefMessagesMatchUtil is the drift guard named in
// [LabRef.RequireSingle]'s doc comment.
func TestLabRefMessagesMatchUtil(t *testing.T) {
	t.Parallel()

	lab := model.NewLab("drift", model.DefaultDefaults())
	refs := []LabRef{
		{},
		{Hash: "h"},
		{Name: "n"},
		{Lab: lab},
		{Hash: "h", Name: "n"},
		{Hash: "h", Lab: lab},
		{Name: "n", Lab: lab},
		{Hash: "h", Name: "n", Lab: lab},
	}

	for _, ref := range refs {
		params := []util.Param{
			{Name: "lab_hash", Present: ref.Hash != ""},
			{Name: "lab_name", Present: ref.Name != ""},
			{Name: "lab", Present: ref.Lab != nil},
		}

		if got, want := errText(ref.RequireSingle()), errText(util.CheckRequiredSingleNotNoneVar(params...)); got != want {
			t.Errorf("RequireSingle(%+v) = %q, util says %q", ref, got, want)
		}
		if got, want := errText(ref.AtMostOne()), errText(util.CheckSingleNotNoneVar(params...)); got != want {
			t.Errorf("AtMostOne(%+v) = %q, util says %q", ref, got, want)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestNameSet(t *testing.T) {
	t.Parallel()

	t.Run("NewNameSet is never nil", func(t *testing.T) {
		t.Parallel()

		empty := NewNameSet()
		if empty == nil {
			t.Fatal("NewNameSet() = nil; the empty set must stay distinguishable from no filter")
		}
		if len(empty) != 0 {
			t.Errorf("NewNameSet() has %d members, want 0", len(empty))
		}
	})

	t.Run("membership", func(t *testing.T) {
		t.Parallel()

		set := NewNameSet("pc1", "pc2", "pc1")
		if len(set) != 2 {
			t.Errorf("len = %d, want 2 (duplicates collapse, as a Python set does)", len(set))
		}
		if !set.Has("pc1") || !set.Has("pc2") {
			t.Error("Has missed a member")
		}
		if set.Has("pc3") {
			t.Error("Has found a non-member")
		}
	})

	t.Run("Has is safe on nil", func(t *testing.T) {
		t.Parallel()

		var none NameSet
		if none.Has("pc1") {
			t.Error("a nil NameSet has members")
		}
		if got := none.Names(); len(got) != 0 {
			t.Errorf("a nil NameSet enumerated %q", got)
		}
	})

	t.Run("Names is sorted and copied", func(t *testing.T) {
		t.Parallel()

		set := NewNameSet("pc3", "pc1", "pc2")
		if got := set.Names(); !slices.Equal(got, []string{"pc1", "pc2", "pc3"}) {
			t.Errorf("Names() = %q, want sorted", got)
		}

		names := set.Names()
		slices.Reverse(names)
		if got := set.Names(); !slices.Equal(got, []string{"pc1", "pc2", "pc3"}) {
			t.Errorf("Names() = %q after a caller reversed its copy", got)
		}
	})
}

func TestWaitPolicy(t *testing.T) {
	t.Parallel()

	t.Run("zero value is wait=False", func(t *testing.T) {
		t.Parallel()

		var w WaitPolicy
		if w != NoWait() {
			t.Errorf("zero WaitPolicy = %+v, want NoWait()", w)
		}
		if w.Enabled {
			t.Error("the zero value waits; exec's default is not to")
		}
	})

	t.Run("WaitForever is wait=True", func(t *testing.T) {
		t.Parallel()

		w := WaitForever()
		if !w.Enabled {
			t.Error("WaitForever does not wait")
		}
		if w.Retries != nil {
			t.Errorf("Retries = %v, want nil (Python's n_retries = None)", *w.Retries)
		}
		// Python's bool arm hardcodes `retry_interval = 1`.
		if w.Interval != time.Second {
			t.Errorf("Interval = %v, want 1s", w.Interval)
		}
	})

	t.Run("WaitRetries is the tuple", func(t *testing.T) {
		t.Parallel()

		w := WaitRetries(5, 250*time.Millisecond)
		if !w.Enabled {
			t.Error("WaitRetries does not wait")
		}
		if w.Retries == nil || *w.Retries != 5 {
			t.Errorf("Retries = %v, want 5", w.Retries)
		}
		if w.Interval != 250*time.Millisecond {
			t.Errorf("Interval = %v, want 250ms", w.Interval)
		}
		// `(0, interval)` is a tuple Python accepts.
		if zero := WaitRetries(0, time.Second); zero.Retries == nil || *zero.Retries != 0 {
			t.Errorf("WaitRetries(0, …).Retries = %v, want 0", zero.Retries)
		}
	})

	t.Run("connect_tty defaults to waiting forever", func(t *testing.T) {
		t.Parallel()

		if got := DefaultConnectTTYOptions(); got.Wait != WaitForever() {
			t.Errorf("DefaultConnectTTYOptions().Wait = %+v, want WaitForever()", got.Wait)
		}
		// The other three fields are the Python signature defaults.
		got := DefaultConnectTTYOptions()
		if got.Shell != "" || got.Logs || got.LogWriter != nil {
			t.Errorf("DefaultConnectTTYOptions() = %+v, want the bare signature defaults", got)
		}
	})
}

// TestCommand is the `Union[List[str], str]` of `exec`. The two shapes are not
// interchangeable: the string one is word-split by the backend, with shlex
// semantics (`DockerMachine.py:803`), before it leaves the host — which is how
// `kathara exec pc1 "ls -la"` — a one-element argv that `ExecCommand.py:96`
// pops to a bare string — comes to run two words.
func TestCommand(t *testing.T) {
	t.Parallel()

	t.Run("argv shape", func(t *testing.T) {
		t.Parallel()

		c := NewCommand("ls", "-la")
		argv, ok := c.Argv()
		if !ok {
			t.Fatal("Argv() reported the wrong shape")
		}
		if len(argv) != 2 || argv[0] != "ls" || argv[1] != "-la" {
			t.Errorf("Argv() = %q, want [ls -la]", argv)
		}
		if _, ok := c.Line(); ok {
			t.Error("Line() claimed an argv command was a shell line")
		}
	})

	t.Run("shell-line shape", func(t *testing.T) {
		t.Parallel()

		c := NewShellCommand("ls -la")
		line, ok := c.Line()
		if !ok {
			t.Fatal("Line() reported the wrong shape")
		}
		if line != "ls -la" {
			t.Errorf("Line() = %q, want %q", line, "ls -la")
		}
		if _, ok := c.Argv(); ok {
			t.Error("Argv() claimed a shell line was an argv command")
		}
	})

	t.Run("the two shapes are distinguishable for the same text", func(t *testing.T) {
		t.Parallel()

		// This is the whole reason the union survives: one word containing a
		// space is one argument in the list shape and two in the string shape.
		argv := NewCommand("echo hello world")
		line := NewShellCommand("echo hello world")
		if _, ok := argv.Line(); ok {
			t.Error("the argv shape answered as a shell line")
		}
		if _, ok := line.Argv(); ok {
			t.Error("the shell-line shape answered as argv")
		}
	})

	t.Run("zero value is the empty argv", func(t *testing.T) {
		t.Parallel()

		var c Command
		argv, ok := c.Argv()
		if !ok || len(argv) != 0 {
			t.Errorf("zero Command = %q, %v; want the empty argv list", argv, ok)
		}
	})
}
