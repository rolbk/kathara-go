package settings

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ---------------------------------------------------------------------------
// Image
// ---------------------------------------------------------------------------

type stubImageChecker struct {
	seen string
	err  error
}

func (c *stubImageChecker) CheckImage(_ context.Context, image string) error {
	c.seen = image
	return c.err
}

// TestCheckImageDefaultsToConfiguredImage is `if not image` (NILABILITY.tsv:44):
// the empty string means "the configured one", not "an image whose name is the
// empty string".
func TestCheckImageDefaultsToConfiguredImage(t *testing.T) {
	s := Defaults()
	s.Image = "kathara/frr"
	checker := &stubImageChecker{}

	if err := s.CheckImage(context.Background(), checker, ""); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if checker.seen != "kathara/frr" {
		t.Errorf("checked %q, want the configured image", checker.seen)
	}

	if err := s.CheckImage(context.Background(), checker, "kathara/quagga"); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if checker.seen != "kathara/quagga" {
		t.Errorf("checked %q, want the argument", checker.seen)
	}
}

// TestCheckImagePropagates: the backend's answer is the answer.
func TestCheckImagePropagates(t *testing.T) {
	want := kerrors.NewDockerImageNotFound("kathara/nope")
	checker := &stubImageChecker{err: want}

	err := Defaults().CheckImage(context.Background(), checker, "kathara/nope")
	if !errors.Is(err, kerrors.ErrDockerImageNotFound) {
		t.Fatalf("CheckImage = %v, want the backend's error", err)
	}
}

// TestIsImageRejection is the `except (ConnectionError, DockerImageNotFoundError,
// InvalidImageArchitectureError)` of `validator/ImageValidator.validate`: those
// three mean "bad image name, ask again", anything else propagates.
func TestIsImageRejection(t *testing.T) {
	rejections := []error{
		kerrors.NewDockerImageNotFound("kathara/nope"),
		kerrors.NewInvalidImageArchitecture("kathara/base", "arm64"),
		kerrors.NewConnectionImagePull("kathara/base"),
	}
	for _, err := range rejections {
		if !IsImageRejection(err) {
			t.Errorf("IsImageRejection(%v) = false, want true", err)
		}
	}

	others := []error{
		nil,
		errors.New("boom"),
		kerrors.NewDaemonConnection(errors.New("no socket")),
		kerrors.ErrSettingsManagerType,
	}
	for _, err := range others {
		if IsImageRejection(err) {
			t.Errorf("IsImageRejection(%v) = true, want false", err)
		}
	}
}

// ---------------------------------------------------------------------------
// Terminal
// ---------------------------------------------------------------------------

// TestCheckTerminalTMUX: the special value bypasses the check on every
// platform (Setting.py:288).
func TestCheckTerminalTMUX(t *testing.T) {
	s := Defaults()
	if err := s.CheckTerminal("TMUX"); err != nil {
		t.Errorf("CheckTerminal(TMUX) = %v", err)
	}

	// It is exact: the check is `terminal == "TMUX"`.
	s.Terminal = "TMUX"
	if err := s.CheckTerminal(""); err != nil {
		t.Errorf("CheckTerminal(\"\") with terminal=TMUX = %v", err)
	}
}

// TestCheckTerminalUnix is the `check_unix` arm: an existing, executable,
// regular file and nothing else. Probed against 3.8.3.
func TestCheckTerminalUnix(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the executable-file check is the Linux arm")
	}

	dir := t.TempDir()

	executable := filepath.Join(dir, "term")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := Defaults()

	if err := s.CheckTerminal(executable); err != nil {
		t.Errorf("an executable file was rejected: %v", err)
	}

	for _, bad := range []string{plain, dir, filepath.Join(dir, "missing"), "xterm", ""} {
		err := s.CheckTerminal(bad)
		if err == nil {
			t.Errorf("CheckTerminal(%q) accepted an unusable terminal", bad)
			continue
		}
		var invalid *kerrors.SettingsInvalidError
		if !errors.As(err, &invalid) {
			t.Errorf("CheckTerminal(%q) = %v, want *SettingsInvalidError", bad, err)
		}
	}
}

// TestCheckTerminalMessageNamesTheResolvedValue: an empty argument falls back
// to the configured terminal, and the message quotes *that*, not "" — probed
// against 3.8.3.
func TestCheckTerminalMessageNamesTheResolvedValue(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("depends on the Linux arm rejecting a missing file")
	}

	s := Defaults()
	s.Terminal = "/nonexistent/emulator"

	err := s.CheckTerminal("")
	want := "Settings file is not valid: Terminal Emulator `/nonexistent/emulator` not valid! " +
		"Install it before using it. Fix it or delete it before launching."
	if err == nil || err.Error() != want {
		t.Errorf("message = %v, want %q", err, want)
	}
}

// ---------------------------------------------------------------------------
// docker config.json
// ---------------------------------------------------------------------------

// TestValidateDockerConfigJSON is `validator/DockerConfigJsonValidator.validate`:
// open the expanded path, parse it, and answer with the class of whichever
// step failed. Probed against 3.8.3.
func TestValidateDockerConfigJSON(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "config.json")
	if err := os.WriteFile(good, []byte(`{"auths": {}}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("nope"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := ValidateDockerConfigJSON(good); err != nil {
		t.Errorf("a valid config was rejected: %v", err)
	}

	if err := ValidateDockerConfigJSON(bad); !errors.Is(err, kerrors.ErrValue) {
		t.Errorf("bad JSON = %v, want the Value class (Python's ValueError)", err)
	}

	if err := ValidateDockerConfigJSON(filepath.Join(dir, "missing.json")); !errors.Is(err, kerrors.ErrOS) {
		t.Errorf("missing file = %v, want the OS class (Python's OSError)", err)
	}

	// A directory is Python's `IsADirectoryError`, an OSError subclass.
	if err := ValidateDockerConfigJSON(dir); !errors.Is(err, kerrors.ErrOS) {
		t.Errorf("directory = %v, want the OS class", err)
	}

	// The tilde is expanded, which is what makes DefaultDockerConfigJSONPath
	// a usable answer.
	t.Setenv("HOME", dir)
	if err := ValidateDockerConfigJSON("~/config.json"); err != nil {
		t.Errorf("~ was not expanded: %v", err)
	}
}

// TestExpandUserAgainstOracle replays `posixpath.expanduser`
// (testdata/expanduser.json), including the forms that return the path
// unchanged.
func TestExpandUserAgainstOracle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("recorded from posixpath")
	}

	type row struct {
		In      string `json:"in"`
		HomeSet string `json:"home_set"`
		Out     string `json:"out"`
	}
	rows := loadJSONFixture[[]row](t, "expanduser.json")

	for _, r := range rows {
		want := r.Out
		// `~user` rows resolve through the passwd database, exactly as
		// posixpath does — but the fixture was recorded on Linux, where
		// root's home is /root; on macOS it is /var/root. Re-derive the
		// platform's own answer instead of skipping the row.
		if name, rest, ok := namedUserRow(r.In); ok {
			u, err := user.Lookup(name)
			if err != nil {
				t.Skipf("user %q not in this platform's passwd db", name)
			}
			want = u.HomeDir + rest
		}
		t.Setenv("HOME", r.HomeSet)
		if got := expandUser(r.In); got != want {
			t.Errorf("expandUser(%q) with HOME=%q = %q, want %q", r.In, r.HomeSet, got, want)
		}
	}
}

// namedUserRow reports whether in is a `~user[/rest]` form, returning the user
// and the untouched remainder.
func namedUserRow(in string) (name, rest string, ok bool) {
	if !strings.HasPrefix(in, "~") || in == "~" || strings.HasPrefix(in, "~/") {
		return "", "", false
	}
	name = in[1:]
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name, rest = name[:i], name[i:]
	}
	return name, rest, true
}

// TestExpandUserPrefersHOME pins the detail that makes `~` mean root's home
// under sudo, which is the opposite of what this package does for
// `kathara.conf` — and is Python's behaviour on both counts.
func TestExpandUserPrefersHOME(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("posixpath only")
	}
	t.Setenv("HOME", "/somewhere/else")
	if got := expandUser("~/x"); got != "/somewhere/else/x" {
		t.Errorf("expandUser(~/x) = %q, want /somewhere/else/x", got)
	}
}
