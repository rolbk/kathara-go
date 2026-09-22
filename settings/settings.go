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

var availableDebugLevels = []string{"CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "EXCEPTION"}

var availableManagers = []string{"docker", "kubernetes"}

// AvailableDebugLevels returns `AVAILABLE_DEBUG_LEVELS` in source order.
func AvailableDebugLevels() []string { return append([]string(nil), availableDebugLevels...) }

// AvailableManagers returns `AVAILABLE_MANAGERS` in source order.
func AvailableManagers() []string { return append([]string(nil), availableManagers...) }

var availableVolumeMountPolicies = []string{"Always", "Prompt", "Never"}

// AvailableVolumeMountPolicies returns the `volume_mount_policy` menu in menu
// order.
func AvailableVolumeMountPolicies() []string {
	return append([]string(nil), availableVolumeMountPolicies...)
}

// Settings is the whole of `kathara.conf`: the twelve keys `Setting._to_dict`
// writes, plus the keys of both addons.
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

	RemoteURL *string

	CertPath *string
	// NetworkPlugin is `network_plugin`.

	NetworkPlugin *string

	// --- Kubernetes addon (`setting/addon/KubernetesSettingsAddon.py`) ---

	APIServerURL *string
	// APIToken is `api_token`, paired with [Settings.APIServerURL].
	APIToken *string
	// HostShared is `host_shared`.
	HostShared bool
	// ImagePullPolicy is `image_pull_policy`. Nullable for the same reason as
	// [Settings.NetworkPlugin].
	ImagePullPolicy *string

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
func confPath(dir string) (string, error) {
	if dir == "" {
		return DefaultPath()
	}
	return joinConfPath(dir, runtime.GOOS == "windows"), nil
}

// joinConfPath is `os.path.join(dir, SETTINGS_FILENAME)`: posixpath's join off
// Windows, ntpath's on it. The flavour is a parameter rather than a build tag
// so that the Windows rules are exercised by the test suite on any host.
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
func Load(dir string) (*Settings, error) {
	s := Defaults()
	if err := s.LoadFromDisk(dir); err != nil {
		return nil, err
	}
	return s, nil
}

// LoadFromDisk is `Setting.load_from_disk` (Setting.py:88): it overlays the
// file in dir onto the receiver. An empty dir reads [DefaultPath].
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
func (s *Settings) LoadFromJSON(data []byte) error {

	if !utf8.Valid(data) {
		return kerrors.ErrSettingsInvalidJSON
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {

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
func toTextFile(data []byte, sep string) []byte {
	if sep == "\n" {
		return data
	}
	return bytes.ReplaceAll(data, []byte("\n"), []byte(sep))
}

// Wipe is `Setting.wipe_from_disk` (Setting.py:157), the `kathara wipe
// --settings` half: remove the configuration file if it is there, and say
// nothing if it is not.
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
