package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"

	"github.com/KatharaFramework/kathara-go/event"
	// Imported for effect, and it has to be this package that does it: an
	// `init()` in `main` runs *after* every imported package's, which is too
	// late — see the package doc for what bubbletea does in its own init and
	// what it costs the thirteen commands that are not a terminal UI.
	_ "github.com/KatharaFramework/kathara-go/internal/charmguard"
	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// run is the whole entrypoint, parameterised on its streams so that the
// dispatch tests can drive it without a process.
func run(argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// `Setting.get_instance().load_from_disk()` and, on
	// `SettingsNotFoundError`, `save_to_disk()`: a first run writes the
	// defaults before anything else happens.
	cfg, err := settings.Load("")
	if err != nil {
		if !errors.Is(err, kerrors.ErrSettingsNotFound) {
			// Python's `load_from_disk` raises `SettingsError` for an
			// unparseable file, which is NOT caught here — it escapes the
			// module-level call and prints a traceback. This implementation reports it
			// the way the catch-all would and exits 1.
			renderStartupError(stdout, err)
			return 1
		}
		cfg = settings.Defaults()
		if saveErr := cfg.Save(""); saveErr != nil {
			renderStartupError(stdout, saveErr)
			return 1
		}
	}

	// `logging.basicConfig(level=debug_level …)`, with EXCEPTION mapped to
	// DEBUG for the logger and remembered for the catch-all.
	console := cliout.New(stdout, stderr, cliout.FormatHuman, cliout.ParseLevel(cfg.DebugLevel))
	console.Traceback = cfg.DebugLevel == "EXCEPTION"
	// The Go packages log through `log/slog`; routing the default logger
	// here is what makes `labfile`'s duplicate-meta warning land on stdout with
	// the level gutter, as Python's `logging.warning` does.
	slog.SetDefault(slog.New(cliout.NewSlogHandler(console)))

	cwd, err := os.Getwd()
	if err != nil {
		renderStartupError(stdout, err)
		return 1
	}

	dispatcher := event.New()
	a := &app{
		console:    console,
		prompter:   &cliout.Prompter{Console: console, In: stdin},
		settings:   cfg,
		dispatcher: dispatcher,
		cwd:        cwd,
		stdin:      stdin,
	}
	a.checkSettings = a.settings.Check
	a.newManager = func(ctx context.Context) (kathara.Manager, error) {
		return kathara.NewClient(ctx, a.settings,
			kathara.WithRegistry(backendRegistry()),
			kathara.WithDispatcher(a.dispatcher))
	}
	a.terminalOpener = a.openTerminal
	a.isTTY = a.isInteractiveTTY

	// `register_cli_events()`, before dispatch, so that a backend constructed
	// inside a command already has its subscribers.
	a.registerEvents()

	dropEffectivePrivileges()

	// SIGINT only.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	code := dispatch(ctx, a, argv)
	// The interrupt is read off the context itself rather than off a flag a
	// goroutine sets: the goroutine races `finish` and can lose.
	return a.finish(code, ctx.Err() != nil)
}

// finish is the entrypoint's two exit paths: the `except KeyboardInterrupt`
// arm and the ordinary one. Both run `unregister_cli_events()` first, which is
// what stops the progress bars (`src/kathara.py:61,66,75,83,87,93,100,107`).
func (a *app) finish(code int, wasInterrupted bool) int {
	if wasInterrupted {
		if !interruptExempt[a.commandName] {
			a.console.Warning("You interrupted Kathara during a command. The system may be in an " +
				"inconsistent state! If you encounter any problem please run `kathara wipe`.")
		}
		a.console.EmitInterrupted()
		a.unregisterEvents()
		return 0
	}
	a.unregisterEvents()
	return code
}

func renderStartupError(stdout io.Writer, err error) {
	c := cliout.New(stdout, io.Discard, cliout.FormatHuman, cliout.LevelDebug)
	c.Log(cliout.LevelCritical, "(%s) %s", cliout.HumanLabelOf(err), err.Error())
}
