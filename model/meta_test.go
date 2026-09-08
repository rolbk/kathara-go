package model

import (
	"errors"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// TestAddMetaSysctl is EXPECTATIONS-core.md §1 "add_meta — sysctl (8)" plus the
// int-coercion corners the oracle turned up.
func TestAddMetaSysctl(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		key     string
		want    Scalar
		wantErr bool
		// wantValueErr is Python's bare, uncaught ValueError — a different
		// class from the MachineOptionError the other failures raise.
		wantValueErr string
	}{
		{name: "numeric", in: "net.ipv4.tcp_syncookies=1", key: "net.ipv4.tcp_syncookies", want: Int(1)},
		{name: "text", in: "net.test_sysctl.text=test", key: "net.test_sysctl.text", want: Str("test")},
		{name: "negative", in: "net.test_sysctl.negative=-1", key: "net.test_sysctl.negative", want: Int(-1)},
		// `-1-`.lstrip('-') is `1-`, which is not numeric, so it stays a string.
		{name: "trailing dash", in: "net.test_sysctl.negative=-1-", key: "net.test_sysctl.negative", want: Str("-1-")},
		{
			name: "spaces are part of the value",
			in:   "net.ipv4.tcp_rmem=4096 87380 33554432",
			key:  "net.ipv4.tcp_rmem", want: Str("4096 87380 33554432"),
		},
		// The value is stripped, and then found numeric.
		{name: "padded numeric", in: "net.a.b=  5  ", key: "net.a.b", want: Int(5)},
		{name: "leading zeros", in: "net.a.b=007", key: "net.a.b", want: Int(7)},
		// Unicode decimal digits parse; `+5` is not isnumeric, so it stays text.
		{name: "arabic-indic digit", in: "net.a.b=٣", key: "net.a.b", want: Int(3)},
		{name: "explicit plus", in: "net.a.b=+5", key: "net.a.b", want: Str("+5")},
		{name: "pep515 underscore", in: "net.a.b=1_0", key: "net.a.b", want: Str("1_0")},
		{name: "empty value", in: "net.a.b=", key: "net.a.b", want: Str("")},
		// Python's `$` matches before a trailing newline (oracle Q1).
		{name: "trailing newline", in: "net.a.b=1\n", key: "net.a.b", want: Int(1)},
		// The sysctl value class is `[^=]*`, which DOES match a newline —
		// unlike env's `.*`. Oracle: `net.a.b=1\nx` stores the string "1\nx".
		{name: "embedded newline is part of the value", in: "net.a.b=1\nx", key: "net.a.b", want: Str("1\nx")},
		{name: "unicode key", in: "net.ключ.b=1", key: "net.ключ.b", want: Int(1)},
		{name: "dashed key label", in: "net.a-b.c=1", key: "net.a-b.c", want: Int(1)},

		{name: "not net namespace", in: "kernel.shm_rmid_forced=1", wantErr: true},
		{name: "no equals", in: "kernel.shm_rmid_forced", wantErr: true},
		{name: "second equals", in: "net.test_sysctl.text=test=again", wantErr: true},
		// net. plus only one label is refused (vector labconf/sysctl_shallow_key).
		{name: "shallow key", in: "net.foo=1", wantErr: true},
		{name: "space before equals", in: "net.a.b =5", wantErr: true},

		// DIVERGENCES: isnumeric accepts these and int() rejects them, so
		// Python dies with an uncaught ValueError (vector
		// labconf/sysctl_double_dash_crash).
		{name: "double dash", in: "net.a.b=--5", wantValueErr: `invalid literal for int() with base 10: '--5'`},
		{name: "superscript", in: "net.a.b=²", wantValueErr: `invalid literal for int() with base 10: '²'`},
		{name: "vulgar fraction", in: "net.a.b=½", wantValueErr: `invalid literal for int() with base 10: '½'`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, machine := newTestMachine(t)

			_, _, err := machine.AddMeta("sysctl", tt.in)
			switch {
			case tt.wantValueErr != "":
				if !errors.Is(err, kerrors.ErrValue) {
					t.Fatalf("error = %v, want ErrValue", err)
				}
				if err.Error() != tt.wantValueErr {
					t.Errorf("message = %q, want %q", err.Error(), tt.wantValueErr)
				}
			case tt.wantErr:
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Fatalf("error = %v, want ErrMachineOption", err)
				}
				want := "Invalid sysctl value (`" + tt.in + "`) on `test_machine`, missing `=` or value not in `net.` namespace."
				if err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
			default:
				if err != nil {
					t.Fatalf("AddMeta: %v", err)
				}
				got, ok := machine.Meta.Sysctls.Get(tt.key)
				if !ok {
					t.Fatalf("sysctl %q not stored; map = %v", tt.key, machine.Meta.Sysctls.Entries())
				}
				if got.Kind() != tt.want.Kind() || got.Value() != tt.want.Value() {
					t.Errorf("sysctl %q = %v (%v), want %v (%v)",
						tt.key, got.Value(), got.Kind(), tt.want.Value(), tt.want.Kind())
				}
			}
		})
	}
}

// TestAddMetaEnv is EXPECTATIONS-core.md §1 "add_meta — env (5)".
func TestAddMetaEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		key     string
		want    string
		wantErr bool
	}{
		{name: "plain", in: "MY_ENV_VAR=test", key: "MY_ENV_VAR", want: "test"},
		// No int coercion here, unlike sysctl.
		{name: "number stays a string", in: "MY_ENV_VAR=1", key: "MY_ENV_VAR", want: "1"},
		{name: "spaces", in: "MY_ENV_VAR=spaced value", key: "MY_ENV_VAR", want: "spaced value"},
		// The value class is `.*`, so a second `=` is part of the value.
		{name: "complex", in: "IFACES=linux:eth0,name=iface/name", key: "IFACES", want: "linux:eth0,name=iface/name"},
		{name: "empty value", in: "K=", key: "K", want: ""},
		{name: "value is stripped", in: "K=  v  ", key: "K", want: "v"},
		{name: "trailing newline", in: "K=v\n", key: "K", want: "v"},
		{name: "unicode key", in: "ключ=1", key: "ключ", want: "1"},

		{name: "no equals", in: "MY_ENV_VAR", wantErr: true},
		{name: "empty key", in: "=1", wantErr: true},
		{name: "space in key", in: "a b=1", wantErr: true},
		{name: "embedded newline", in: "K=v\nx", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, machine := newTestMachine(t)

			_, _, err := machine.AddMeta("env", tt.in)
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Fatalf("error = %v, want ErrMachineOption", err)
				}
				want := "Invalid env value (`" + tt.in + "`) on `test_machine`."
				if err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			if got, ok := machine.Meta.Envs.Get(tt.key); !ok || got != tt.want {
				t.Errorf("env %q = %q, %v; want %q", tt.key, got, ok, tt.want)
			}
		})
	}
}

// TestAddMetaPort is EXPECTATIONS-core.md §1 "add_meta — port (6)" plus the two
// uncaught unpack crashes (vectors labconf/port_two_slashes, port_two_colons).
func TestAddMetaPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		in        string
		wantKey   PortKey
		wantGuest int
		wantMsg   string
		wantValue string
	}{
		{name: "guest only", in: "8080", wantKey: PortKey{3000, "tcp"}, wantGuest: 8080},
		{name: "guest and protocol", in: "8080/udp", wantKey: PortKey{3000, "udp"}, wantGuest: 8080},
		{name: "host guest protocol", in: "2000:8080/udp", wantKey: PortKey{2000, "udp"}, wantGuest: 8080},
		{name: "protocol is lower-cased", in: "80/TCP", wantKey: PortKey{3000, "tcp"}, wantGuest: 80},
		{name: "zero guest", in: "0", wantKey: PortKey{3000, "tcp"}, wantGuest: 0},
		{name: "int() accepts a plus", in: "+1", wantKey: PortKey{3000, "tcp"}, wantGuest: 1},
		{name: "unicode digit", in: "٣", wantKey: PortKey{3000, "tcp"}, wantGuest: 3},
		{name: "pep515 underscore", in: "1_0:2", wantKey: PortKey{10, "tcp"}, wantGuest: 2},
		{name: "trailing newline", in: "8080\n", wantKey: PortKey{3000, "tcp"}, wantGuest: 8080},

		{name: "unknown protocol", in: "8080/ppp", wantMsg: "Port protocol value not valid on `test_machine`."},
		{name: "empty protocol", in: "8080/", wantMsg: "Port protocol value not valid on `test_machine`."},
		{name: "empty host", in: ":2000", wantMsg: "Port value not valid on `test_machine`."},
		{name: "empty ports", in: "/tcp", wantMsg: "Port value not valid on `test_machine`."},
		{name: "non numeric", in: "abc", wantMsg: "Port value not valid on `test_machine`."},

		{name: "two colons", in: "1:2:3", wantValue: "too many values to unpack (expected 2)"},
		{name: "two slashes", in: "80/tcp/x", wantValue: "too many values to unpack (expected 2)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, machine := newTestMachine(t)

			_, _, err := machine.AddMeta("port", tt.in)
			switch {
			case tt.wantValue != "":
				if !errors.Is(err, kerrors.ErrValue) {
					t.Fatalf("error = %v, want ErrValue", err)
				}
				if err.Error() != tt.wantValue {
					t.Errorf("message = %q, want %q", err.Error(), tt.wantValue)
				}
			case tt.wantMsg != "":
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Fatalf("error = %v, want ErrMachineOption", err)
				}
				if err.Error() != tt.wantMsg {
					t.Errorf("message = %q, want %q", err.Error(), tt.wantMsg)
				}
			default:
				if err != nil {
					t.Fatalf("AddMeta: %v", err)
				}
				got, ok := machine.Meta.Ports.Get(tt.wantKey)
				if !ok || got != tt.wantGuest {
					t.Errorf("ports[%v] = %d, %v; want %d", tt.wantKey, got, ok, tt.wantGuest)
				}
			}
		})
	}
}

// TestAddMetaUlimit is EXPECTATIONS-core.md §1 "add_meta — ulimit (10)".
//
// All three failure messages name the OPTION and not the device: they end in
// "on `ulimit`." whatever the device is called, which is DIVERGENCES.md 2, a
// Python bug ported verbatim.
func TestAddMetaUlimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		key     string
		want    Ulimit
		wantMsg string
	}{
		{name: "soft and hard", in: "nofile=1024:2048", key: "nofile", want: Ulimit{1024, 2048}},
		{name: "soft only", in: "nofile=1024", key: "nofile", want: Ulimit{1024, 1024}},
		{name: "unlimited", in: "memlock=-1", key: "memlock", want: Ulimit{-1, -1}},
		// Soft above hard is silently clamped down.
		{name: "soft clamped", in: "nofile=2048:1024", key: "nofile", want: Ulimit{1024, 1024}},
		{name: "hard unlimited", in: "nofile=2048:-1", key: "nofile", want: Ulimit{2048, -1}},
		{name: "both unlimited", in: "nofile=-1:-1", key: "nofile", want: Ulimit{-1, -1}},
		{name: "leading zeros", in: "nofile=007", key: "nofile", want: Ulimit{7, 7}},
		{name: "unicode digits", in: "n=٣:٥", key: "n", want: Ulimit{3, 5}},
		{name: "unicode key", in: "ключ=5", key: "ключ", want: Ulimit{5, 5}},
		{name: "trailing newline", in: "nofile=5\n", key: "nofile", want: Ulimit{5, 5}},

		{name: "three parts", in: "nofile=1024:2048:4096", wantMsg: "Invalid ulimit value (`nofile=1024:2048:4096`) on `ulimit`."},
		{name: "no equals", in: "nofile", wantMsg: "Invalid ulimit value (`nofile`) on `ulimit`."},
		{name: "space in key", in: "no file=1", wantMsg: "Invalid ulimit value (`no file=1`) on `ulimit`."},
		{name: "embedded newline", in: "nofile=5\nx", wantMsg: "Invalid ulimit value (`nofile=5\nx`) on `ulimit`."},
		{name: "below minus one", in: "nofile=1024:-2", wantMsg: "Invalid ulimit value (`nofile=1024:-2`) on `ulimit`. Values must be >= -1."},
		{
			name: "soft unlimited hard bounded", in: "nofile=-1:1024",
			wantMsg: "Invalid ulimit value (`nofile=-1:1024`) on `ulimit`. Soft limit (-1) cannot be greater than hard limit (1024).",
		},
		{
			// The hard limit is rendered as Python's str(int(group)), so the
			// leading zeros are gone (oracle P10).
			name: "hard limit is normalised in the message", in: "nofile=-1:007",
			wantMsg: "Invalid ulimit value (`nofile=-1:007`) on `ulimit`. Soft limit (-1) cannot be greater than hard limit (7).",
		},
		{
			// Beyond an int64, and rendered in full: kerrors takes the hard
			// limit pre-rendered exactly for this.
			name: "hard limit beyond int64", in: "nofile=-1:99999999999999999999999999",
			wantMsg: "Invalid ulimit value (`nofile=-1:99999999999999999999999999`) on `ulimit`. " +
				"Soft limit (-1) cannot be greater than hard limit (99999999999999999999999999).",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, machine := newTestMachine(t)

			_, _, err := machine.AddMeta("ulimit", tt.in)
			if tt.wantMsg != "" {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Fatalf("error = %v, want ErrMachineOption", err)
				}
				if err.Error() != tt.wantMsg {
					t.Errorf("message = %q, want %q", err.Error(), tt.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			if got, ok := machine.Meta.Ulimits.Get(tt.key); !ok || got != tt.want {
				t.Errorf("ulimits[%q] = %+v, %v; want %+v", tt.key, got, ok, tt.want)
			}
		})
	}
}

// TestAddMetaVolume is EXPECTATIONS-core.md §1 "add_meta — volume (6)". The key
// is os.path.abspath of the host path, so the test pins it against a known
// working directory.
func TestAddMetaVolume(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Volume host paths go through the platform abspath, exactly as
		// Python's os.path.abspath does — on Windows "/h" becomes "C:\h" in
		// both implementations, so the POSIX-shaped expectations cannot
		// apply. Windows runtime is out of 1.0 scope.
		t.Skip("expectations are POSIX-abspath-shaped")
	}
	dir := t.TempDir()
	t.Chdir(dir)

	tests := []struct {
		name    string
		in      string
		key     string
		want    Volume
		wantMsg string
	}{
		{name: "relative host path", in: ".|/test", key: dir, want: Volume{"/test", "ro"}},
		{name: "rw mode", in: "/h|/test|rw", key: "/h", want: Volume{"/test", "rw"}},
		{name: "ro mode", in: "/h|/test|ro", key: "/h", want: Volume{"/test", "ro"}},
		// SYNTHESIS C-6: rx is accepted by the code even though no test in
		// 3.8.3 exercises it.
		{name: "rx mode", in: "/h|/test|rx", key: "/h", want: Volume{"/test", "rx"}},
		// Empty segments are filtered out before the count is taken.
		{name: "empty middle segment", in: "a||b", key: filepath.Join(dir, "a"), want: Volume{"b", "ro"}},
		{name: "empty leading segment", in: "|/a|/b", key: "/a", want: Volume{"/b", "ro"}},
		// The guest path is NOT stripped (oracle Q1).
		{name: "guest path keeps its newline", in: "/h|/g\n", key: "/h", want: Volume{"/g\n", "ro"}},

		{
			name: "one part", in: "/h",
			wantMsg: "The volume specified `/h` is not in a valid format: <host_path>|<guest_path>|[<mode>]",
		},
		{
			name: "trailing empty segment", in: "/h|",
			wantMsg: "The volume specified `/h|` is not in a valid format: <host_path>|<guest_path>|[<mode>]",
		},
		{
			name: "four parts", in: "/h|/g|rw|extra",
			wantMsg: "The volume specified `/h|/g|rw|extra` is not in a valid format: <host_path>|<guest_path>|[<mode>]",
		},
		{
			// Note the trailing space, which ERROR_CODES.md §0.2 freezes.
			name: "invalid mode", in: "/h|/g|xx",
			wantMsg: "Invalid volume mode `xx` on `/h` mount. Allowed values are ro, rw, rx. ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, machine := newTestMachine(t)

			_, _, err := machine.AddMeta("volume", tt.in)
			if tt.wantMsg != "" {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Fatalf("error = %v, want ErrMachineOption", err)
				}
				if err.Error() != tt.wantMsg {
					t.Errorf("message = %q, want %q", err.Error(), tt.wantMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			if got, ok := machine.Meta.Volumes.Get(tt.key); !ok || got != tt.want {
				t.Errorf("volumes[%q] = %+v, %v; want %+v (map: %v)",
					tt.key, got, ok, tt.want, machine.Meta.Volumes.Entries())
			}
		})
	}
}

// TestAddMetaBool covers `privileged` and `bridged`, whose strtobool failure is
// a bare ValueError and NOT a MachineOptionError (vector
// labconf/strtobool_invalid).
func TestAddMetaBool(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"privileged", "bridged"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, tt := range []struct {
				in   string
				want bool
			}{
				{"1", true}, {"true", true}, {"TRUE", true}, {"yes", true}, {"y", true}, {"on", true}, {"t", true},
				{"0", false}, {"false", false}, {"no", false}, {"n", false}, {"off", false}, {"f", false},
			} {
				_, machine := newTestMachine(t)
				if _, _, err := machine.AddMeta(name, tt.in); err != nil {
					t.Fatalf("AddMeta(%q, %q): %v", name, tt.in, err)
				}
				field := machine.Meta.Privileged
				if name == "bridged" {
					field = machine.Meta.Bridged
				}
				if field == nil || *field != tt.want {
					t.Errorf("%s=%q stored %v, want %v", name, tt.in, field, tt.want)
				}
			}

			_, machine := newTestMachine(t)
			_, _, err := machine.AddMeta(name, "maybe")
			if !errors.Is(err, kerrors.ErrValue) {
				t.Fatalf("error = %v, want ErrValue", err)
			}
			if want := "Invalid truth value `maybe`."; err.Error() != want {
				t.Errorf("message = %q, want %q", err.Error(), want)
			}
		})
	}
}

// TestAddMetaReturnsPreviousValue is EXPECTATIONS-core.md §1 "add_meta —
// overwrite return value (4)". LabParser warns about a duplicate meta only when
// this reports one, so the presence flag — not the value — is what matters
// (NILABILITY.tsv:8). Values are the oracle's P2 run.
func TestAddMetaReturnsPreviousValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		meta      string
		first     string
		second    string
		wantFirst any
		wantPrev  any
	}{
		{name: "generic", meta: "image", first: "a", second: "b", wantPrev: "a"},
		{name: "sysctl", meta: "sysctl", first: "net.a.b=1", second: "net.a.b=2", wantPrev: int64(1)},
		{name: "env", meta: "env", first: "K=1", second: "K=2", wantPrev: "1"},
		{name: "port", meta: "port", first: "8080", second: "9090", wantPrev: 8080},
		{name: "privileged", meta: "privileged", first: "yes", second: "no", wantPrev: true},
		{name: "bridged", meta: "bridged", first: "1", second: "0", wantPrev: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, machine := newTestMachine(t)

			prev, existed, err := machine.AddMeta(tt.meta, tt.first)
			if err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			if existed || prev != nil {
				t.Errorf("first AddMeta = %v, %v; want nil, false", prev, existed)
			}

			prev, existed, err = machine.AddMeta(tt.meta, tt.second)
			if err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			if !existed {
				t.Error("second AddMeta reports no previous value")
			}
			if prev != tt.wantPrev {
				t.Errorf("previous = %#v, want %#v", prev, tt.wantPrev)
			}
		})
	}

	t.Run("ulimit", func(t *testing.T) {
		t.Parallel()
		_, machine := newTestMachine(t)

		if _, existed, _ := machine.AddMeta("ulimit", "nofile=5"); existed {
			t.Error("first ulimit reports a previous value")
		}
		prev, existed, err := machine.AddMeta("ulimit", "nofile=6")
		if err != nil || !existed {
			t.Fatalf("AddMeta = %v, %v, %v", prev, existed, err)
		}
		if prev != (Ulimit{Soft: 5, Hard: 5}) {
			t.Errorf("previous = %#v, want {5 5}", prev)
		}
	})

	t.Run("volume", func(t *testing.T) {
		t.Parallel()
		_, machine := newTestMachine(t)

		if _, existed, _ := machine.AddMeta("volume", "/h|/g"); existed {
			t.Error("first volume reports a previous value")
		}
		prev, existed, err := machine.AddMeta("volume", "/h|/g2|rw")
		if err != nil || !existed {
			t.Fatalf("AddMeta = %v, %v, %v", prev, existed, err)
		}
		if prev != (Volume{GuestPath: "/g", Mode: "ro"}) {
			t.Errorf("previous = %#v, want {/g ro}", prev)
		}
	})

	t.Run("exec never reports one", func(t *testing.T) {
		t.Parallel()
		_, machine := newTestMachine(t)

		for _, cmd := range []string{"echo one", "echo two"} {
			prev, existed, err := machine.AddMeta("exec", cmd)
			if err != nil || existed || prev != nil {
				t.Errorf("AddMeta(exec, %q) = %v, %v, %v", cmd, prev, existed, err)
			}
		}
		// Append order is boot order (ORDERING.tsv model/Machine.py:163).
		if got := machine.ExecCommands(); len(got) != 2 || got[0] != "echo one" || got[1] != "echo two" {
			t.Errorf("ExecCommands() = %v", got)
		}
	})
}

// TestAddMetaKeepsInsertionPosition pins Python's dict semantics: overwriting a
// key does not move it (ORDERING.tsv model/Machine.py:182,200,261,286).
func TestAddMetaKeepsInsertionPosition(t *testing.T) {
	t.Parallel()
	_, machine := newTestMachine(t)

	for _, v := range []string{"net.a.b=1", "net.c.d=2", "net.a.b=3"} {
		if _, _, err := machine.AddMeta("sysctl", v); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}
	}

	keys := machine.Meta.Sysctls.Keys()
	if len(keys) != 2 || keys[0] != "net.a.b" || keys[1] != "net.c.d" {
		t.Errorf("keys = %v, want [net.a.b net.c.d]", keys)
	}
	if got, _ := machine.Meta.Sysctls.Get("net.a.b"); got.Value() != int64(3) {
		t.Errorf("net.a.b = %v, want 3", got.Value())
	}
}

// TestAddMetaUnknownGoesToExtras pins the accepted ruling: an unknown meta name
// is stored, not rejected (`model/Machine.py:289`, PACKAGE_GRAPH.md §0).
func TestAddMetaUnknownGoesToExtras(t *testing.T) {
	t.Parallel()
	_, machine := newTestMachine(t)

	if _, existed, err := machine.AddMeta("zzz", "q"); err != nil || existed {
		t.Fatalf("AddMeta = %v, %v", existed, err)
	}
	got, ok := machine.Meta.Extras.Get("zzz")
	if !ok || got.Value() != "q" {
		t.Errorf("extras[zzz] = %v, %v; want q", got.Value(), ok)
	}

	prev, existed, err := machine.AddMeta("zzz", "r")
	if err != nil || !existed || prev != "q" {
		t.Errorf("second AddMeta = %v, %v, %v", prev, existed, err)
	}
}

// TestAddMetaContainerNames pins the choice DIVERGENCES.md 42 records. The six
// plural container names have no case in `add_meta`, so 3.8.3 falls into the
// generic branch and OVERWRITES the container with the string — poisoning the
// device — while returning the container as the previous value.
//
// `LabParser` passes `pc1[sysctls]=x` straight through (its `arg` class is
// `\w+`), so the return matters: the first such line must report "there was a
// previous value" and make the parser warn.
func TestAddMetaContainerNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"exec_commands", "sysctls", "envs", "ports", "ulimits", "volumes"} {
		_, machine := newTestMachine(t)

		prev, existed, err := machine.AddMeta(name, "clobber")
		if err != nil {
			t.Fatalf("AddMeta(%q): %v", name, err)
		}
		if !existed {
			t.Errorf("AddMeta(%q) reported no previous value; the container is always present", name)
		}
		if prev == nil {
			t.Errorf("AddMeta(%q) previous = nil, want the container", name)
		}
		if got, ok := machine.Meta.Extras.Get(name); !ok || got.Value() != "clobber" {
			t.Errorf("extras[%q] = %v, %v; want clobber", name, got.Value(), ok)
		}

		// A second assignment reports the string the first one stored, exactly
		// as Python's clobbered dict entry does.
		prev, existed, err = machine.AddMeta(name, "again")
		if err != nil || !existed || prev != "clobber" {
			t.Errorf("second AddMeta(%q) = %v, %v, %v", name, prev, existed, err)
		}
	}

	// The recorded residue: the container itself survives, so the singular
	// spellings keep working where 3.8.3 would now raise a TypeError.
	_, machine := newTestMachine(t)
	if _, _, err := machine.AddMeta("sysctls", "clobber"); err != nil {
		t.Fatalf("AddMeta(sysctls): %v", err)
	}
	if _, _, err := machine.AddMeta("sysctl", "net.a.b=1"); err != nil {
		t.Fatalf("AddMeta(sysctl) after the clobber: %v", err)
	}
	if machine.Sysctls().Len() != 1 {
		t.Errorf("sysctls = %v, want the container intact", machine.Sysctls().Entries())
	}
}

// TestMetaScalars pins the serializer view: the typed scalars plus the extras,
// with privileged and bridged rendered as the bools Python stores. It is the
// `meta.extra` map of the Layer B vector shape.
func TestMetaScalars(t *testing.T) {
	t.Parallel()
	_, machine := newTestMachine(t)

	for _, kv := range [][2]string{
		{"image", "kathara/frr"}, {"mem", "512m"}, {"cpus", "1.5"}, {"shell", "/bin/bash"},
		{"ipv6", "true"}, {"bridged", "true"}, {"privileged", "1"}, {"num_terms", "2"},
		{"entrypoint", "/bin/sh -c"}, {"args", "--foo bar"}, {"custom", "x"},
	} {
		if _, _, err := machine.AddMeta(kv[0], kv[1]); err != nil {
			t.Fatalf("AddMeta(%q): %v", kv[0], err)
		}
	}

	// Every value keeps the type lab.conf gives it: strings everywhere except
	// privileged and bridged, which go through strtobool (vector
	// labconf/meta_all_options).
	want := map[string]any{
		"image": "kathara/frr", "mem": "512m", "cpus": "1.5", "shell": "/bin/bash",
		"ipv6": "true", "num_terms": "2", "privileged": true, "bridged": true,
		"entrypoint": "/bin/sh -c", "args": "--foo bar", "custom": "x",
	}
	got := make(map[string]any)
	for _, entry := range machine.Meta.Scalars() {
		got[entry.Name] = entry.Value.Value()
	}
	if len(got) != len(want) {
		t.Fatalf("Scalars() = %v, want %v", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %#v, want %#v", name, got[name], value)
		}
	}
}

// TestUpdateMeta is EXPECTATIONS-core.md §1 "update_meta (8)" plus the gates
// that differ per key (oracle P19).
func TestUpdateMeta(t *testing.T) {
	t.Parallel()

	t.Run("lists are applied in order", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, err := lab.NewMachine("pc1", &MetaOptions{
			ExecCommands: []string{"echo one", "echo two"},
			Ports:        []string{"8080", "2000:9090/udp"},
			Sysctls:      []string{"net.a.b=1", "net.c.d=x"},
			Envs:         []string{"K=1", "J=2"},
			Ulimits:      []string{"nofile=1024", "nproc=100:200"},
			Volumes:      []string{"/h|/g", "/h2|/g2|rw"},
		})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		if got := machine.ExecCommands(); len(got) != 2 || got[0] != "echo one" {
			t.Errorf("ExecCommands() = %v", got)
		}
		if got := machine.Meta.Ports.Keys(); len(got) != 2 || got[0] != (PortKey{3000, "tcp"}) {
			t.Errorf("port keys = %v", got)
		}
		if got, _ := machine.Meta.Sysctls.Get("net.a.b"); got.Value() != int64(1) {
			t.Errorf("sysctl coercion did not run through add_meta: %v", got.Value())
		}
		if got := machine.Meta.Envs.Keys(); len(got) != 2 || got[0] != "K" {
			t.Errorf("env keys = %v", got)
		}
		if got, _ := machine.Meta.Ulimits.Get("nproc"); got != (Ulimit{100, 200}) {
			t.Errorf("ulimit = %+v", got)
		}
		if machine.Meta.Volumes.Len() != 2 {
			t.Errorf("volumes = %v", machine.Meta.Volumes.Entries())
		}
	})

	t.Run("privileged and bridged need a true value", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		no := false
		machine, err := lab.NewMachine("pc1", &MetaOptions{Privileged: &no, Bridged: &no})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		// Oracle P19: `bridged False` and `privileged False` leave the meta
		// UNSET, which is not the same as setting it to false — Machine.String
		// prints no bridged line at all.
		if machine.Meta.Privileged != nil || machine.Meta.Bridged != nil {
			t.Errorf("a false gate stored something: %v %v", machine.Meta.Privileged, machine.Meta.Bridged)
		}

		yes := true
		machine, err = lab.NewMachine("pc2", &MetaOptions{Privileged: &yes, Bridged: &yes})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if !machine.IsPrivileged() || !machine.IsBridged() {
			t.Error("a true gate did not store")
		}
	})

	t.Run("ipv6 false is recorded", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		no := false
		machine, err := lab.NewMachine("pc1", &MetaOptions{IPv6: &no})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		// Unlike privileged/bridged, ipv6 has no truthy gate — and it is stored
		// as a real bool, which is what makes it override a settings default.
		if _, isBool := machine.Meta.IPv6.AsBool(); !isBool {
			t.Fatalf("ipv6 = %v (%v), want a bool", machine.Meta.IPv6.Value(), machine.Meta.IPv6.Kind())
		}
	})

	t.Run("falsy scalars are applied", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		zero, empty := "0", ""
		machine, err := lab.NewMachine("pc1", &MetaOptions{NumTerms: &zero, Shell: &empty})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if got, err := machine.GetNumTerms(); err != nil || got != 0 {
			t.Errorf("GetNumTerms() = %d, %v; want 0", got, err)
		}
		// `shell=""` is stored, so the accessor returns "" and not the default.
		if got := machine.GetShell(); got != "" {
			t.Errorf("GetShell() = %q, want the stored empty string", got)
		}
	})

	t.Run("the first invalid option in statement order wins", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		// Oracle P19: sysctls are applied before envs, so the sysctl error is
		// the one that surfaces.
		_, err := lab.NewMachine("pc1", &MetaOptions{
			Sysctls: []string{"bad"},
			Envs:    []string{"bad"},
		})
		want := "Invalid sysctl value (`bad`) on `pc1`, missing `=` or value not in `net.` namespace."
		if err == nil || err.Error() != want {
			t.Errorf("error = %v, want %q", err, want)
		}
		// A device that failed to build is not registered.
		if lab.HasMachine("pc1") {
			t.Error("the scenario kept a device whose options were invalid")
		}
	})

	t.Run("args keeps its list shape", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, err := lab.NewMachine("pc1", &MetaOptions{Args: []string{"--a", "b"}})
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		got, isList := machine.Meta.Args.AsStrings()
		if !isList || len(got) != 2 || got[0] != "--a" {
			t.Errorf("args = %#v (%v)", machine.Meta.Args.Value(), machine.Meta.Args.Kind())
		}
	})
}
