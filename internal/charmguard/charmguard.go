// Package charmguard keeps one side effect of linking bubbletea out of the
// fourteen commands that are not a terminal UI.
package charmguard

import "github.com/charmbracelet/lipgloss"

func init() {
	lipgloss.SetHasDarkBackground(true)
}
