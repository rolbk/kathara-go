//go:build linux

// This file is the accepted carve-out (PACKAGE_GRAPH.md D-6, ruling on
// SYNTHESIS C-3): `os/Networking.get_iptables_version` (`os/Networking.py:194`)
// lives here instead of in a ported `os/` package.
//
// The rest of `os/Networking` is deferred with `lab.ext` (PORT_SPEC §0.3), but
// this one function is on the 1.0 path — `DockerPlugin._xtables_lock_mount`
// calls it on every launch on Linux with the bridge plugin — so the deferral
// boundary as written does not compile without it. A one-function `os/` package
// would outlive its purpose the moment `lab.ext` lands and the rest of the file
// moves into `netns/`, hence the carve-out rather than a package.

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
//
// They are probed because `iptables` normally lives in a directory that is not
// on a non-root user's PATH, and the answer is needed before any privilege is
// raised.
var iptablesFallbackPaths = []string{"/sbin/iptables", "/usr/sbin/iptables"}

// iptablesVersion is `Networking.get_iptables_version`: run `iptables
// --version` and hand back its trimmed output.
//
// The output is not parsed. `_xtables_lock_mount` only asks whether the string
// contains "nf_tables", because the legacy backend takes the `/run/xtables.lock`
// file lock and the nf_tables one does not, so the plugin must bind-mount the
// lock in the first case and must not in the second.
//
// Lookup order is `shutil.which` first, then the two fallbacks
// (`os/Networking.py:201-209`) — a PATH entry wins over `/sbin`.
//
// The command is `os.popen("%s --version")`, i.e. a SHELL invocation whose
// STDERR goes to the parent's and whose exit status is DISCARDED. Both matter:
// a failing `iptables --version` yields "" rather than an error, and "" does
// not contain "nf_tables", so the lock gets mounted. That is reproduced —
// `exec.Cmd.Output`'s error is dropped for the same reason — with the shell
// itself dropped, since the command line is `<path> --version` with no
// metacharacter to interpret and `path` came from a filesystem probe.
//
// Errors: [kerrors.ErrIptablesNotFound] when no binary is found, which is
// Python's `FileNotFoundError("Cannot find \`iptables\` in the host.")` and a
// live row of ERROR_CODES.md §2.
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
//
// It is a local posix-only `which` rather than [os/exec.LookPath] for the
// reason `internal/util`'s own port gives: `LookPath` `Clean`s a relative PATH
// entry and answers differently for an unset PATH. Neither difference is
// reachable for a bare "iptables", but the fallback for an UNSET PATH is —
// `os.defpath` is what a daemon-launched process with no environment gets, and
// `LookPath` would find nothing there.
//
// `internal/util`'s `pyWhich` is unexported and the package is outside this
// stage's edit scope, so the twenty posix-only lines are spelled here;
// PROPOSED-DIVERGENCES.md asks for it to be exported and this copy deleted.
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
//
// [util.AccessXOK] is access(2), which answers for the REAL uid — the same
// choice `internal/util` documents, and the one that matters on the
// setgid-docker install the Linux packages produce.
func isExecutableFile(name string) bool {
	info, err := os.Stat(name)
	if err != nil || info.IsDir() {
		return false
	}
	return util.AccessXOK(name)
}
