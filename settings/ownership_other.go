//go:build !linux && !darwin

package settings

// applyOwnership is the Windows arm of `Setting.save_to_disk`'s permission
// step, `lambda: None` (Setting.py:145), and the implicit None every other
// platform gets from `exec_by_platform`. The file keeps whatever the umask or
// the inherited ACL gave it.
func applyOwnership(string) error { return nil }
