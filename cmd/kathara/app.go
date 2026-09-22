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
	dispatcher  *event.Dispatcher
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

	suppressEmit bool
	// lastLcleanResult and lastLstartResult are how `lrestart` collects the
	// two phase envelopes it composes.
	lastLcleanResult cliout.LcleanResult
	lastLstartResult cliout.LstartResult

	// newManager builds the backend. It is a field so that a test can supply
	// one; the production value calls [kathara.NewClient].
	newManager func(ctx context.Context) (kathara.Manager, error)

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

	muxDevices []*model.Machine

	opCtx context.Context
}

// manager builds the backend once and caches it.
func (a *app) manager(ctx context.Context) (kathara.Manager, error) {
	a.managerOnce.Do(func() {
		a.managerVal, a.managerErr = a.newManager(ctx)
	})
	return a.managerVal, a.managerErr
}

// resolveLabPath is the idiom every lab-path-taking command opens with
// (`LstartCommand.py:150-151` and six siblings): strip every quote character
// from the `-d` value, fall back to the working directory, then resolve.
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
func (a *app) loadCustomConfiguration(labPath string) error {
	custom := filepath.Join(labPath, settings.Filename)
	if _, err := os.Stat(custom); err != nil {
		return nil
	}
	a.console.Info("Loading custom kathara.conf file from path `%s`...", custom)
	return a.settings.LoadFromDisk(labPath)
}

// defaults are the four settings values the model falls back to. They are
// re-read per lab because `loadCustomConfiguration` can have changed them.
func (a *app) defaults() model.Defaults {
	return kathara.DefaultsFrom(a.settings)
}

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

func (a *app) newVlab() *model.Lab { return model.NewLab(vlabName, a.defaults()) }
