// This file is the dispatch half of `cli/ui/utils.open_machine_terminal`: the
// decision Python spelled as `if terminal == "TMUX" … else <spawn an emulator>`
// inside three per-platform closures.

// What the stored default is, is a separate question from what this file maps.

package term

// The two reserved `terminal` values. Anything else names a program (Linux) or
// an application (macOS), or is ignored entirely (Windows, where Python spawns
// PowerShell without reading the setting at all).
const (
	TerminalTMUX = "TMUX"

	TerminalMultiplexer = "MULTIPLEXER"
)

// Mode is which terminal integration a `terminal` setting value selects.
type Mode int

const (
	// ModeMultiplexer is the built-in bubbletea multiplexer, the default.
	ModeMultiplexer Mode = iota
	// ModeTmux drives the tmux binary through its documented CLI.
	ModeTmux
	// ModeExternal spawns an OS terminal emulator per device, matching the
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
