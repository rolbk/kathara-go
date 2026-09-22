//go:build windows

package tmuxdrv

import (
	"context"
	"testing"
)

func startAttachHelper(t *testing.T, _ context.Context, _ string, _ []string, _ string) *attachHelper {
	t.Helper()
	t.Skip("tmux attach integration tests require Unix pty support")
	return nil
}
