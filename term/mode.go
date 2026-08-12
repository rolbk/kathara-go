// This file is the dispatch half of `cli/ui/utils.open_machine_terminal`: the
// decision Python spelled as `if terminal == "TMUX" … else <spawn an emulator>`
// inside three per-platform closures.
//
// PORT_SPEC §3.3 turns that binary choice into three modes in priority order —
// built-in multiplexer, tmux, external emulator — and calls the setting that
// selects them `terminal_mode`. There is no such key: §3.2 item 1 freezes the
// configuration schema ("same JSON schema, same path. Existing installs must
// keep working. This is not a sanctioned place to innovate"), so the mode is
// carried by the existing `terminal` key, whose value space Python had already
// made a mode selector by reserving the literal "TMUX" in it
// (`setting/Setting.py:288`). The rebuild reserves one more literal,
// "MULTIPLEXER", and treats the empty value — Windows's stock default — the
// same way.
//
// What the stored default is, is a separate question from what this file maps.
// `settings.Defaults` keeps Python's per-platform value — `/usr/bin/xterm` on
// Linux, `Terminal` on macOS, `""` on Windows — because that default is pinned
// by a recorded CPython oracle (`settings/testdata/conf_roundtrip.json`), so
// Windows gets the multiplexer for free through the empty value while Unix
// opts in with `kathara config set terminal MULTIPLEXER`. PROPOSED-DIVERGENCES.md
// ("Making the built-in multiplexer the *stored* default collides with a
// recorded oracle") holds the ruling that would flip the other two, and
// DIVERGENCES.md item 107 records where it stands.

package term

// The two reserved `terminal` values. Anything else names a program (Linux) or
// an application (macOS), or is ignored entirely (Windows, where Python spawns
// PowerShell without reading the setting at all).
const (
	// TerminalTMUX selects the tmux backend (§3.3 item 2, `term/tmuxdrv`).
	// The spelling is Python's, and `Setting.check_terminal` has always
	// short-circuited on it.
	TerminalTMUX = "TMUX"

	// TerminalMultiplexer selects the built-in multiplexer (§3.3 item 1).
	TerminalMultiplexer = "MULTIPLEXER"
)

// Mode is which terminal integration a `terminal` setting value selects.
type Mode int

const (
	// ModeMultiplexer is the built-in bubbletea multiplexer, the default.
	ModeMultiplexer Mode = iota
	// ModeTmux drives the tmux binary through its documented CLI.
	ModeTmux
	// ModeExternal spawns an OS terminal emulator per device, the ported
	// 3.8.3 behaviour.
	ModeExternal
)

func (m Mode) String() string {
	switch m {
	case ModeTmux:
		return "tmux"
	case ModeExternal:
		return "external"
	default:
		return "multiplexer"
	}
}

// ModeFor maps a `terminal` setting value to its mode.
//
// The empty value is the multiplexer because it is Windows's stock default —
// `Setting.py` gives `terminal` the empty string there — and because the Unix
// arms of `Setting.check_terminal` reject it, so no Unix install can be
// carrying one by accident.
func ModeFor(terminal string) Mode {
	switch terminal {
	case TerminalTMUX:
		return ModeTmux
	case TerminalMultiplexer, "":
		return ModeMultiplexer
	default:
		return ModeExternal
	}
}
