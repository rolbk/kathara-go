//go:build windows

package settings

// terminalAvailable is the Windows arm of `Setting.check_terminal`
// (Setting.py:293), which is `lambda: True`.
func terminalAvailable(string) (bool, error) {
	return true, nil
}
