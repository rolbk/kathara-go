package main

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// runConfigJSON drives one `kathara config …` invocation in json mode and
// returns the exit code and the single line of stdout.
func runConfigJSON(t *testing.T, a *testApp, args ...string) (int, string) {
	t.Helper()
	spec := commandTable(a.app)["config"]
	code := runCommand(t.Context(), a.app, spec, append([]string{"--format", "json"}, args...))
	return code, strings.TrimRight(a.stdoutString(), "\n")
}

// reloadSaved reads back what the command wrote, which is the only proof that
// `config set` reached the disk and that the file it wrote still parses.
func reloadSaved(t *testing.T, a *testApp) *settings.Settings {
	t.Helper()
	saved, err := settings.Load(a.settingsDir)
	if err != nil {
		t.Fatalf("the saved file does not load back: %v", err)
	}
	return saved
}

// kubernetesApp is a test app whose `manager_type` has already been switched,
// which is what makes the five Kubernetes keys visible at all.
func kubernetesApp(t *testing.T) *testApp {
	t.Helper()
	a := newTestApp(t)
	if err := a.settings.Set("manager_type", "kubernetes"); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestConfigSetEveryKeyRoundTrips is PORT_SPEC §3.2 item 2 over the whole
// schema: every key of both addons, written by `config set`, read back off the
// file, and reported in the E12 `set` envelope (JSON_CLI_CONTRACT.md §3.12).
//
// `terminal` is exercised with TMUX because that is the one value
// `Setting.check_terminal` short-circuits (Setting.py:288); any other answer
// depends on which emulators the host has installed, which a unit test may not.
func TestConfigSetEveryKeyRoundTrips(t *testing.T) {
	cases := []struct {
		key        string
		arg        string
		wantJSON   string
		wantStored any
		kubernetes bool
	}{
		{key: "image", arg: "kathara/frr", wantJSON: `"kathara/frr"`, wantStored: "kathara/frr"},
		{key: "manager_type", arg: "kubernetes", wantJSON: `"kubernetes"`, wantStored: "kubernetes"},
		{key: "terminal", arg: "TMUX", wantJSON: `"TMUX"`, wantStored: "TMUX"},
		{key: "open_terminals", arg: "false", wantJSON: `false`, wantStored: false},
		{key: "device_shell", arg: "/bin/zsh", wantJSON: `"/bin/zsh"`, wantStored: "/bin/zsh"},
		{key: "net_prefix", arg: "lab_net", wantJSON: `"lab_net"`, wantStored: "lab_net"},
		{key: "device_prefix", arg: "lab_dev", wantJSON: `"lab_dev"`, wantStored: "lab_dev"},
		{key: "debug_level", arg: "DEBUG", wantJSON: `"DEBUG"`, wantStored: "DEBUG"},
		{key: "print_startup_log", arg: "no", wantJSON: `false`, wantStored: false},
		{key: "enable_ipv6", arg: "yes", wantJSON: `true`, wantStored: true},
		{key: "volume_mount_policy", arg: "Never", wantJSON: `"Never"`, wantStored: "Never"},
		{key: "last_checked", arg: "1785923124.0260758", wantJSON: `1785923124.0260758`, wantStored: 1785923124.0260758},

		{key: "hosthome_mount", arg: "true", wantJSON: `true`, wantStored: true},
		{key: "shared_mount", arg: "0", wantJSON: `false`, wantStored: false},
		{key: "image_update_policy", arg: "Always", wantJSON: `"Always"`, wantStored: "Always"},
		{key: "shared_cds", arg: "3", wantJSON: `3`, wantStored: settings.SharedBetweenUsers},
		{key: "remote_url", arg: "http://10.0.0.1:2375", wantJSON: `"http://10.0.0.1:2375"`, wantStored: "http://10.0.0.1:2375"},
		{key: "remote_url", arg: "", wantJSON: `null`, wantStored: nil},
		{key: "cert_path", arg: "/etc/docker/certs", wantJSON: `"/etc/docker/certs"`, wantStored: "/etc/docker/certs"},
		{key: "cert_path", arg: "", wantJSON: `null`, wantStored: nil},
		{key: "network_plugin", arg: "kathara/katharanp", wantJSON: `"kathara/katharanp"`, wantStored: "kathara/katharanp"},
		{key: "network_plugin", arg: "", wantJSON: `null`, wantStored: nil},

		{key: "api_server_url", arg: "https://k8s.example.com:6443", wantJSON: `"https://k8s.example.com:6443"`, wantStored: "https://k8s.example.com:6443", kubernetes: true},
		{key: "api_server_url", arg: "", wantJSON: `null`, wantStored: nil, kubernetes: true},
		{key: "api_token", arg: "s3cr3t", wantJSON: `"s3cr3t"`, wantStored: "s3cr3t", kubernetes: true},
		{key: "api_token", arg: "", wantJSON: `null`, wantStored: nil, kubernetes: true},
		{key: "host_shared", arg: "off", wantJSON: `false`, wantStored: false, kubernetes: true},
		{key: "image_pull_policy", arg: "Never", wantJSON: `"Never"`, wantStored: "Never", kubernetes: true},
		{key: "image_pull_policy", arg: "", wantJSON: `null`, wantStored: nil, kubernetes: true},
		{key: "docker_config_json", arg: "", wantJSON: `null`, wantStored: nil, kubernetes: true},
	}

	for _, tc := range cases {
		t.Run(tc.key+"="+tc.arg, func(t *testing.T) {
			a := newTestApp(t)
			if tc.kubernetes {
				a = kubernetesApp(t)
			}

			code, out := runConfigJSON(t, a, "set", tc.key, tc.arg)
			if code != 0 {
				t.Fatalf("exit = %d: %s%s", code, out, a.stderrString())
			}
			want := `{"key":"` + tc.key + `","value":` + tc.wantJSON + `,"saved":true}`
			if out != want {
				t.Errorf("envelope\n got: %s\nwant: %s", out, want)
			}

			stored, err := reloadSaved(t, a).Get(tc.key)
			if err != nil {
				t.Fatalf("Get after reload: %v", err)
			}
			if stored != tc.wantStored {
				t.Errorf("reloaded %s = %#v, want %#v", tc.key, stored, tc.wantStored)
			}
		})
	}
}

// TestConfigGetEveryKey checks the read envelope for every key of both
// schemas, and that `get` never touches the file.
func TestConfigGetEveryKey(t *testing.T) {
	for _, manager := range []string{"docker", "kubernetes"} {
		a := newTestApp(t)
		if manager == "kubernetes" {
			a = kubernetesApp(t)
		}
		keys, err := a.settings.Keys()
		if err != nil {
			t.Fatal(err)
		}
		if len(keys) == 0 {
			t.Fatalf("%s schema is empty", manager)
		}

		for _, key := range keys {
			t.Run(manager+"/"+key, func(t *testing.T) {
				a := newTestApp(t)
				if manager == "kubernetes" {
					a = kubernetesApp(t)
				}
				code, out := runConfigJSON(t, a, "get", key)
				if code != 0 {
					t.Fatalf("exit = %d: %s", code, a.stderrString())
				}
				if !strings.HasPrefix(out, `{"key":"`+key+`","value":`) || !strings.HasSuffix(out, "}") {
					t.Errorf("envelope = %s", out)
				}
				if _, err := os.Stat(filepath.Join(a.settingsDir, settings.Filename)); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("`config get` wrote the settings file")
				}
			})
		}
	}
}

// TestConfigValidationFailures is the whole of §3.2 item 4 seen from the
// scriptable side: every restriction `settings/` carries, rejected here with
// its own message, exit 1, and the frozen `Settings` code of
// JSON_CLI_CONTRACT.md §3.12.
//
// Nothing in this table is a check `cmd/kathara` implements; each row fails
// inside `settings`, which is the point.
func TestConfigValidationFailures(t *testing.T) {
	cases := []struct {
		name          string
		key, arg      string
		wantMsg       string
		wantCode      string
		kubernetes    bool
		skipOnWindows bool
	}{
		{name: "unknown key", key: "nope", arg: "x",
			wantMsg: "Setting `nope` not found.", wantCode: kerrors.CodeSettings},
		{name: "a key of the other backend is not found", key: "api_token", arg: "x",
			wantMsg: "Setting `api_token` not found.", wantCode: kerrors.CodeSettings},
		{name: "a docker key is not found under kubernetes", key: "hosthome_mount", arg: "true",
			wantMsg: "Setting `hosthome_mount` not found.", wantCode: kerrors.CodeSettings, kubernetes: true},
		{name: "manager_type", key: "manager_type", arg: "podman",
			wantMsg: "Manager Type not allowed.", wantCode: kerrors.CodeSettings},
		{name: "manager_type is case sensitive", key: "manager_type", arg: "Docker",
			wantMsg: "Manager Type not allowed.", wantCode: kerrors.CodeSettings},
		// On Windows this case is skipped in the loop below: Python's arm is
		// `lambda: True` (Setting.py:293), so the value is accepted there.
		{name: "terminal", key: "terminal", arg: "/nonexistent/emulator",
			wantCode: kerrors.CodeSettings, skipOnWindows: true},
		{name: "net_prefix", key: "net_prefix", arg: "Kathara1",
			wantMsg: "Networks Prefix must only contain lowercase letters and underscore.", wantCode: kerrors.CodeSettings},
		{name: "device_prefix", key: "device_prefix", arg: "dev-1",
			wantMsg: "Device Prefix must only contain lowercase letters and underscore.", wantCode: kerrors.CodeSettings},
		{name: "debug_level", key: "debug_level", arg: "TRACE",
			wantCode: kerrors.CodeSettings},
		{name: "volume_mount_policy", key: "volume_mount_policy", arg: "Sometimes",
			wantMsg:  "Setting `volume_mount_policy` must be one of the following: Always, Prompt, Never.",
			wantCode: kerrors.CodeSettings},
		{name: "a bool that is not a bool", key: "open_terminals", arg: "maybe",
			wantMsg: "Setting `open_terminals` must be a boolean.", wantCode: kerrors.CodeSettings},
		{name: "a float that is not a float", key: "last_checked", arg: "soon",
			wantMsg: "Setting `last_checked` must be a number.", wantCode: kerrors.CodeSettings},
		{name: "a float that would not load back", key: "last_checked", arg: "inf",
			wantMsg: "Setting `last_checked` must be a number.", wantCode: kerrors.CodeSettings},
		{name: "image_update_policy", key: "image_update_policy", arg: "Sometimes",
			wantMsg:  "Setting `image_update_policy` must be one of the following: Prompt, Always, Never.",
			wantCode: kerrors.CodeSettings},
		{name: "shared_cds out of range", key: "shared_cds", arg: "7",
			wantMsg: "Setting `shared_cds` must be one of the following: 1, 2, 3.", wantCode: kerrors.CodeSettings},
		{name: "shared_cds not an integer", key: "shared_cds", arg: "two",
			wantMsg: "Setting `shared_cds` must be an integer.", wantCode: kerrors.CodeSettings},
		{name: "network_plugin", key: "network_plugin", arg: "kathara/other",
			wantMsg:  "Setting `network_plugin` must be one of the following: kathara/katharanp, kathara/katharanp_vde.",
			wantCode: kerrors.CodeSettings},
		{name: "image_pull_policy", key: "image_pull_policy", arg: "Sometimes",
			wantMsg:  "Setting `image_pull_policy` must be one of the following: Always, IfNotPresent, Never.",
			wantCode: kerrors.CodeSettings, kubernetes: true},
		{name: "docker_config_json path does not exist", key: "docker_config_json", arg: "/nonexistent/config.json",
			wantCode: kerrors.CodeOS, kubernetes: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.skipOnWindows && runtime.GOOS == "windows" {
				t.Skip("this validation is a no-op on Windows in Python")
			}
			a := newTestApp(t)
			if tc.kubernetes {
				a = kubernetesApp(t)
			}

			code, out := runConfigJSON(t, a, "set", tc.key, tc.arg)
			if code != 1 {
				t.Fatalf("exit = %d, want 1 (out: %s)", code, out)
			}
			if !strings.Contains(out, `"code":"`+tc.wantCode+`"`) {
				t.Errorf("envelope = %s, want code %s", out, tc.wantCode)
			}
			if tc.wantMsg != "" && !strings.Contains(out, tc.wantMsg) {
				t.Errorf("envelope = %s, want message %q", out, tc.wantMsg)
			}
			// A rejected value must not have reached the file.
			if _, err := os.Stat(filepath.Join(a.settingsDir, settings.Filename)); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a rejected `config set` wrote the settings file")
			}
		})
	}
}

// TestConfigGetUnknownKey is the read half of the same rule.
func TestConfigGetUnknownKey(t *testing.T) {
	a := newTestApp(t)
	code, out := runConfigJSON(t, a, "get", "nope")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(out, `"code":"Settings"`) || !strings.Contains(out, "Setting `nope` not found.") {
		t.Errorf("envelope = %s", out)
	}
}

// TestConfigListEnvelope pins the §3.12 `list` shape: the twelve base keys in
// `_to_dict` order, then the active addon's, which is the file's own order.
func TestConfigListEnvelope(t *testing.T) {
	a := kubernetesApp(t)
	code, out := runConfigJSON(t, a, "list")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}
	if !strings.HasPrefix(out, `{"settings":{"image":`) {
		t.Fatalf("envelope = %s", out)
	}

	keys, err := a.settings.Keys()
	if err != nil {
		t.Fatal(err)
	}
	at := -1
	for _, key := range keys {
		next := strings.Index(out, `"`+key+`":`)
		if next < 0 {
			t.Fatalf("%s missing from %s", key, out)
		}
		if next <= at {
			t.Errorf("%s is out of schema order in %s", key, out)
		}
		at = next
	}
}

// TestConfigListHumanFormat is the same listing in human mode: one `key =
// value` line per key, in the same order, and no JSON.
func TestConfigListHumanFormat(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["config"]
	if code := runCommand(t.Context(), a.app, spec, []string{"list"}); code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}

	lines := strings.Split(strings.TrimRight(a.stdoutString(), "\n"), "\n")
	keys, err := a.settings.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != len(keys) {
		t.Fatalf("got %d lines for %d keys:\n%s", len(lines), len(keys), a.stdoutString())
	}
	for i, key := range keys {
		if !strings.HasPrefix(lines[i], key+" = ") {
			t.Errorf("line %d = %q, want the %s row", i, lines[i], key)
		}
	}
	if strings.Contains(a.stdoutString(), "{") {
		t.Errorf("human mode emitted an envelope:\n%s", a.stdoutString())
	}
}

// TestConfigReset is `reset` with no key: the file goes back to the defaults
// and the process's own settings go with it.
func TestConfigReset(t *testing.T) {
	a := newTestApp(t)
	if _, out := runConfigJSON(t, a, "set", "image", "kathara/frr"); !strings.Contains(out, "kathara/frr") {
		t.Fatalf("setup failed: %s", out)
	}
	a.stdout.Reset()

	code, out := runConfigJSON(t, a, "reset")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}
	if !strings.Contains(out, `"image":"kathara/base"`) || !strings.HasSuffix(out, `,"saved":true}`) {
		t.Errorf("envelope = %s", out)
	}
	if a.settings.Image != "kathara/base" {
		t.Errorf("the live settings were not reset: %s", a.settings.Image)
	}
	if got := reloadSaved(t, a).Image; got != "kathara/base" {
		t.Errorf("the file was not reset: %s", got)
	}
}

// TestConfigResetKey is `reset <key>`: one key back to its default, every other
// key left where it was.
func TestConfigResetKey(t *testing.T) {
	a := newTestApp(t)
	for _, kv := range [][2]string{{"image", "kathara/frr"}, {"device_shell", "/bin/zsh"}} {
		if code, out := runConfigJSON(t, a, "set", kv[0], kv[1]); code != 0 {
			t.Fatalf("setup failed: %s", out)
		}
		a.stdout.Reset()
	}

	code, out := runConfigJSON(t, a, "reset", "image")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}
	if !strings.Contains(out, `"image":"kathara/base"`) {
		t.Errorf("envelope = %s", out)
	}
	if !strings.Contains(out, `"device_shell":"/bin/zsh"`) {
		t.Errorf("`reset <key>` reset a neighbouring key: %s", out)
	}

	saved := reloadSaved(t, a)
	if saved.Image != "kathara/base" || saved.DeviceShell != "/bin/zsh" {
		t.Errorf("file = %s / %s", saved.Image, saved.DeviceShell)
	}
}

// TestConfigResetKeyOfTheActiveAddon checks the `manager_type`-aware default
// lookup: `api_token` has no default in a docker-typed schema at all.
func TestConfigResetKeyOfTheActiveAddon(t *testing.T) {
	a := kubernetesApp(t)
	if code, out := runConfigJSON(t, a, "set", "host_shared", "false"); code != 0 {
		t.Fatalf("setup failed: %s", out)
	}
	a.stdout.Reset()

	code, out := runConfigJSON(t, a, "reset", "host_shared")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}
	if !strings.Contains(out, `"host_shared":true`) {
		t.Errorf("envelope = %s", out)
	}
	if !reloadSaved(t, a).HostShared {
		t.Errorf("the file was not reset")
	}
}

// TestConfigResetManagerType is the one key that must *not* take its default
// from the current file. The addon-aware lookup forces the fresh settings'
// `manager_type` to the current one so that the addon keys exist; applied to
// `manager_type` itself that makes the key its own default, and the reset
// becomes a no-op that still reports `saved: true`.
func TestConfigResetManagerType(t *testing.T) {
	a := kubernetesApp(t)
	if code, out := runConfigJSON(t, a, "set", "api_token", "secret"); code != 0 {
		t.Fatalf("setup failed: %s", out)
	}
	a.stdout.Reset()

	code, out := runConfigJSON(t, a, "reset", "manager_type")
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, a.stderrString())
	}
	if !strings.Contains(out, `"manager_type":"docker"`) {
		t.Errorf("envelope = %s, want the default manager", out)
	}
	// The write goes through Set, so the switch also rebuilt the schema: the
	// Kubernetes keys are gone from the envelope and from the file.
	if strings.Contains(out, `"api_token"`) {
		t.Errorf("the kubernetes addon survived the reset: %s", out)
	}
	if saved := reloadSaved(t, a); saved.ManagerType != "docker" {
		t.Errorf("the file kept manager_type = %q", saved.ManagerType)
	}
}

// TestConfigResetUnknownKey keeps `reset` on the same error contract as the
// other two key-taking operations.
func TestConfigResetUnknownKey(t *testing.T) {
	a := newTestApp(t)
	code, out := runConfigJSON(t, a, "reset", "nope")
	if code != 1 || !strings.Contains(out, `"code":"Settings"`) {
		t.Fatalf("exit = %d, envelope = %s", code, out)
	}
}

// TestConfigSetManagerTypeResetsTheAddon is `update_setting_value`'s `reload`
// flag (`cli/ui/setting/utils.py:57-64`) reached from the scriptable side: the
// switch builds a fresh addon, so the previous backend's values do not leak.
func TestConfigSetManagerTypeResetsTheAddon(t *testing.T) {
	a := newTestApp(t)
	if code, out := runConfigJSON(t, a, "set", "shared_cds", "3"); code != 0 {
		t.Fatalf("setup failed: %s", out)
	}
	a.stdout.Reset()

	if code, out := runConfigJSON(t, a, "set", "manager_type", "kubernetes"); code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	a.stdout.Reset()

	// Back to docker: `shared_cds` must be the default again, not the 3 the
	// file carried before the switch.
	if code, out := runConfigJSON(t, a, "set", "manager_type", "docker"); code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	a.stdout.Reset()

	code, out := runConfigJSON(t, a, "get", "shared_cds")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if out != `{"key":"shared_cds","value":1}` {
		t.Errorf("envelope = %s, want the default", out)
	}
}

// TestConfigSetDockerConfigJSONTakesAPath is the OQ-12c ruling: the word after
// the key is a *path*, and what is stored is the base64 of CPython's
// re-serialization of that file — which is what the settings screen's
// `store_b64_docker_json_callback` produced.
func TestConfigSetDockerConfigJSONTakesAPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{\n  \"auths\":   {}\n}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	a := kubernetesApp(t)
	code, out := runConfigJSON(t, a, "set", "docker_config_json", path)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	// base64 of `{"auths": {}}`, the whitespace already collapsed.
	const want = "eyJhdXRocyI6IHt9fQ=="
	if out != `{"key":"docker_config_json","value":"`+want+`","saved":true}` {
		t.Errorf("envelope = %s", out)
	}
	stored := reloadSaved(t, a).DockerConfigJSON
	if stored == nil || *stored != want {
		t.Errorf("stored = %v, want %q", stored, want)
	}
}

// TestConfigUsageErrors is the argparse contract of JSON_CLI_CONTRACT.md §5.5:
// a usage failure is exit 2 with the text on stderr and nothing on stdout, in
// every format.
func TestConfigUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"get"},
		{"get", "image", "extra"},
		{"set", "image"},
		{"set", "image", "a", "b"},
		{"list", "extra"},
		{"reset", "image", "extra"},
		{"frobnicate"},
	}

	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			a := newTestApp(t)
			code, out := runConfigJSON(t, a, args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2", code)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing", out)
			}
			if !strings.Contains(a.stderrString(), "kathara config: error:") {
				t.Errorf("stderr = %q", a.stderrString())
			}
		})
	}
}

// TestConfigHumanOutput is the non-JSON rendering: `key = value`, with Python's
// own spellings for the two types that have one.
func TestConfigHumanOutput(t *testing.T) {
	cases := []struct{ args, want []string }{
		{args: []string{"get", "image"}, want: []string{"image = kathara/base"}},
		{args: []string{"get", "open_terminals"}, want: []string{"open_terminals = True"}},
		{args: []string{"get", "remote_url"}, want: []string{"remote_url = None"}},
		{args: []string{"get", "shared_cds"}, want: []string{"shared_cds = 1"}},
		{args: []string{"set", "enable_ipv6", "true"}, want: []string{"enable_ipv6 = True"}},
		{args: []string{"reset"}, want: []string{"Settings reset to their defaults."}},
	}

	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			a := newTestApp(t)
			spec := commandTable(a.app)["config"]
			if code := runCommand(t.Context(), a.app, spec, tc.args); code != 0 {
				t.Fatalf("exit = %d: %s", code, a.stderrString())
			}
			for _, want := range tc.want {
				if !strings.Contains(a.stdoutString(), want) {
					t.Errorf("stdout = %q, want %q", a.stdoutString(), want)
				}
			}
		})
	}
}

// TestConfigHumanErrorLine is ERROR_CODES.md §0.1's human rendering, which is
// the same `CRITICAL (SettingsError) …` line the rest of the CLI produces.
func TestConfigHumanErrorLine(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["config"]
	if code := runCommand(t.Context(), a.app, spec, []string{"set", "net_prefix", "Kathara"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	// The frozen `SettingsError` wrapper, message and all (kerrors messages.go).
	want := "CRITICAL (SettingsError) Settings file is not valid: " +
		"Networks Prefix must only contain lowercase letters and underscore. " +
		"Fix it or delete it before launching."
	if !strings.Contains(a.stdoutString(), want) {
		t.Errorf("stdout = %q, want %q", a.stdoutString(), want)
	}
}

// TestConfigRejectsJSONL keeps `config` off the streaming format
// (JSON_CLI_CONTRACT.md §1.1: json only).
func TestConfigRejectsJSONL(t *testing.T) {
	a := newTestApp(t)
	spec := commandTable(a.app)["config"]
	if code := runCommand(t.Context(), a.app, spec, []string{"--format", "jsonl", "list"}); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// TestConfigSetSavesTheWholeFile checks that a `config set` writes a document
// the *loader* accepts — the encoder and the decoder are the same schema table,
// and a key written in the wrong place would break every later run.
func TestConfigSetSavesTheWholeFile(t *testing.T) {
	// settings.Save writes text-mode line endings (CRLF on Windows), the
	// deliberate parity with Python's `open(..., "w")` — normalise before the
	// byte comparison below so the LF-shaped expectation holds everywhere.
	a := newTestApp(t)
	if code, out := runConfigJSON(t, a, "set", "image", "kathara/frr"); code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}

	data, err := os.ReadFile(filepath.Join(a.settingsDir, settings.Filename))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := a.settings.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != string(encoded) {
		t.Errorf("file =\n%s\nwant\n%s", got, encoded)
	}
}

// TestSettingsCheckIsSkippedForConfig is the PROPOSED-DIVERGENCES entry
// "`kathara config` is exempt from the startup settings check": `config` is how
// a broken file gets repaired, so the check may not run in front of it.
func TestSettingsCheckIsSkippedForConfig(t *testing.T) {
	a := newTestApp(t)
	a.checkSettings = func() error { return kerrors.ErrSettingsManagerType }
	a.console.Format = cliout.FormatJSON

	if code := dispatch(t.Context(), a.app, []string{"kathara", "config", "--format", "json", "get", "image"}); code != 0 {
		t.Fatalf("exit = %d, want 0: %s", code, a.stdoutString())
	}
}
