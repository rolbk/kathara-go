// This file is the port of `setting/Setting.py` minus its `check()`, which is
// check.go, and minus the addon indirection, which is addon.go. The singleton,
// the `__slots__` juggling and the `__getattr__`/`__setattr__` delegation to an
// addon object are gone (§0.2 #10, NILABILITY.tsv:41-42): what is left is a
// struct, a loader and a writer.

package settings

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// Filename is `SETTINGS_FILENAME` (Setting.py:34). Every path in this package
// is a *directory* that this name is appended to, which is how Python spells
// it: `load_from_disk("/some/lab")` reads `/some/lab/kathara.conf`.
const Filename = "kathara.conf"

// oneWeek is `ONE_WEEK` (Setting.py:20), the interval between update checks
// and the amount [Defaults] backdates `last_checked` by so that a fresh
// install checks on its first run.
const oneWeek = 604800

// availableDebugLevels is `AVAILABLE_DEBUG_LEVELS` (Setting.py:17), in the
// literal order the settings screen lists them and, more importantly, the
// order they are joined into the error message for an unknown level
// (ORDERING.tsv:112). That message is frozen in `kerrors` as
// [kerrors.ErrSettingsDebugLevel]; TestDebugLevelMessageMatchesRegistry pins
// the two together.
var availableDebugLevels = []string{"CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "EXCEPTION"}

// availableManagers is `AVAILABLE_MANAGERS` (Setting.py:18). It is the manager
// listing order everywhere — the settings screen, `Kathara.get_available_managers_name`
// and `Setting._check_manager` all read this one list (SYNTHESIS C-5), so
// `kathara`'s backend registry takes its declared order from here rather than
// re-deriving it.
var availableManagers = []string{"docker", "kubernetes"}

// AvailableDebugLevels returns `AVAILABLE_DEBUG_LEVELS` in source order.
func AvailableDebugLevels() []string { return append([]string(nil), availableDebugLevels...) }

// AvailableManagers returns `AVAILABLE_MANAGERS` in source order.
func AvailableManagers() []string { return append([]string(nil), availableManagers...) }

// AvailableVolumeMountPolicies is the `volume_mount_policy` menu of
// `CommonOptionsHandler.py:352-379`, in menu order. Nothing in `check()`
// enforces it, but the settings screen never let a user store anything else,
// so `kathara config set` must not either — §3.2 item 4 requires the two entry
// paths to agree, and the value reaches `Machine.get_volumes`'s three-way
// branch where a fourth spelling silently means "never mount".
var availableVolumeMountPolicies = []string{"Always", "Prompt", "Never"}

// AvailableVolumeMountPolicies returns the `volume_mount_policy` menu in menu
// order.
func AvailableVolumeMountPolicies() []string {
	return append([]string(nil), availableVolumeMountPolicies...)
}

// Settings is the whole of `kathara.conf`: the twelve keys `Setting._to_dict`
// writes, plus the keys of both addons.
//
// It is flat where Python has an addon object hanging off `Setting.addons`.
// The addon indirection existed to let `__getattr__` forward unknown attribute
// reads to whichever addon class reflection had picked; with an explicit
// registry (§0.2 #7) it buys nothing, and a flat struct is what makes the
// schema checkable at compile time. Which subset of the addon fields is
// *visible* — read by [Load], written by [Save], listed by [Settings.Keys] —
// is still decided by `manager_type`, exactly as the addon object decided it.
//
// The zero value is not a usable configuration; start from [Defaults] or
// [Load].
type Settings struct {
	// Image is `image`: the default Docker image for a device.
	Image string
	// ManagerType is `manager_type`: which backend runs the scenario, and
	// which addon's keys are part of the file. One of [AvailableManagers].
	ManagerType string
	// Terminal is `terminal`: the external terminal emulator, either a path
	// (Linux), an application name (macOS), or the special value "TMUX".
	Terminal string
	// OpenTerminals is `open_terminals`.
	OpenTerminals bool
	// DeviceShell is `device_shell`.
	DeviceShell string
	// NetPrefix is `net_prefix`, prepended to every collision-domain name.
	NetPrefix string
	// DevicePrefix is `device_prefix`, prepended to every device name.
	DevicePrefix string
	// DebugLevel is `debug_level`. One of [AvailableDebugLevels].
	DebugLevel string
	// PrintStartupLog is `print_startup_log`.
	PrintStartupLog bool
	// EnableIPv6 is `enable_ipv6`.
	EnableIPv6 bool
	// VolumeMountPolicy is `volume_mount_policy`.
	VolumeMountPolicy string
	// LastChecked is `last_checked`: the epoch seconds of the last successful
	// update check, as a float. [Defaults] backdates it by [oneWeek] so a
	// fresh install checks immediately. It stays in the schema with the
	// webhook deferred; see check.go.
	//
	// NILABILITY.tsv:46 proposes an int64 of unix seconds. `time.time()` is a
	// float and `json.dumps` writes every digit of it, so an int64 could not
	// round-trip the `1785923124.0260758` that every existing install's file
	// carries — §0.4 freezes that file. DIVERGENCES.md records the deviation
	// for the register owner.
	LastChecked float64

	// --- Docker addon (`setting/addon/DockerSettingsAddon.py`) ---

	// HosthomeMount is `hosthome_mount`.
	HosthomeMount bool
	// SharedMount is `shared_mount`.
	SharedMount bool
	// ImageUpdatePolicy is `image_update_policy`.
	ImageUpdatePolicy string
	// SharedCds is `shared_cds`.
	SharedCds SharedCollisionDomains
	// RemoteURL is `remote_url`: nil selects the local Docker socket,
	// non-nil a remote TLS client (NILABILITY.tsv:47). The pointer is what
	// keeps `null` and `""` apart, and they are not the same thing — Python
	// tests this field with `is None` and would happily hand `""` to the
	// Docker client as a URL.
	RemoteURL *string
	// CertPath is `cert_path`: the CA certificate for [Settings.RemoteURL],
	// nil for none (NILABILITY.tsv:48).
	CertPath *string
	// NetworkPlugin is `network_plugin`.
	//
	// NILABILITY.tsv:49 proposes a plain string with the default substituted
	// for `null`. That substitution is not what Python does at either end: a
	// `null` in the file is loaded as None, written back as `null`, and
	// interpolated into the driver name as the literal "None"
	// (`DockerPlugin.py:30`, `DockerLink.py:139`). Substituting would rewrite
	// the user's file on the next save, which §0.4 freezes against, so the
	// pointer stays and consumers apply Python's own semantics.
	NetworkPlugin *string

	// --- Kubernetes addon (`setting/addon/KubernetesSettingsAddon.py`) ---

	// APIServerURL is `api_server_url`: nil falls back to kubeconfig or the
	// in-cluster config (NILABILITY.tsv:50).
	APIServerURL *string
	// APIToken is `api_token`, paired with [Settings.APIServerURL].
	APIToken *string
	// HostShared is `host_shared`.
	HostShared bool
	// ImagePullPolicy is `image_pull_policy`. Nullable for the same reason as
	// [Settings.NetworkPlugin].
	ImagePullPolicy *string
	// DockerConfigJSON is `docker_config_json`: the **base64 of a docker
	// config.json's contents**, not a path — the settings screen reads the
	// file the user names and stores the encoding, and `KubernetesSecret`
	// copies the value into a Secret's `data` verbatim. nil and "" both mean
	// "no private-registry secret" (NILABILITY.tsv:52).
	DockerConfigJSON *string
}

// defaultSettings is the union of `DEFAULTS` (Setting.py:22) and the two
// addons' `__init__` bodies, with `last_checked` left at zero: [Defaults] owns
// the clock read so that the constants stay a constant.
func defaultSettings() *Settings {
	remoteURL := (*string)(nil)
	certPath := (*string)(nil)
	networkPlugin := "kathara/katharanp_vde"
	imagePullPolicy := "IfNotPresent"

	return &Settings{
		Image:       "kathara/base",
		ManagerType: "docker",
		// `exec_by_platform(lambda: '/usr/bin/xterm', lambda: '', lambda: 'Terminal')`
		// (Setting.py:25). The macOS value is an application name, not a
		// path, which is why check_terminal branches per platform too.
		//
		// This stays Python's value even though PORT_SPEC §3.3 item 1 calls the
		// built-in multiplexer "the default", because this key's default is a
		// **recorded oracle**: `testdata/conf_roundtrip.json` pins what a
		// partial `kathara.conf` round-trips to through CPython, defaults
		// included, and TestRoundTripAgainstOracle compares it byte for byte.
		// Changing it here would falsify that vector rather than pass it. The
		// Windows default is the empty string, which `term.ModeFor` already
		// reads as the multiplexer, so the platform §3.3 was written for gets
		// the new default for free; Unix opts in with
		// `kathara config set terminal MULTIPLEXER`.
		// PROPOSED-DIVERGENCES.md asks for the ruling that would flip it.
		Terminal:          util.ExecByPlatform(func() string { return "/usr/bin/xterm" }, func() string { return "" }, func() string { return "Terminal" }),
		OpenTerminals:     true,
		DeviceShell:       "/bin/bash",
		NetPrefix:         "kathara",
		DevicePrefix:      "kathara",
		DebugLevel:        "INFO",
		PrintStartupLog:   true,
		EnableIPv6:        false,
		VolumeMountPolicy: "Always",

		HosthomeMount:     false,
		SharedMount:       true,
		ImageUpdatePolicy: "Prompt",
		SharedCds:         NotShared,
		RemoteURL:         remoteURL,
		CertPath:          certPath,
		NetworkPlugin:     &networkPlugin,

		APIServerURL:     nil,
		APIToken:         nil,
		HostShared:       true,
		ImagePullPolicy:  &imagePullPolicy,
		DockerConfigJSON: nil,
	}
}

// Defaults is `Setting.__init__`: every key at its default, with
// `last_checked` set to `time.time() - ONE_WEEK` (Setting.py:74) so that the
// first [Settings.Check] of a fresh install runs its update bookkeeping
// immediately.
func Defaults() *Settings {
	s := defaultSettings()
	s.LastChecked = timeNow() - oneWeek
	return s
}

// timeNow is `time.time()`. It is a variable so that the tests can pin the
// clock; nothing outside this package can reach it.
var timeNow = func() float64 {
	return float64(time.Now().UnixNano()) / float64(time.Second)
}

// defaultPathFn is `DEFAULT_SETTINGS_PATH` (Setting.py:35), computed on demand
// rather than at import: the home lookup can fail, and Python's failure mode
// for that is a traceback before `main` runs.
//
// It is a variable for the same reason [timeNow] is — [Settings.Check] writes
// to this path as a side effect, and a test that could not redirect it would
// have to write to the developer's real configuration file.
var defaultPathFn = func() (string, error) {
	home, err := util.GetCurrentUserHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", Filename), nil
}

// DefaultPath is `DEFAULT_SETTINGS_PATH`: `<home>/.config/kathara.conf`, where
// the home is the sudo-aware one on Linux (`utils.get_current_user_home`), so
// that a sudo-ed run reads and writes the invoking user's configuration and
// not root's.
func DefaultPath() (string, error) { return defaultPathFn() }

// confPath resolves a directory argument the way `os.path.join(path,
// SETTINGS_FILENAME) if path is not None else DEFAULT_SETTINGS_PATH` does
// (Setting.py:100).
//
// The empty string is Python's None (NILABILITY.tsv:43).
func confPath(dir string) (string, error) {
	if dir == "" {
		return DefaultPath()
	}
	return joinConfPath(dir, runtime.GOOS == "windows"), nil
}

// joinConfPath is `os.path.join(dir, SETTINGS_FILENAME)`: posixpath's join off
// Windows, ntpath's on it. The flavour is a parameter rather than a build tag
// so that the Windows rules are exercised by the test suite on any host.
//
// Neither flavour is `filepath.Join`, which runs Clean: the joined path is
// interpolated into [kerrors.NewSettingsNotFound]'s message, so `-d
// /labs/../lab` has to report the path the user typed. ntpath differs on a
// second count that Clean cannot express — `join("C:", "kathara.conf")` is the
// drive-relative `"C:kathara.conf"`, with no separator after a bare drive —
// so the Windows arm goes through the ported `ntpath.join` rather than through
// a hand-rolled rule.
func joinConfPath(dir string, windows bool) string {
	if windows {
		return util.NTJoin(dir, Filename)
	}
	// `posixpath.join`: a separator unless the head is empty or already ends
	// in one. The second component is a plain relative name, so the
	// absolute-b branch cannot apply.
	if dir == "" || strings.HasSuffix(dir, "/") {
		return dir + Filename
	}
	return dir + "/" + Filename
}

// Load is `Setting.load_from_disk` starting from a fresh `Setting()`: the
// defaults, overlaid with whatever the file in dir holds. An empty dir reads
// [DefaultPath].
//
// Use [Settings.LoadFromDisk] instead when the file is a per-scenario override
// layered onto settings that are already loaded; the two differ, and not in a
// way anybody would guess (see that method).
func Load(dir string) (*Settings, error) {
	s := Defaults()
	if err := s.LoadFromDisk(dir); err != nil {
		return nil, err
	}
	return s, nil
}

// LoadFromDisk is `Setting.load_from_disk` (Setting.py:88): it overlays the
// file in dir onto the receiver. An empty dir reads [DefaultPath].
//
// "Overlay" is only half true, and the asymmetry is Python's. The twelve base
// keys are overlaid — a key the file omits keeps the value the receiver
// already had. The addon keys are *reset to their defaults first*, because
// `load_settings_addon()` (Setting.py:114) constructs a brand-new addon object
// before `addons.load(settings)` (Setting.py:115) applies the file's values to
// it. So a per-scenario `kathara.conf` that sets nothing but `image` also
// silently resets `hosthome_mount`, `remote_url`, `network_plugin` and the
// rest of the active addon to their defaults. That is `Command._load_custom_configuration`'s
// real behaviour and it is reproduced here; DIVERGENCES.md records it.
//
// Errors: [kerrors.NewSettingsNotFound] when the file is absent,
// [kerrors.ErrSettingsInvalidJSON] when it does not parse, and the frozen
// "Manager Type not allowed." when `manager_type` names no known addon.
func (s *Settings) LoadFromDisk(dir string) error {
	path, err := confPath(dir)
	if err != nil {
		return err
	}

	// `os.path.exists` is False for every stat failure, not only ENOENT, and
	// the caller of the error is `kathara.py`, which answers a missing file by
	// writing a default one.
	if _, err := os.Stat(path); err != nil {
		return kerrors.NewSettingsNotFound(path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return kerrors.WrapOS(err, err.Error())
	}

	return s.LoadFromJSON(data)
}

// LoadFromJSON is `Setting.load_from_dict` (Setting.py:145) — the same body as
// [Settings.LoadFromDisk] without the file read, and with the same addon-reset
// asymmetry.
//
// Python takes an already-parsed dict; Go takes the document, because a
// `map[string]any` would have thrown away the distinction between a JSON
// number that is an integer and one that is not before this package could see
// it.
func (s *Settings) LoadFromJSON(data []byte) error {
	// `open(settings_path, 'r')` decodes the file with the locale's encoding
	// before `json.load` ever sees it, so on the UTF-8 locale every install
	// ships with, a byte sequence that is not UTF-8 raises `UnicodeDecodeError`
	// — a `ValueError`, caught at Setting.py:110 as "Not a valid JSON.".
	// `encoding/json` instead substitutes U+FFFD inside strings, which would
	// load the file and then rewrite the user's bytes on the next save.
	// DIVERGENCES.md records the check and the locales it does not model.
	if !utf8.Valid(data) {
		return kerrors.ErrSettingsInvalidJSON
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		// Python catches only `ValueError` from `json.load`, so a document
		// that parses but is not an object (`[1,2,3]`) escapes as an
		// `AttributeError` from `settings.items()`. There is nothing portable
		// to reproduce in a traceback; both shapes are reported as the invalid
		// JSON they are. DIVERGENCES.md records it.
		return kerrors.ErrSettingsInvalidJSON
	}
	if raw == nil {
		// The document `null` is the one non-object that `encoding/json`
		// accepts into a map: unmarshalling it is a no-op that leaves the map
		// nil and returns no error. Python dies on it exactly as it dies on
		// `[1,2,3]` (`'NoneType' object has no attribute 'items'`), so it gets
		// the same answer. `{}` decodes to a non-nil empty map and still
		// loads.
		return kerrors.ErrSettingsInvalidJSON
	}

	// Base keys first: `manager_type` decides which addon the rest of the
	// file is read against, and Python's first loop has already applied it by
	// the time `load_settings_addon()` runs.
	for _, d := range baseKeys {
		if value, ok := raw[d.name]; ok {
			if err := d.decode(s, value); err != nil {
				return err
			}
		}
	}

	// `load_settings_addon()`: a fresh addon instance, i.e. its defaults.
	addon, err := s.resetAddon()
	if err != nil {
		return err
	}

	// `self.addons.load(settings)`.
	for _, d := range addon {
		if value, ok := raw[d.name]; ok {
			if err := d.decode(s, value); err != nil {
				return err
			}
		}
	}

	return nil
}

// resetAddon is `Setting.load_settings_addon` (Setting.py:288): it replaces the
// active addon with a brand-new one, which for a flat struct means putting that
// addon's fields back to their defaults. It returns the addon's key table,
// which every caller wants next.
//
// It runs wherever Python constructs a new addon object: on every load, and
// when `manager_type` changes through [Settings.Set] (`cli/ui/setting/utils.py:57`).
// Both are places where the other backend's values are meant to be forgotten.
func (s *Settings) resetAddon() ([]keyDesc, error) {
	addon, err := addonKeys(s.ManagerType)
	if err != nil {
		return nil, err
	}

	fresh := defaultSettings()
	for _, d := range addon {
		d.copy(s, fresh)
	}
	return addon, nil
}

// Save is `Setting.save_to_disk` (Setting.py:117): it writes the receiver to
// `<dir>/kathara.conf`, or to [DefaultPath] when dir is empty.
//
// The three details that are observable, in Python's order:
//
//   - The containing directory is created with `os.mkdir`, which is not
//     recursive. A first run creates `~/.config`; a `-d` pointing two levels
//     into nothing fails with the OS error, and that is the documented
//     behaviour rather than an oversight to paper over with MkdirAll.
//   - The file is written in *text* mode, so the newlines `json.dumps` put
//     between the entries are translated to the platform's line ending on the
//     way out — `\r\n` on Windows, where every 3.8.3-written `kathara.conf`
//     is therefore CRLF. Reading is unaffected: those bytes are JSON
//     whitespace, and Python's universal-newline read translates them back.
//   - The file is written whole, then chmod'ed to 0600 and chowned to the
//     invoking user — the sudo-aware one, so that `sudo kathara` does not
//     leave a root-owned config behind. Both steps are Unix-only
//     (`exec_by_platform(unix_permissions, lambda: None, unix_permissions)`,
//     Setting.py:145).
//   - Keys the file used to hold but the schema does not are gone: the output
//     is built from the schema, never from what was read.
func (s *Settings) Save(dir string) error {
	path, err := confPath(dir)
	if err != nil {
		return err
	}

	dirname := dir
	if dirname == "" {
		dirname = filepath.Dir(path)
	}
	info, err := os.Stat(dirname)
	if err != nil || !info.IsDir() {
		// `if not os.path.isdir(...)`: a stat failure and a non-directory are
		// the same answer, and `os.mkdir` then reports whichever it was.
		if err := os.Mkdir(dirname, 0o777); err != nil {
			return kerrors.WrapOS(err, err.Error())
		}
	}

	// After the mkdir, as in Python: the directory exists even when the
	// serialization is the step that fails.
	data, err := s.Encode()
	if err != nil {
		return err
	}

	// `open(path, 'w')` truncates and creates with 0666 before the umask;
	// the chmod that follows is what actually locks the file down.
	if err := os.WriteFile(path, toTextFile(data, lineSep), 0o666); err != nil {
		return kerrors.WrapOS(err, err.Error())
	}

	return applyOwnership(path)
}

// lineSep is `os.linesep`, the line ending `open(path, 'w')` gives a `\n` with
// the default `newline=None`. It is `\r\n` on Windows and `\n` on the two
// platforms Python's `unix_permissions` arm covers.
var lineSep = func() string {
	if runtime.GOOS == "windows" {
		return "\r\n"
	}
	return "\n"
}()

// toTextFile applies that translation to the bytes [Settings.Encode] produced.
// It is unconditional, like Python's: the writer replaces every `\n`, not only
// the ones not already preceded by a `\r`. Nothing else can reach it —
// `json.dumps` escapes a `\n` inside a value as `\\n`, so the only raw
// newlines in the document are the ones between entries.
//
// Only [Settings.Save] goes through here. [Settings.Encode] and
// [Settings.MarshalJSON] keep their `\n`: they feed the JSON CLI envelope,
// which is a protocol stream and not a text file.
func toTextFile(data []byte, sep string) []byte {
	if sep == "\n" {
		return data
	}
	return bytes.ReplaceAll(data, []byte("\n"), []byte(sep))
}

// Wipe is `Setting.wipe_from_disk` (Setting.py:157), the `kathara wipe
// --settings` half: remove the configuration file if it is there, and say
// nothing if it is not.
//
// The gate is `os.path.exists`, and it is not the same test as "the remove
// failed with ENOENT". `exists` follows symlinks and answers False for *any*
// stat failure — a dangling symlink at the path, an unreadable `~/.config`,
// an ELOOP — and in every one of those cases Python never calls `os.remove`
// and returns silently. Going straight to the remove would delete a dangling
// symlink Python leaves alone, and would report an EACCES Python swallows.
func Wipe() error {
	path, err := DefaultPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	// Past the gate the failure is Python's too: `os.remove` raises, and
	// nothing in `wipe_from_disk` catches it.
	if err := os.Remove(path); err != nil {
		return kerrors.WrapOS(err, err.Error())
	}
	return nil
}
