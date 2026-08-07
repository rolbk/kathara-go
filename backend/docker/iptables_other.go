//go:build !linux

// The non-Linux half of the D-6 carve-out (PACKAGE_GRAPH.md §4:
// "`iptables_linux.go` / `iptables_other.go` — carve-out `get_iptables_version`
// + xtables lock mount / no-op (never called)").

package docker

// iptablesVersion is unreachable off Linux: `_xtables_lock_mount`'s Windows arm
// asks the daemon for its kernel version instead and its macOS arm is
// `lambda: ""` (`DockerPlugin.py:162-170`). Python's `Networking` module is
// importable on every platform and would happily run `shutil.which("iptables")`
// on Windows; nothing calls it there, and neither does this.
//
// It answers the empty version rather than an error, which is the same thing
// the Linux arm produces for an `iptables --version` that fails: "" does not
// contain "nf_tables", so the caller would mount the lock. No caller reaches
// it, so neither answer is observable — the empty string is the one that
// cannot introduce a platform-specific failure if one ever does.
func iptablesVersion() (string, error) { return "", nil }
