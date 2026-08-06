//go:build !linux

package util

// IsWSLPlatform is utils.is_wsl_platform (utils.py:131) off Linux, where the
// guard on `sys.platform` short-circuits to False before any uname happens.
func IsWSLPlatform() bool { return false }
