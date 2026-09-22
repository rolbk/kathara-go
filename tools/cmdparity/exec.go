package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// CmdResult is the raw outcome of one subprocess invocation.
type CmdResult struct {
	Argv     []string
	ExitCode int
	Stdout   string
	Stderr   string
	TimedOut bool
	Err      error
}

// runProcess executes argv in dir with the deterministic environment defined
// below. A non-zero exit is not an error: the exit code is part of the record.
func runProcess(ctx context.Context, dir, home string, argv []string) CmdResult {
	res := CmdResult{Argv: append([]string(nil), argv...)}
	if len(argv) == 0 {
		res.Err = errors.New("empty argv")
		res.ExitCode = -1
		return res
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv is harness-controlled
	cmd.Dir = dir
	cmd.Env = deterministicEnv(home)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Never inherit stdin: a command that asks for confirmation must see EOF
	// and take the default path rather than block until the timeout.
	cmd.Stdin = strings.NewReader("")

	err := cmd.Run()
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()

	if ctx.Err() != nil {
		res.TimedOut = true
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		res.ExitCode = 0
	case errors.As(err, &ee):
		res.ExitCode = ee.ExitCode()
	default:
		res.ExitCode = -1
		res.Err = err
	}
	return res
}

// consoleWidth is the pinned terminal width. The golden harness pins 80; the
// parity harness pins 200 because `create_lab_table` builds an `expand=True`
// table with eleven columns, and at 80 every cell of it renders as a single
// ellipsis — the widest of the flows would compare nothing at all.
var consoleWidth = "200"

// deterministicEnv is tools/goldenharness/exec.go's environment, with HOME
// overridden so each run gets a private, pinned settings file.
func deterministicEnv(home string) []string {
	keep := map[string]bool{
		"PATH": true, "USER": true, "LOGNAME": true,
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_CONFIG": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if ok && keep[k] {
			env = append(env, kv)
		}
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+home+"/.config",
		"COLUMNS="+consoleWidth,
		"LINES=24",
		"TERM=dumb",
		"NO_COLOR=1",
		"LC_ALL=C.UTF-8",
		"LANG=C.UTF-8",
		"PYTHONIOENCODING=utf-8",
		"PYTHONUNBUFFERED=1",
	)
	return env
}
