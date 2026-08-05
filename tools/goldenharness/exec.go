package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// runProcess executes argv in dir with the harness's deterministic environment
// and captures both streams. A non-zero exit is not an error: the exit code is
// part of the golden.
func runProcess(ctx context.Context, dir string, extraEnv []string, argv []string) CmdResult {
	res := CmdResult{Argv: append([]string(nil), argv...)}
	if len(argv) == 0 {
		res.Err = errors.New("empty argv")
		res.ExitCode = -1
		return res
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) // #nosec G204 -- argv is harness-controlled
	cmd.Dir = dir
	cmd.Env = append(deterministicEnv(), extraEnv...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Never inherit the harness's stdin: a lab that asks for confirmation must
	// see EOF and take the default path rather than block until the timeout.
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

// deterministicEnv builds the environment every child process runs under.
// Terminal geometry is pinned because rich lays panels out to the console
// width, and colour is disabled so the recording does not depend on whether a
// TTY happened to be attached.
func deterministicEnv() []string {
	keep := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "LOGNAME": true,
		"DOCKER_HOST": true, "DOCKER_CONTEXT": true, "DOCKER_CONFIG": true,
		"XDG_CONFIG_HOME": true, "SSL_CERT_FILE": true, "SSL_CERT_DIR": true,
	}
	var env []string
	for _, kv := range os.Environ() {
		k, _, ok := strings.Cut(kv, "=")
		if ok && keep[k] {
			env = append(env, kv)
		}
	}
	env = append(env,
		"COLUMNS=80",
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

// ErrCommandFailed is returned by mustRun helpers when a support command (not
// a command under test) fails.
type ErrCommandFailed struct {
	Argv   []string
	Code   int
	Stderr string
}

func (e *ErrCommandFailed) Error() string {
	return fmt.Sprintf("command %q failed with exit %d: %s",
		strings.Join(e.Argv, " "), e.Code, strings.TrimSpace(e.Stderr))
}
