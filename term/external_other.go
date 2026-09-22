//go:build !linux && !darwin && !windows

package term

import "context"

// OpenExternal on a platform Python's `exec_by_platform` does not name.
func OpenExternal(context.Context, Request) error {
	return ErrExternalUnsupported
}
