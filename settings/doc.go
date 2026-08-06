// Package settings is the Kathará configuration file: the typed schema of
// `~/.config/kathara.conf`, its loader, its writer and the checks Kathará runs
// against it at startup.
//
// It is the §3.2 rebuild of `setting/`, `foundation/setting/` and `validator/`.
// The rebuild is bounded on purpose (PORT_SPEC §0.4, SYNTHESIS.md §1.5): the
// path, the JSON schema and the serialization are frozen, because every
// existing install has a file in this format and a 1.0 that rewrote it would
// silently break them. What the rebuild replaces is the machinery around the
// file — the singleton, the `__getattr__` delegation to an addon object, the
// reflection-based `SettingsAddonFactory`, and the vendored curses menu that
// owned the validators — not the bytes on disk.
//
// What "frozen" means here, precisely:
//
//   - The file is `<home>/.config/kathara.conf`, with the sudo-aware home on
//     Linux (`internal/util.GetCurrentUserHome`).
//   - `json.dumps(..., indent=True)` is `indent=1`: one space per level, `": "`
//     between key and value, `",\n"` between entries, and **no trailing
//     newline**. [Settings.Encode] emits exactly that.
//   - The file is opened in *text* mode, so on Windows those newlines reach
//     the disk as `\r\n`; every `kathara.conf` 3.8.3 wrote there is CRLF, and
//     [Settings.Save] keeps it that way. [Settings.Encode] and
//     [Settings.MarshalJSON] stay LF: they feed the JSON CLI, not the file.
//   - Key order is the twelve base keys in `Setting._to_dict` literal order,
//     then the active addon's keys in its own `_to_dict` order
//     (ORDERING.tsv:111, :113).
//   - Unknown keys are ignored on load and dropped on save; switching
//     `manager_type` and saving drops the other backend's addon keys. Both are
//     observable and both are reproduced.
//   - `shared_cds` is a plain JSON int, `last_checked` a JSON float written
//     with CPython's `repr`, and strings are escaped the way `ensure_ascii=True`
//     escapes them. `pyjson.go` carries that half.
//
// The type is a plain value, not a singleton (§0.2 #10, NILABILITY.tsv:41).
// Construct one with [Defaults] or [Load]; nothing here is global except the
// frozen tables.
//
// Two things `setting/Setting.py` does are *not* here. The GitHub release
// webhook of `check()` is deferred (§0.3); [Settings.Check] keeps the
// last_checked bookkeeping and the write-to-disk side effect that surrounded it
// (see check.go). And the interactive settings screen is `cmd/kathara`'s
// (PACKAGE_GRAPH.md D-4), which keeps this package free of any TTY dependency;
// the validation it runs is the validation `kathara config set` runs, which is
// the point of §3.2 item 4.
package settings
