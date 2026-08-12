// This file is `foundation/cli/command/Command.py` — the two helpers every
// lab-path-taking command shares — plus the process-wide state the §0.2 #10
// singleton removal turned into a value: the settings, the event dispatcher,
// the console, and the manager the commands run against.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/cliout"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// app is everything a command needs that Python read off a singleton.
//
// `Setting.get_instance()`, `EventDispatcher.get_instance()` and
// `Kathara.get_instance()` are all fields here (PORT_SPEC §0.2 #10), which is
// what lets `cmd/kathara`'s tests run a whole command against a fake manager
// with no Docker daemon in sight.
type app struct {
	// console is `Command.console` and the logging handler at once.
	console *cliout.Console
	// prompter reads the two confirmation prompts.
	prompter *cliout.Prompter
	// settings is the loaded `kathara.conf`. Commands mutate it — `lstart`
	// overrides `open_terminals` and `terminal`, `vstart` also `device_shell`
	// — exactly as Python mutates its singleton.
	settings *settings.Settings
	// dispatcher is the event bus the backends announce into.
	dispatcher *event.Dispatcher
	// settingsDir is the directory `kathara config` and the settings form save
	// into, i.e. the `dir` argument of [settings.Settings.Save]. It is empty in
	// production, which means [settings.DefaultPath] — `~/.config/kathara.conf`,
	// the frozen location (§3.2 item 1). It is a field only so that a test can
	// exercise the write path without touching the developer's own file.
	settingsDir string
	// cwd is `os.getcwd()`, the fallback lab path.
	cwd string
	// stdin is where prompts and `--from-archive -` read from.
	stdin io.Reader

	// commandName is the dispatched command word, which the Ctrl-C handler
	// needs for the `src/kathara.py:97` warning whitelist.
	commandName string
	// rawArgs is `sys.argv[2:]`, kept because `lrestart` re-parses it with
	// *lstart's* parser (`LrestartCommand.py:140`) — the re-parse that makes
	// `--xterm` fail after the clean has already run.
	rawArgs []string

	// suppressEmit stops a nested command from writing its own envelope.
	// `lrestart` runs `lclean` and `lstart` in-process and emits ONE combined
	// object at the end (JSON_CLI_CONTRACT.md §3.3).
	suppressEmit bool
	// lastLcleanResult and lastLstartResult are how `lrestart` collects the
	// two phase envelopes it composes.
	lastLcleanResult cliout.LcleanResult
	lastLstartResult cliout.LstartResult

	// newManager builds the backend. It is a field so that a test can supply
	// one; the production value calls [kathara.NewClient].
	newManager func(ctx context.Context) (kathara.Manager, error)

	// checkSettings is `Setting.get_instance().check()`, the startup pass
	// step 3 of CLI_SURFACE.md §0.2 runs for every command whose name does not
	// contain "settings". It is a field because the real one writes to
	// `~/.config/kathara.conf` — the weekly `last_checked` stamp — which a
	// dispatch test must not do to the machine it runs on.
	checkSettings func() error

	// handlers holds the CLI's event subscriptions, so that
	// [app.unregisterEvents] can tear them down on every exit path the way
	// `unregister_cli_events()` does.
	handlers *cliHandlers

	managerOnce sync.Once
	managerVal  kathara.Manager
	managerErr  error

	// terminalOpener is the `HandleMachineTerminal.run` half that spawns a
	// window. It is a field for the same reason newManager is.
	terminalOpener func(ctx context.Context, machine *model.Machine) error

	// isTTY reports whether this process's console is interactive both ways,
	// which is the precondition for anything bubbletea draws: the built-in
	// multiplexer takes over stdin and stdout, and neither a pipe nor a
	// redirect can give it back. It is a field so that a test can drive those
	// paths without a pseudo-terminal; production is [app.isInteractiveTTY].
	isTTY func() bool

	// muxDevices are the devices the built-in multiplexer will show
	// (PORT_SPEC §3.3 item 1). Python opens one OS window per device as each
	// one deploys; the multiplexer is one window for the whole scenario, so
	// the per-device event enqueues here and [app.runPendingTerminals] opens
	// the window once, after the command has emitted its result. Empty for
	// every other terminal mode and for `--noterminals`.
	muxDevices []*model.Machine

	// opCtx is the operation context, stored because the event bus that calls
	// [app.openMachineTerminals] is `EventDispatcher`-shaped and carries none:
	// a subscriber signature with a ctx would be a change to `event/`, which
	// this package does not own. It is set once per dispatch and is what keeps
	// the terminal driver off context.Background() (PORT_SPEC §0.2 #11).
	opCtx context.Context
}

// manager builds the backend once and caches it.
//
// The construction is eager in the same sense Python's was: `Kathara.__init__`
// opened the Docker client and checked the network plugin, so a daemon that is
// not running failed at the first `Kathara.get_instance()` and not at the first
// operation (analysis/manager-foundation.md §7 gotcha 9). What moved is *when*
// that first call happens: Python's is inside the command body, and so is this.
func (a *app) manager(ctx context.Context) (kathara.Manager, error) {
	a.managerOnce.Do(func() {
		a.managerVal, a.managerErr = a.newManager(ctx)
	})
	return a.managerVal, a.managerErr
}

// resolveLabPath is the idiom every lab-path-taking command opens with
// (`LstartCommand.py:150-151` and six siblings): strip every quote character
// from the `-d` value, fall back to the working directory, then resolve.
//
// The quote stripping is a `str.replace` over the whole string, not a
// surrounding-quote trim, so a directory whose name contains an apostrophe
// cannot be reached with `-d`. That is Python's behaviour and it is reproduced.
func (a *app) resolveLabPath(directory string) (string, error) {
	labPath := a.cwd
	if directory != "" {
		labPath = strings.NewReplacer(`"`, "", "'", "").Replace(directory)
	}
	return util.GetAbsolutePath(labPath)
}

// loadCustomConfiguration is `Command._load_custom_configuration`: a
// `kathara.conf` sitting in the scenario directory is layered onto the settings
// before the scenario is parsed.
//
// The layering is asymmetric and the asymmetry is Python's: the twelve base
// keys are overlaid, but the active addon's keys are *reset to their defaults*
// first, because `load_settings_addon()` builds a fresh addon object before the
// file's values are applied. A per-scenario file that sets only `image`
// therefore also resets `hosthome_mount`, `remote_url` and the rest.
// `settings.Settings.LoadFromDisk` carries that whole behaviour.
func (a *app) loadCustomConfiguration(labPath string) error {
	custom := filepath.Join(labPath, settings.Filename)
	if _, err := os.Stat(custom); err != nil {
		return nil
	}
	a.console.Info("Loading custom kathara.conf file from path `%s`...", custom)
	return a.settings.LoadFromDisk(labPath)
}

// defaults are the four settings values `model` falls back to (OQ-4). They are
// re-read per lab because `loadCustomConfiguration` can have changed them.
func (a *app) defaults() model.Defaults {
	return kathara.DefaultsFrom(a.settings)
}

// registerFormat declares `--format` on a command and records which values it
// accepts (JSON_CLI_CONTRACT.md §1.1). A command that takes no `--format` at
// all — `connect`, `settings` — simply does not call this, so the flag is
// unknown there and any use of it is a usage error, exit 2.
func registerFormat(p *parser, streaming bool) {
	help := "Output format: human or json."
	if streaming {
		help = "Output format: human, json or jsonl."
	}
	p.Flags().String("format", string(cliout.FormatHuman), help)
	p.meta("format", "FORMAT")
}

// applyFormat validates `--format` and switches the console over to it.
func (a *app) applyFormat(spec *commandSpec) error {
	flag := spec.Cmd.Flags().Lookup("format")
	if flag == nil {
		return nil
	}
	value := cliout.Format(flag.Value.String())
	allowed := map[cliout.Format]bool{cliout.FormatHuman: true, cliout.FormatJSON: true}
	if spec.Streaming {
		allowed[cliout.FormatJSONL] = true
	}
	if !allowed[value] {
		return fmt.Errorf("argument --format: invalid choice: %q", string(value))
	}
	a.console.Format = value
	return nil
}

// asLabObject builds the JSON_CLI_CONTRACT.md §3.0.1 `lab` object from a parsed
// scenario. withPath is false for the v-family and for archive deploys, whose
// scenario has no stable directory.
func asLabObject(lab *model.Lab, withPath bool) cliout.Lab {
	obj := cliout.Lab{Hash: lab.Hash}
	if lab.HasName() {
		obj.Name = cliout.Str(lab.Name())
	}
	if withPath {
		if path, ok := lab.FSPath(); ok {
			obj.Path = cliout.Str(path)
		}
	}
	obj.Description = lab.Description
	obj.Version = lab.Version
	obj.Author = lab.Author
	obj.Email = lab.Email
	obj.Web = lab.Web
	return obj
}

// vlabName is the fixed single-device scenario the whole v-family shares
// (`VstartCommand.py:213`, `VcleanCommand.py:40`, `VconfigCommand.py:58`,
// `ConnectCommand.py:64`, `ExecCommand.py:81`).
const vlabName = "kathara_vlab"

// newVlab builds it. §0.2 #3 collapses the v-commands onto the lab code path,
// and this constructor is the whole of what "a one-device in-memory Lab" means.
func (a *app) newVlab() *model.Lab { return model.NewLab(vlabName, a.defaults()) }
