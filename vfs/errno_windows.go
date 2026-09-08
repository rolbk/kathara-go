//go:build windows

package vfs

import (
	"errors"
	"syscall"
)

// CPython translates winerror to errno before choosing the OSError subclass
// (PC/errmap.h), which is what keeps `NotADirectoryError` and the
// directory-not-empty OSError stable across platforms. Go leaves the raw
// winerror in place, so the same translation happens here for the two codes
// the vfs classifier depends on.
const (
	errorDirNotEmpty = syscall.Errno(145) // ERROR_DIR_NOT_EMPTY -> ENOTEMPTY
	errorDirectory   = syscall.Errno(267) // ERROR_DIRECTORY     -> ENOTDIR
)

func isNotEmptyErr(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, errorDirNotEmpty)
}

func isNotDirErr(err error) bool {
	return errors.Is(err, syscall.ENOTDIR) || errors.Is(err, errorDirectory)
}
