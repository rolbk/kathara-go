package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// testApp is an [app] over in-memory streams, which is what PORT_SPEC §0.2 #10
// bought: with the manager, the settings and the dispatcher all fields rather
// than singletons, a whole command runs in a test with no Docker daemon.
type testApp struct {
	*app
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func (t *testApp) stdoutString() string { return t.stdout.String() }
func (t *testApp) stderrString() string { return t.stderr.String() }

// newTestApp builds one, with a manager factory that fails: a test that does
// not set [testApp.newManager] is asserting something that happens before the
// backend is reached, and would otherwise silently try to open a socket.
func newTestApp(t *testing.T) *testApp {
	t.Helper()

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	// INFO is `debug_level`'s shipped default (`setting/Setting.py:30`).
	console := cliout.New(stdout, stderr, cliout.FormatHuman, cliout.LevelInfo)
	console.Width = 80

	a := &app{
		console:  console,
		prompter: &cliout.Prompter{Console: console, In: nil},
		settings: settings.Defaults(),
		// Never the empty string, which is `~/.config/kathara.conf`: a test that
		// exercises `kathara config set` writes a real file, and it must not be
		// the developer's own.
		settingsDir: t.TempDir(),
		dispatcher:  event.New(),
		cwd:         t.TempDir(),
		stdin:       io.LimitReader(bytes.NewReader(nil), 0),
	}
	a.checkSettings = func() error { return nil }
	a.newManager = func(context.Context) (kathara.Manager, error) {
		return nil, errUnexpectedManager
	}
	a.terminalOpener = func(context.Context, *model.Machine) error { return nil }
	// A test process has no terminal, and the production check would say so
	// anyway ([app.stdin] is not an *os.File here). Tests that exercise a
	// bubbletea path set it to true explicitly.
	a.isTTY = func() bool { return false }

	return &testApp{app: a, stdout: stdout, stderr: stderr}
}

// errUnexpectedManager is what a test that never wired a fake gets.
var errUnexpectedManager = errTestf("the test did not provide a manager")

type testError string

func (e testError) Error() string { return string(e) }

func errTestf(msg string) error { return testError(msg) }
