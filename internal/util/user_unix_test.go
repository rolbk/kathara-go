//go:build unix

package util

import (
	"errors"
	"os"
	"strconv"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// TestGetCurrentUserInfoSudoUID pins the three details of the sudo override
// (utils.py:254-261) and, for the failing one, the code it carries. Every case
// needs the process to be root, because the override is guarded by
// `if user_id == 0`.
func TestGetCurrentUserInfoSudoUID(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("the SUDO_UID override only applies when the real uid is 0")
	}

	root, err := GetCurrentUserInfo()
	if err != nil {
		t.Fatalf("GetCurrentUserInfo: %v", err)
	}

	// `if real_user_id:` is Python truthiness, so an exported-but-empty
	// variable leaves the uid at 0 rather than failing.
	t.Setenv("SUDO_UID", "")
	empty, err := GetCurrentUserInfo()
	if err != nil {
		t.Fatalf("GetCurrentUserInfo with an empty SUDO_UID: %v", err)
	}
	if empty.UID != root.UID {
		t.Errorf("an empty SUDO_UID changed the uid to %d", empty.UID)
	}

	// `int()` is CPython's, not a strict decimal parse.
	t.Setenv("SUDO_UID", " "+strconv.Itoa(os.Getuid())+" ")
	if _, err := GetCurrentUserInfo(); err != nil {
		t.Errorf("GetCurrentUserInfo with a padded SUDO_UID: %v", err)
	}

	// A non-numeric one is an uncaught ValueError in Python, which
	// ERROR_CODES.md §1.2 maps to the `Value` code. Its text is CPython's own.
	t.Setenv("SUDO_UID", "abc")
	_, err = GetCurrentUserInfo()
	if err == nil {
		t.Fatal("a non-numeric SUDO_UID must be an error")
	}
	if !errors.Is(err, kerrors.ErrValue) {
		t.Errorf("error = %v, which is not classified Value", err)
	}
	if want := "invalid literal for int() with base 10: 'abc'"; err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
	if !errors.Is(err, ErrPyIntSyntax) {
		t.Error("the cause must stay reachable through errors.Is")
	}
}
