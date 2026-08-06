//go:build windows

package settings

// terminalAvailable is the Windows arm of `Setting.check_terminal`
// (Setting.py:293), which is `lambda: True`.
//
// Nothing is checked because nothing is launched by name: the Windows terminal
// path spawns `powershell.exe -Command …` with `CREATE_NEW_CONSOLE`
// (`cli/ui/utils.py:167`) and never reads the `terminal` setting at all. The
// value is still stored, and still round-trips through the file.
func terminalAvailable(string) (bool, error) {
	return true, nil
}
