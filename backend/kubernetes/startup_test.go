package kubernetes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/KatharaFramework/kathara-go/model"
)

func commandVectors(t *testing.T) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "commands.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vectors map[string]string
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}
	return vectors
}

// TestStartupCommandsString is
// `"; ".join(STARTUP_COMMANDS).format(...)` against the oracle's own output.
func TestStartupCommandsString(t *testing.T) {
	vectors := commandVectors(t)

	tests := []struct {
		name            string
		vector          string
		machineName     string
		sysctlCommands  string
		machineCommands string
	}{
		{
			name:            "sysctls and user commands",
			vector:          "startup_pc1",
			machineName:     "pc1",
			sysctlCommands:  "sysctl -w -q net.ipv4.ip_forward=1",
			machineCommands: "ls",
		},
		{
			// A device with no sysctls and no `exec` leaves an empty command
			// between two separators — `…fi; ; rm -Rf…` — which is exactly what
			// the join produces and what the pod runs.
			name:            "empty sysctls leave an empty command",
			vector:          "startup_empty_sysctls",
			machineName:     "pc1",
			machineCommands: ":",
		},
		{
			// The substituted values are NOT re-scanned: a user command that
			// looks like a replacement field survives verbatim.
			name:            "a substituted value is not re-formatted",
			vector:          "startup_braces",
			machineName:     "pc1",
			machineCommands: "echo {machine_name}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want, ok := vectors[test.vector]
			if !ok {
				t.Fatalf("no vector %q", test.vector)
			}
			got := StartupCommandsString(test.machineName, test.sysctlCommands, test.machineCommands)
			if got != want {
				t.Errorf("startup string mismatch\n got %q\nwant %q", got, want)
			}
		})
	}
}

// TestShutdownCommandsString is the preStop twin, whose order is the reverse of
// the startup one: the device's own script runs before the shared one.
func TestShutdownCommandsString(t *testing.T) {
	vectors := commandVectors(t)

	for _, test := range []struct{ vector, machineName string }{
		{"shutdown_pc1", "pc1"},
		{"shutdown_underscore", "test_device"},
	} {
		t.Run(test.machineName, func(t *testing.T) {
			want, ok := vectors[test.vector]
			if !ok {
				t.Fatalf("no vector %q", test.vector)
			}
			if got := ShutdownCommandsString(test.machineName); got != want {
				t.Errorf("shutdown string mismatch\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestSysctlCommands(t *testing.T) {
	tests := []struct {
		name    string
		entries [][2]any
		want    string
		wantErr bool
	}{
		{
			name: "empty map",
			want: "",
		},
		{
			name:    "ints in insertion order",
			entries: [][2]any{{"net.ipv4.ip_forward", int64(1)}, {"net.ipv4.icmp_ratelimit", int64(0)}},
			want:    "sysctl -w -q net.ipv4.ip_forward=1; sysctl -w -q net.ipv4.icmp_ratelimit=0",
		},
		{
			name:    "a bool renders as 1/0",
			entries: [][2]any{{"a", true}, {"b", false}},
			want:    "sysctl -w -q a=1; sysctl -w -q b=0",
		},
		{
			name:    "a float truncates toward zero",
			entries: [][2]any{{"a", 3.9}},
			want:    "sysctl -w -q a=3",
		},
		{
			name:    "a string is Python's TypeError",
			entries: [][2]any{{"net.a.b", "abc"}},
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sysctls := model.NewOrderedMap[string, model.Scalar]()
			for _, entry := range test.entries {
				key := entry[0].(string)
				switch value := entry[1].(type) {
				case int64:
					sysctls.Set(key, model.Int(value))
				case bool:
					sysctls.Set(key, model.Bool(value))
				case float64:
					sysctls.Set(key, model.Float(value))
				case string:
					sysctls.Set(key, model.Str(value))
				}
			}

			got, err := SysctlCommands(sysctls)
			if test.wantErr {
				if !errors.Is(err, model.ErrPyTypeError) {
					t.Fatalf("error = %v, want a TypeError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("SysctlCommands: %v", err)
			}
			if got != test.want {
				t.Errorf("got %q, want %q", got, test.want)
			}
		})
	}
}

// TestSysctlCommandsNil pins that a device whose sysctl map was never built
// answers the empty string rather than faulting — the accessors on a partially
// built Meta answer a nil map.
func TestSysctlCommandsNil(t *testing.T) {
	got, err := SysctlCommands(nil)
	if err != nil || got != "" {
		t.Fatalf("SysctlCommands(nil) = %q, %v; want \"\", nil", got, err)
	}
}

// TestFormatCommands is the single-pass `str.format`: known fields substituted,
// unknown fields left alone, `{{`/`}}` unescaped, and a substituted value never
// re-scanned.
func TestFormatCommands(t *testing.T) {
	fields := map[string]string{"a": "1", "b": "{a}"}

	tests := []struct {
		name     string
		template string
		want     string
	}{
		{name: "plain substitution", template: "x{a}y", want: "x1y"},
		{name: "substituted value is not re-scanned", template: "{b}", want: "{a}"},
		{name: "unknown field is left alone", template: "{zzz}", want: "{zzz}"},
		{name: "doubled braces unescape", template: "{{a}}", want: "{a}"},
		{name: "unbalanced open brace is kept", template: "x{a", want: "x{a"},
		{name: "stray close brace is kept", template: "x}y", want: "x}y"},
		{name: "no fields at all", template: "plain", want: "plain"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatCommands(test.template, fields); got != test.want {
				t.Errorf("formatCommands(%q) = %q, want %q", test.template, got, test.want)
			}
		})
	}
}
