//go:build linux

package docker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// iptablesFallbackPaths are the two sbin locations Python probes when
// `shutil.which` comes up empty (`os/Networking.py:203-206`), in that order.
var iptablesFallbackPaths = []string{"/sbin/iptables", "/usr/sbin/iptables"}

// iptablesVersion is `Networking.get_iptables_version`: run `iptables
// --version` and hand back its trimmed output.
func iptablesVersion() (string, error) {
	binary := lookIptables()
	if binary == "" {
		return "", kerrors.ErrIptablesNotFound
	}

	out, _ := exec.Command(binary, "--version").Output()
	return strings.TrimSpace(string(out)), nil
}

// lookIptables is `shutil.which("iptables") or /sbin or /usr/sbin`, returning
// "" for Python's None.
func lookIptables() string {
	searchPath, ok := os.LookupEnv("PATH")
	if !ok {
		// `os.defpath` on posix.
		searchPath = ":/bin:/usr/bin"
	}
	if searchPath != "" {
		seen := make(map[string]struct{})
		for _, dir := range strings.Split(searchPath, string(os.PathListSeparator)) {
			if _, repeat := seen[dir]; repeat {
				continue
			}
			seen[dir] = struct{}{}
			candidate := filepath.Join(dir, "iptables")
			if isExecutableFile(candidate) {
				return candidate
			}
		}
	}

	for _, candidate := range iptablesFallbackPaths {
		// `os.path.exists`, which follows symlinks and does not test the
		// execute bit — Python's fallback is weaker than its `which`.
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// isExecutableFile is `shutil.which`'s `_access_check`: `os.access(fn,
// F_OK|X_OK) and not os.path.isdir(fn)`.
func isExecutableFile(name string) bool {
	info, err := os.Stat(name)
	if err != nil || info.IsDir() {
		return false
	}
	return util.AccessXOK(name)
}
