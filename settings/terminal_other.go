//go:build !linux && !darwin && !windows

package settings

// terminalAvailable is what `exec_by_platform` does on a platform that is
// none of the three it names (utils.py:138): it falls off the end of the
// dispatch and returns None. `Setting.check_terminal` tests that with
// `if not ...`, so the terminal is reported invalid and every emulator name is
// rejected.
//
// The arm exists so that a build for such a platform keeps Python's answer
// instead of silently inheriting the Unix check, which would accept terminals
// Python refuses.
func terminalAvailable(string) (bool, error) {
	return false, nil
}
