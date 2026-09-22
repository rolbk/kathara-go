//go:build !linux

package docker

// iptablesVersion is unreachable off Linux: `_xtables_lock_mount`'s Windows arm
// asks the daemon for its kernel version instead and its macOS arm is
// `lambda: ""` (`DockerPlugin.py:162-170`). Python's `Networking` module is
// importable on every platform and would happily run `shutil.which("iptables")`
// on Windows; nothing calls it there, and neither does this.
func iptablesVersion() (string, error) { return "", nil }
