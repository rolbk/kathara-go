// This file exports the two `ntpath` primitives that packages outside
// `internal/util` need. They are wrappers rather than renames so that the
// oracle-recorded vectors in ntpath_test.go keep testing the primitives
// themselves.
//
// `settings` is the caller: `Setting.load_from_disk` and `save_to_disk` join
// their directory argument with `os.path.join`, and `ntpath.expanduser` splits
// and joins `%USERPROFILE%`. Both land in user-facing strings — a settings
// path is interpolated into `SettingsNotFoundError` — so neither may go
// through `path/filepath`, which runs Clean and inserts a separator after a
// bare drive letter that ntpath deliberately leaves out.

package util

// NTJoin is `ntpath.join(a, b)`: the Windows path join, without Clean.
//
// The drive handling is the reason it is not `a + "\\" + b`. `join("C:", "x")`
// is the drive-relative `"C:x"` with no separator; a `b` naming a *different*
// drive discards `a` entirely; a rooted `b` keeps `a`'s drive only when it has
// none of its own.
func NTJoin(a, b string) string { return ntJoin(a, b) }

// NTSplit is `ntpath.split(p)`: the head is everything up to the last
// separator with the trailing separators stripped (but the root kept), and the
// tail is the last component, empty for a path that ends in a separator.
//
// `ntpath.dirname` and `ntpath.basename` are exactly this function's two
// halves, which is why they are not offered separately.
func NTSplit(p string) (head, tail string) { return ntSplit(p) }
