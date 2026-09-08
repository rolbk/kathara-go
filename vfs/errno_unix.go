//go:build !windows

package vfs

import (
	"errors"
	"syscall"
)

func isNotEmptyErr(err error) bool { return errors.Is(err, syscall.ENOTEMPTY) }
func isNotDirErr(err error) bool   { return errors.Is(err, syscall.ENOTDIR) }
