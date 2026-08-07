//go:build !linux

// The macOS and Windows arms of `utils.exec_by_platform(…, lambda: None,
// lambda: None)`: Python passes a no-op for both, so this file is the faithful
// port of two lambdas that do nothing.

package main

// dropEffectivePrivileges does nothing outside Linux, which is what
// `src/kathara.py:134` asks for on macOS and Windows.
func dropEffectivePrivileges() {}
