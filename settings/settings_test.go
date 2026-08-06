package settings

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ---------------------------------------------------------------------------
// Fixtures and helpers
// ---------------------------------------------------------------------------

// roundTripCase is one row of testdata/conf_roundtrip.json: a `kathara.conf`
// handed to 3.8.3, and the bytes 3.8.3 wrote back after loading and saving it.
type roundTripCase struct {
	Name   string `json:"name"`
	Note   string `json:"note"`
	Input  string `json:"input"`
	Output string `json:"output"`
	Error  string `json:"error"`
}

func loadJSONFixture[T any](t *testing.T, name string) T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var out T
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return out
}

// writeConf drops a `kathara.conf` into a fresh directory and returns it.
func writeConf(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, Filename), []byte(body), 0o600); err != nil {
		t.Fatalf("write conf: %v", err)
	}
	return dir
}

// pinDefaultPath redirects DEFAULT_SETTINGS_PATH for the duration of a test.
// Check and Wipe write there, and a test that could not redirect it would
// scribble on the developer's own configuration.
func pinDefaultPath(t *testing.T, path string) {
	t.Helper()
	prev := defaultPathFn
	defaultPathFn = func() (string, error) { return path, nil }
	t.Cleanup(func() { defaultPathFn = prev })
}

// pinClock freezes time.time().
func pinClock(t *testing.T, now float64) {
	t.Helper()
	prev := timeNow
	timeNow = func() float64 { return now }
	t.Cleanup(func() { timeNow = prev })
}

// ---------------------------------------------------------------------------
// The frozen file format
// ---------------------------------------------------------------------------

// TestRoundTripAgainstOracle is the byte-for-byte contract with 3.8.3: load
// what Python loaded, save, and produce the same file Python produced. Every
// row was recorded by running the real `Setting` (testdata/conf_roundtrip.json).
func TestRoundTripAgainstOracle(t *testing.T) {
	cases := loadJSONFixture[[]roundTripCase](t, "conf_roundtrip.json")
	if len(cases) < 15 {
		t.Fatalf("fixture shrank: %d cases", len(cases))
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Name == "only_last_checked" && runtime.GOOS != "linux" {
				// The row exercises DEFAULTS, and DEFAULTS["terminal"] is
				// platform-dependent.
				t.Skip("recorded on Linux; terminal default differs per platform")
			}

			s, err := Load(writeConf(t, tc.Input))

			if tc.Error != "" {
				if err == nil {
					t.Fatalf("expected an error (Python: %s), got none", tc.Error)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			got, err := s.Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			if string(got) != tc.Output {
				t.Errorf("%s\nnote: %s\n got: %q\nwant: %q", tc.Name, tc.Note, got, tc.Output)
			}
		})
	}
}

// TestRealConfIsUnchanged is the narrow, load-bearing case of the table above,
// spelled on its own: the file an existing 3.8.3 install has on disk must come
// back out identical, or the port silently rewrites everybody's configuration
// on first run.
func TestRealConfIsUnchanged(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "kathara.conf.3.8.3"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	dir := writeConf(t, string(want))
	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := s.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("round trip changed the file\n got: %q\nwant: %q", got, want)
	}
}

// TestEncodeSerializationShape pins the three properties of `json.dumps(...,
// indent=True)` that a future refactor could lose without any single value
// changing.
func TestEncodeSerializationShape(t *testing.T) {
	pinClock(t, 1785923124.0260758)
	data, err := Defaults().Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out := string(data)

	if out[len(out)-1] == '\n' {
		t.Error("output ends with a newline; json.dumps does not add one")
	}
	if !hasPrefix(out, "{\n \"image\": ") {
		t.Errorf("indent is not one space: %.32q", out)
	}
	if containsSubstring(out, ", \"") {
		t.Error("entries are separated by \", \" instead of \",\\n\"")
	}
	if !containsSubstring(out, "\"image\": \"kathara/base\"") {
		t.Error("key and value are not separated by \": \"")
	}
}

func hasPrefix(s, p string) bool { return len(s) >= len(p) && s[:len(p)] == p }

func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestKeyOrderIsFrozen pins the schema's key order per manager, which *is* the
// file format (ORDERING.tsv:111, :113).
func TestKeyOrderIsFrozen(t *testing.T) {
	base := []string{
		"image", "manager_type", "terminal", "open_terminals", "device_shell",
		"net_prefix", "device_prefix", "debug_level", "print_startup_log",
		"enable_ipv6", "volume_mount_policy", "last_checked",
	}

	for _, tc := range []struct {
		manager string
		addon   []string
	}{
		{"docker", []string{"hosthome_mount", "shared_mount", "image_update_policy",
			"shared_cds", "remote_url", "cert_path", "network_plugin"}},
		{"kubernetes", []string{"api_server_url", "api_token", "host_shared",
			"image_pull_policy", "docker_config_json"}},
	} {
		t.Run(tc.manager, func(t *testing.T) {
			s := Defaults()
			s.ManagerType = tc.manager
			keys, err := s.Keys()
			if err != nil {
				t.Fatalf("Keys: %v", err)
			}
			want := append(append([]string(nil), base...), tc.addon...)
			if len(keys) != len(want) {
				t.Fatalf("got %d keys, want %d: %v", len(keys), len(want), keys)
			}
			for i := range want {
				if keys[i] != want[i] {
					t.Errorf("key %d = %q, want %q", i, keys[i], want[i])
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Nilability
// ---------------------------------------------------------------------------

// TestNullableKeysRoundTrip pins the tri-state the pointers exist for: `null`
// is not `""`, and neither is silently promoted to the default
// (NILABILITY.tsv:47-52).
func TestNullableKeysRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name  string
		body  string
		check func(*testing.T, *Settings)
	}{
		{
			name: "null stays null",
			body: `{"manager_type":"docker","remote_url":null,"cert_path":null,"network_plugin":null}`,
			check: func(t *testing.T, s *Settings) {
				for name, got := range map[string]*string{
					"remote_url": s.RemoteURL, "cert_path": s.CertPath, "network_plugin": s.NetworkPlugin,
				} {
					if got != nil {
						t.Errorf("%s = %q, want nil", name, *got)
					}
				}
			},
		},
		{
			name: "empty string is not null",
			body: `{"manager_type":"docker","remote_url":"","cert_path":""}`,
			check: func(t *testing.T, s *Settings) {
				if s.RemoteURL == nil || *s.RemoteURL != "" {
					t.Errorf("remote_url = %v, want a pointer to \"\"", s.RemoteURL)
				}
			},
		},
		{
			name: "kubernetes nulls",
			body: `{"manager_type":"kubernetes","api_server_url":null,"api_token":null,"image_pull_policy":null,"docker_config_json":null}`,
			check: func(t *testing.T, s *Settings) {
				if s.APIServerURL != nil || s.APIToken != nil || s.ImagePullPolicy != nil || s.DockerConfigJSON != nil {
					t.Error("a kubernetes nullable key did not survive as nil")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Load(writeConf(t, tc.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			tc.check(t, s)

			encoded, err := s.Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			reloaded, err := Load(writeConf(t, string(encoded)))
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			again, err := reloaded.Encode()
			if err != nil {
				t.Fatalf("re-Encode: %v", err)
			}
			if string(again) != string(encoded) {
				t.Errorf("second round trip differs\n got: %q\nwant: %q", again, encoded)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Load semantics
// ---------------------------------------------------------------------------

// TestUnknownKeysAreDropped is `hasattr(self, name)` on load and a `_to_dict`
// built from the schema on save (probed against 3.8.3).
func TestUnknownKeysAreDropped(t *testing.T) {
	body := `{"image":"kathara/frr","manager_type":"docker","last_checked":1.0,
	          "unknown_key":"hello","another":[1,2],"addons":"x","api_token":"leaks?"}`

	s, err := Load(writeConf(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	encoded, err := s.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	for _, gone := range []string{"unknown_key", "another", "addons", "api_token"} {
		if containsSubstring(string(encoded), gone) {
			t.Errorf("%q survived into the saved file", gone)
		}
	}
	if s.Image != "kathara/frr" {
		t.Errorf("image = %q, want kathara/frr", s.Image)
	}

	// The same key holding an *object* is where Python stops being merely
	// wasteful: `addons` is a real slot, so the assignment replaces the addon
	// instance with a dict, `hasattr` starts answering through `dict.get`, and
	// the next addon key in the file dies with "'dict' object has no attribute
	// 'hosthome_mount'" (probed against 3.8.3). The schema has no such slot to
	// clobber, so the key is dropped and the file loads. DIVERGENCES.md 22.
	clobber, err := Load(writeConf(t, `{"addons":{"a":1},"image":"z","hosthome_mount":true}`))
	if err != nil {
		t.Fatalf("Load with an addons object: %v", err)
	}
	if !clobber.HosthomeMount || clobber.Image != "z" {
		t.Errorf("the keys after `addons` were not applied: %+v", clobber)
	}
}

// TestManagerSwitchDropsOtherAddon: a file written under docker and loaded
// under kubernetes keeps neither the docker keys nor their values.
func TestManagerSwitchDropsOtherAddon(t *testing.T) {
	body := `{"manager_type":"kubernetes","last_checked":1.0,
	          "hosthome_mount":true,"shared_cds":3,"network_plugin":"kathara/katharanp"}`

	s, err := Load(writeConf(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.HosthomeMount {
		t.Error("a docker key was applied while manager_type is kubernetes")
	}
	encoded, err := s.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if containsSubstring(string(encoded), "hosthome_mount") {
		t.Error("docker keys were written under manager_type kubernetes")
	}
	if !containsSubstring(string(encoded), "api_server_url") {
		t.Error("kubernetes keys are missing")
	}
}

// TestLoadFromDiskResetsAddonKeys pins the asymmetry
// `Command._load_custom_configuration` inherits: a per-scenario `kathara.conf`
// overlays the base keys but *resets* the active addon's keys to their
// defaults, because `load_settings_addon()` builds a fresh addon object first.
// The expected file is 3.8.3's own output (testdata/conf_overlay.json).
func TestLoadFromDiskResetsAddonKeys(t *testing.T) {
	fixture := loadJSONFixture[struct {
		Global string `json:"global"`
		Lab    string `json:"lab"`
		Output string `json:"output"`
		Note   string `json:"note"`
	}](t, "conf_overlay.json")

	s, err := Load(writeConf(t, fixture.Global))
	if err != nil {
		t.Fatalf("Load global: %v", err)
	}
	if err := s.LoadFromDisk(writeConf(t, fixture.Lab)); err != nil {
		t.Fatalf("LoadFromDisk lab: %v", err)
	}

	got, err := s.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if string(got) != fixture.Output {
		t.Errorf("%s\n got: %q\nwant: %q", fixture.Note, got, fixture.Output)
	}

	// The two halves of the asymmetry, spelled out so a failure says which.
	if s.DevicePrefix != "mypfx" {
		t.Errorf("base key was reset: device_prefix = %q", s.DevicePrefix)
	}
	if s.RemoteURL != nil {
		t.Errorf("addon key survived the reload: remote_url = %q", *s.RemoteURL)
	}
}

// TestLoadErrors covers the three refusals, including the two where the Go
// answer is not Python's traceback (DIVERGENCES.md).
func TestLoadErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		dir := t.TempDir()
		_, err := Load(dir)

		var notFound *kerrors.SettingsNotFoundError
		if !errors.As(err, &notFound) {
			t.Fatalf("err = %v, want *SettingsNotFoundError", err)
		}
		if want := filepath.Join(dir, Filename); notFound.Path != want {
			t.Errorf("path = %q, want %q", notFound.Path, want)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Error("the error does not unwrap to fs.ErrNotExist")
		}
	})

	t.Run("not JSON", func(t *testing.T) {
		_, err := Load(writeConf(t, "not json at all"))
		if !errors.Is(err, kerrors.ErrSettingsInvalidJSON) {
			t.Fatalf("err = %v, want ErrSettingsInvalidJSON", err)
		}
		want := "Settings file is not valid: Not a valid JSON. Fix it or delete it before launching."
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("JSON that is not an object", func(t *testing.T) {
		// Python reaches `settings.items()` and dies with an AttributeError.
		_, err := Load(writeConf(t, "[1, 2, 3]"))
		if !errors.Is(err, kerrors.ErrSettingsInvalidJSON) {
			t.Fatalf("err = %v, want ErrSettingsInvalidJSON", err)
		}
	})

	t.Run("the document null", func(t *testing.T) {
		// Same AttributeError in Python ("'NoneType' object has no attribute
		// 'items'"), but `encoding/json` unmarshals `null` into a map as a
		// silent no-op, so it needs its own guard: without it the file loads
		// as pure defaults and the next Check overwrites the user's settings.
		_, err := Load(writeConf(t, "null"))
		if !errors.Is(err, kerrors.ErrSettingsInvalidJSON) {
			t.Fatalf("err = %v, want ErrSettingsInvalidJSON", err)
		}

		// The neighbouring shape still loads: `{}` is an object.
		if _, err := Load(writeConf(t, "{}")); err != nil {
			t.Errorf("the empty object should load: %v", err)
		}
	})

	t.Run("not UTF-8", func(t *testing.T) {
		// Python decodes the file before parsing it, so a stray \xff is a
		// UnicodeDecodeError — a ValueError, caught as "Not a valid JSON."
		// (probed on the UTF-8 locale). Go's decoder would substitute U+FFFD
		// and rewrite the user's bytes on the next save. DIVERGENCES.md 35.
		_, err := Load(writeConf(t, "{\"image\": \"kat\xffhara\"}"))
		if !errors.Is(err, kerrors.ErrSettingsInvalidJSON) {
			t.Fatalf("err = %v, want ErrSettingsInvalidJSON", err)
		}
		want := "Settings file is not valid: Not a valid JSON. Fix it or delete it before launching."
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("unknown manager_type", func(t *testing.T) {
		// Python raises ClassNotFoundError out of the addon factory, which
		// has no Go representation (ERROR_CODES.md §1.1); the same value
		// produces this message one call later in Python.
		_, err := Load(writeConf(t, `{"manager_type":"podman"}`))
		if !errors.Is(err, kerrors.ErrSettingsManagerType) {
			t.Fatalf("err = %v, want ErrSettingsManagerType", err)
		}
	})

	t.Run("wrong value type", func(t *testing.T) {
		_, err := Load(writeConf(t, `{"open_terminals":"yes"}`))
		if !errors.Is(err, kerrors.ErrSettings) {
			t.Fatalf("err = %v, want a Settings error", err)
		}
		want := "Settings file is not valid: Setting `open_terminals` must be a boolean. " +
			"Fix it or delete it before launching."
		if err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})
}

// TestManagerTypeCaseIsToleratedOnLoad is `str.capitalize()` inside the addon
// factory: a case-mangled manager loads and saves, and only `check()` rejects
// it.
func TestManagerTypeCaseIsToleratedOnLoad(t *testing.T) {
	for _, spelling := range []string{"DOCKER", "Docker", "dOcKeR", "KUBERNETES"} {
		t.Run(spelling, func(t *testing.T) {
			s, err := Load(writeConf(t, `{"manager_type":"`+spelling+`","last_checked":1.0}`))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if s.ManagerType != spelling {
				t.Errorf("manager_type = %q, want it stored verbatim", s.ManagerType)
			}
			if err := s.CheckManager(); !errors.Is(err, kerrors.ErrSettingsManagerType) {
				t.Errorf("CheckManager = %v, want it rejected", err)
			}
		})
	}
}

// TestLoadFromJSONIsLoadFromDict pins that the dict entry point shares the
// disk one's body, addon reset included.
func TestLoadFromJSONIsLoadFromDict(t *testing.T) {
	s := Defaults()
	s.HosthomeMount = true
	s.Image = "kathara/frr"

	if err := s.LoadFromJSON([]byte(`{"device_prefix":"pfx"}`)); err != nil {
		t.Fatalf("LoadFromJSON: %v", err)
	}
	if s.Image != "kathara/frr" {
		t.Errorf("image = %q, want the pre-existing value", s.Image)
	}
	if s.DevicePrefix != "pfx" {
		t.Errorf("device_prefix = %q, want pfx", s.DevicePrefix)
	}
	if s.HosthomeMount {
		t.Error("hosthome_mount survived the addon reset")
	}
}

// ---------------------------------------------------------------------------
// Save, Wipe, paths
// ---------------------------------------------------------------------------

// TestSavePermissions is the chmod 600 of `unix_permissions`.
func TestSavePermissions(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("Python only sets permissions on Linux and macOS")
	}

	pinClock(t, 1785923124.0)
	dir := t.TempDir()
	if err := Defaults().Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// TestSaveCreatesOneDirectoryLevel is `os.mkdir`, which is not recursive: one
// missing level is created, two are an error. Probed against 3.8.3.
func TestSaveCreatesOneDirectoryLevel(t *testing.T) {
	pinClock(t, 1785923124.0)
	root := t.TempDir()

	oneLevel := filepath.Join(root, "config")
	if err := Defaults().Save(oneLevel); err != nil {
		t.Fatalf("one missing level should be created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oneLevel, Filename)); err != nil {
		t.Fatalf("file not written: %v", err)
	}

	twoLevels := filepath.Join(root, "a", "b")
	if err := Defaults().Save(twoLevels); err == nil {
		t.Error("two missing levels should fail, os.mkdir is not recursive")
	}
}

// TestSaveUsesDefaultPathForEmptyDir is NILABILITY.tsv:43 — "" is Python's
// None.
func TestSaveUsesDefaultPathForEmptyDir(t *testing.T) {
	pinClock(t, 1785923124.0)
	path := filepath.Join(t.TempDir(), "config", Filename)
	pinDefaultPath(t, path)

	if err := Defaults().Save(""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default path not written: %v", err)
	}
}

// TestConfPathDoesNotClean pins that the directory argument is joined the way
// `os.path.join` joins it: the path lands in a user-facing error message, so
// it has to be the path the user typed.
func TestConfPathDoesNotClean(t *testing.T) {
	got, err := confPath("/labs/../lab")
	if err != nil {
		t.Fatalf("confPath: %v", err)
	}
	want := "/labs/../lab" + string(os.PathSeparator) + Filename
	if got != want {
		t.Errorf("confPath = %q, want %q", got, want)
	}

	trailing, err := confPath("/labs" + string(os.PathSeparator))
	if err != nil {
		t.Fatalf("confPath: %v", err)
	}
	if want := "/labs" + string(os.PathSeparator) + Filename; trailing != want {
		t.Errorf("confPath = %q, want %q", trailing, want)
	}
}

// TestJoinConfPathAgainstOSPathJoin replays `posixpath.join(dir,
// "kathara.conf")` and `ntpath.join(dir, "kathara.conf")` on the same
// arguments, recorded from the oracle's two path modules.
//
// The row that matters is the bare drive: ntpath adds no separator after
// `C:`, because `C:kathara.conf` names the file in the *current* directory of
// drive C, and a Windows run given `-d C:` has to look where Python looked.
func TestJoinConfPathAgainstOSPathJoin(t *testing.T) {
	for _, tc := range []struct{ dir, posix, nt string }{
		{"C:", "C:/kathara.conf", "C:kathara.conf"},
		{"c:", "c:/kathara.conf", "c:kathara.conf"},
		{"1:", "1:/kathara.conf", "1:kathara.conf"},
		{"foo:", "foo:/kathara.conf", `foo:\kathara.conf`},
		{`C:\`, `C:\/kathara.conf`, `C:\kathara.conf`},
		{"C:/", "C:/kathara.conf", "C:/kathara.conf"},
		{`C:\dir`, `C:\dir/kathara.conf`, `C:\dir\kathara.conf`},
		{`C:\dir\`, `C:\dir\/kathara.conf`, `C:\dir\kathara.conf`},
		{`\\server\share`, `\\server\share/kathara.conf`, `\\server\share\kathara.conf`},
		{`\\server\share\`, `\\server\share\/kathara.conf`, `\\server\share\kathara.conf`},
		{"dir", "dir/kathara.conf", `dir\kathara.conf`},
		{"dir/", "dir/kathara.conf", "dir/kathara.conf"},
		{"", "kathara.conf", "kathara.conf"},
		{"..", "../kathara.conf", `..\kathara.conf`},
		{"/tmp", "/tmp/kathara.conf", `/tmp\kathara.conf`},
		{"//", "//kathara.conf", "//kathara.conf"},
		{"/labs/../lab", "/labs/../lab/kathara.conf", `/labs/../lab\kathara.conf`},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			if got := joinConfPath(tc.dir, false); got != tc.posix {
				t.Errorf("posixpath: joinConfPath(%q) = %q, want %q", tc.dir, got, tc.posix)
			}
			if got := joinConfPath(tc.dir, true); got != tc.nt {
				t.Errorf("ntpath: joinConfPath(%q) = %q, want %q", tc.dir, got, tc.nt)
			}
		})
	}
}

// TestSaveWritesTextModeNewlines is `open(settings_path, 'w')`: text mode with
// the default `newline=None` translates the newlines `json.dumps` produced to
// `os.linesep`, so a 3.8.3-written `kathara.conf` is CRLF on Windows and LF
// everywhere else. Writing LF on Windows would rewrite every existing install's
// file on the first save.
func TestSaveWritesTextModeNewlines(t *testing.T) {
	pinClock(t, 1785923124.0)

	encoded, err := Defaults().Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if bytes.Contains(encoded, []byte("\r")) {
		t.Fatal("Encode itself must stay LF: it feeds the JSON CLI envelope")
	}

	crlf := toTextFile(encoded, "\r\n")
	if want := bytes.ReplaceAll(encoded, []byte("\n"), []byte("\r\n")); !bytes.Equal(crlf, want) {
		t.Error("the Windows translation is not the one text mode performs")
	}
	if !bytes.Equal(toTextFile(encoded, "\n"), encoded) {
		t.Error("the Unix translation must be the identity")
	}

	// And what the host actually writes is its own os.linesep.
	dir := t.TempDir()
	if err := Defaults().Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	written, err := os.ReadFile(filepath.Join(dir, Filename))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !bytes.Equal(written, toTextFile(encoded, lineSep)) {
		t.Errorf("the file on disk does not carry %q line endings", lineSep)
	}
	// The round trip is unaffected either way: the translated bytes are JSON
	// whitespace.
	if _, err := Load(dir); err != nil {
		t.Errorf("the file it wrote does not load back: %v", err)
	}
}

// TestWipe is `Setting.wipe_from_disk`: remove it, and say nothing when it is
// already gone.
func TestWipe(t *testing.T) {
	pinClock(t, 1785923124.0)
	path := filepath.Join(t.TempDir(), Filename)
	pinDefaultPath(t, path)

	if err := Defaults().Save(""); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("file still there: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Errorf("Wipe on a missing file should be silent, got %v", err)
	}
}

// TestWipeLeavesADanglingSymlink is the `os.path.exists` gate: it follows
// symlinks and is False for a broken one, so Python never reaches `os.remove`
// and the link survives. Going straight to `os.Remove` would delete it.
func TestWipeLeavesADanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs a privilege on Windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, Filename)
	pinDefaultPath(t, path)

	if err := os.Symlink(filepath.Join(dir, "nowhere"), path); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := Wipe(); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Errorf("the dangling symlink was removed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Defaults
// ---------------------------------------------------------------------------

// TestDefaults pins DEFAULTS plus both addons' `__init__` bodies, including
// the `now - ONE_WEEK` seeding that makes a fresh install check immediately
// (NILABILITY.tsv:46).
func TestDefaults(t *testing.T) {
	pinClock(t, 2_000_000.5)
	s := Defaults()

	if want := 2_000_000.5 - oneWeek; s.LastChecked != want {
		t.Errorf("last_checked = %v, want %v", s.LastChecked, want)
	}

	for _, tc := range []struct{ name, got, want string }{
		{"image", s.Image, "kathara/base"},
		{"manager_type", s.ManagerType, "docker"},
		{"device_shell", s.DeviceShell, "/bin/bash"},
		{"net_prefix", s.NetPrefix, "kathara"},
		{"device_prefix", s.DevicePrefix, "kathara"},
		{"debug_level", s.DebugLevel, "INFO"},
		{"volume_mount_policy", s.VolumeMountPolicy, "Always"},
		{"image_update_policy", s.ImageUpdatePolicy, "Prompt"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}

	if !s.OpenTerminals || !s.PrintStartupLog || s.EnableIPv6 {
		t.Error("a base boolean default is wrong")
	}
	if s.HosthomeMount || !s.SharedMount || !s.HostShared {
		t.Error("an addon boolean default is wrong")
	}
	if s.SharedCds != NotShared {
		t.Errorf("shared_cds = %d, want 1", s.SharedCds)
	}
	if s.RemoteURL != nil || s.CertPath != nil || s.APIServerURL != nil ||
		s.APIToken != nil || s.DockerConfigJSON != nil {
		t.Error("a nullable default is not nil")
	}
	if s.NetworkPlugin == nil || *s.NetworkPlugin != "kathara/katharanp_vde" {
		t.Errorf("network_plugin = %v, want kathara/katharanp_vde", s.NetworkPlugin)
	}
	if s.ImagePullPolicy == nil || *s.ImagePullPolicy != "IfNotPresent" {
		t.Errorf("image_pull_policy = %v, want IfNotPresent", s.ImagePullPolicy)
	}

	wantTerminal := map[string]string{"linux": "/usr/bin/xterm", "windows": "", "darwin": "Terminal"}[runtime.GOOS]
	if s.Terminal != wantTerminal {
		t.Errorf("terminal = %q, want %q on %s", s.Terminal, wantTerminal, runtime.GOOS)
	}
}

// TestAvailableListsAreOrderedAndCopied pins the declared orders C-5 and
// ORDERING.tsv:112 depend on, and that a caller cannot mutate them.
func TestAvailableListsAreOrderedAndCopied(t *testing.T) {
	managers := AvailableManagers()
	if len(managers) != 2 || managers[0] != "docker" || managers[1] != "kubernetes" {
		t.Errorf("AvailableManagers = %v, want [docker kubernetes]", managers)
	}
	managers[0] = "clobbered"
	if AvailableManagers()[0] != "docker" {
		t.Error("AvailableManagers handed out its backing array")
	}

	levels := AvailableDebugLevels()
	want := []string{"CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "EXCEPTION"}
	if len(levels) != len(want) {
		t.Fatalf("AvailableDebugLevels = %v", levels)
	}
	for i := range want {
		if levels[i] != want[i] {
			t.Errorf("level %d = %q, want %q", i, levels[i], want[i])
		}
	}
}

// TestSharedCollisionDomainsToString is `to_string`, including the implicit
// None for a value the enum does not name.
func TestSharedCollisionDomainsToString(t *testing.T) {
	for _, tc := range []struct {
		in   SharedCollisionDomains
		want string
	}{
		{NotShared, "Not Shared"},
		{SharedBetweenLabs, "Share collision domains between network scenarios"},
		{SharedBetweenUsers, "Share collision domains between users"},
		{0, ""},
		{7, ""},
		{-1, ""},
	} {
		if got := tc.in.ToString(); got != tc.want {
			t.Errorf("SharedCollisionDomains(%d).ToString() = %q, want %q", tc.in, got, tc.want)
		}
	}
}
