package cliout

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestSlogHandlerRendersAttributesAsTrailingPairs pins what the handler does
// with structured attributes, which is the reason the guard below exists: an
// attribute becomes a trailing ` key=value`, and Python's `logging` has no such
// thing. Every message that has to match the oracle byte for byte must
// therefore arrive already interpolated.
func TestSlogHandlerRendersAttributesAsTrailingPairs(t *testing.T) {
	var buf bytes.Buffer
	console := &Console{Out: &buf, Err: &buf, Level: LevelDebug, Width: 200}
	handler := NewSlogHandler(console)

	record := slog.NewRecord(time.Time{}, slog.LevelWarn, "message", 0)
	record.AddAttrs(slog.String("device", "pc1"))
	if err := handler.Handle(context.Background(), record); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if got := buf.String(); !strings.Contains(got, "message device=pc1") {
		t.Errorf("attribute did not render as a trailing pair: %q", got)
	}
}

// TestPortedLogCallsCarryNoAttributes is the regression guard for the
// structured-log interpolation defect class.
//
// Python interpolates every operand into the message before `logging` sees it
// (`"To expose ports of device `%s` on the host, …" % machine.name`,
// `DockerMachine.py:246-249`); a Go call that passes `"device", machine.Name`
// instead renders `… device=pc1`, which is a user-visible divergence the
// `syn-port-udp` golden caught. The rule for the ported packages is therefore:
// one argument to `slog.Debug`/`Info`/`Warn`/`Error`, the finished message.
//
// The exemptions below are the sites with NO Python counterpart — the two
// backends' event-dispatch failures, which `EventDispatcher.dispatch` cannot
// produce because it returns nothing. Those are Go-only diagnostics and the
// `key=value` rendering is the only thing they could do.
func TestPortedLogCallsCarryNoAttributes(t *testing.T) {
	// Sites without a Python original, keyed by the message literal.
	exempt := []string{
		"Failed to dispatch machines_deploy_started.",
		"Failed to dispatch machine_deployed.",
		"Failed to dispatch machines_deploy_ended.",
		"Failed to dispatch machines_undeploy_started.",
		"Failed to dispatch machine_undeployed.",
		"Failed to dispatch machines_undeploy_ended.",
	}

	for _, dir := range []string{
		"../../backend/docker",
		"../../backend/kubernetes",
		"../../labfile",
		"../../model",
		"../../internal/util",
		"../../cmd/kathara",
	} {
		for _, call := range parseSlogCalls(t, dir) {
			if len(call.args) == 1 {
				continue
			}
			if call.message != "" && slices.Contains(exempt, call.message) {
				continue
			}
			t.Errorf("%s: slog.%s carries %d structured attribute(s); Python interpolates them into the message",
				call.pos, call.level, len(call.args)-1)
		}
	}
}

// TestPortedLogMessagesMatchPythonFormatStrings pins the literal text of every
// ported log message against the format string of the `logging` call it ports.
//
// The Go side is read out of the source: a message expression is rendered as a
// template whose string literals are kept verbatim and whose interpolated
// operands become `%s`, which is the same shape as Python's `%`-format and
// f-string templates. Each want below is that Python template, transcribed
// from the cited line.
//
// This is the assertion the `syn-port-udp` golden made: the message text is
// user-visible output, and the port owes it byte for byte.
func TestPortedLogMessagesMatchPythonFormatStrings(t *testing.T) {
	tests := []struct {
		dir  string
		want []string
	}{
		{
			dir: "../../backend/docker",
			want: []string{
				// `DockerImage.py:59`
				"Pulling image `%s`... This may take a while.",
				// `DockerImage.py:74`
				"Checking updates for %s...",
				// `DockerImage.py:77`
				"No need to check image digest of %s.",
				// `DockerImage.py:84`
				"Image %s is built locally.",
				// `DockerImage.py:150` — names no image.
				"Cannot check updates, skipping...",
				// `DockerMachine.py:165`
				"Shared folder cannot be mounted with a remote Docker connection.",
				// `DockerMachine.py:219`
				"Creating device `%s`...",
				// `DockerMachine.py:246-249`
				"To expose ports of device `%s` on the host, you have to specify the `bridged` option on that device.",
				// `DockerMachine.py:325`
				"Volumes of device `%s` will not be mounted.",
				// `DockerMachine.py:332`
				"Privileged flag is ignored with a remote Docker connection.",
				// `DockerMachine.py:509`
				"Starting device `%s`...",
				// `DockerMachine.py:526-529`
				"Connecting device `%s` to collision domain `%s` on interface %s...",
				// `DockerMachine.py:551`
				"Executing startup command on `%s`: %s",
				// `DockerMachine.py:565-568`
				"Shell `%s` not found in image `%s` of device `%s`. Startup commands will not be executed and " +
					"terminal will not open. Please specify a valid shell for this device.",
				// `DockerMachine.py:677`
				"Connect to device `%s` with shell: %s",
				// `DockerMachine.py:777`
				"Executing command `%s` to device with name: %s",
				// `DockerMachine.py:910` — no backticks at this site.
				"Waiting startup commands execution for device %s...",
				// `DockerMachine.py:1099`
				"Executing shutdown commands on `%s`: %s",
				// `DockerMachine.py:1111-1113`
				"Shell `%s` not found in image `%s` of device `%s`. Shutdown commands will not be executed.",
				// `DockerManager.py:363`
				"Cannot wipe devices of other users with a remote Docker connection.",
				// `DockerPlugin.py:43`
				"Checking plugin `%s`...",
				// `DockerPlugin.py:50` — parentheses, not backticks.
				"Installing Kathara Network Plugin (%s)...",
				// `DockerPlugin.py:52`
				"Kathara Network Plugin installed successfully!",
				// `DockerPlugin.py:58`, `:64`, `:75` — the same line three times.
				"Enabling plugin `%s`...",
				"Enabling plugin `%s`...",
				"Enabling plugin `%s`...",
				// `DockerPlugin.py:179`
				"Configuring xtables.lock source to `%s`...",
			},
		},
		{
			dir: "../../backend/kubernetes",
			want: []string{
				// `KubernetesLink.py:130`
				"External is not supported on Megalos. It will be ignored.",
				// `KubernetesMachine.py:229-232`
				"Network scenario startup is not responding for over %s seconds, exiting. " +
					"To check devices status, use the following command:\n\tkubectl -n %s get pods",
				// `KubernetesMachine.py:251` and `:630` — deploy and undeploy watchers.
				"Event: %s - Pod: %s (Device %s)",
				"Event: %s - Pod: %s (Device %s)",
				// `KubernetesMachine.py:258`
				"Device `%s` ready.",
				// `KubernetesMachine.py:263-267`
				"Stopping to wait device `%s` since it restarted more than %s times. " +
					"For a detailed log use the following command:\n\tkubectl -n %s describe pod %s",
				// `KubernetesMachine.py:273`
				"Device `%s` has been restarted %s times.",
				// `KubernetesMachine.py:309`
				"Creating device `%s`...",
				// `KubernetesMachine.py:316-317`
				"Privileged option is not supported on Megalos. It will be ignored on device `%s`.",
				// `KubernetesMachine.py:322-323`
				"Bridged option is not supported on Megalos. It will be ignored on device `%s`.",
				// `KubernetesMachine.py:328-329`
				"Ulimit option is not supported on Megalos. It will be ignored on device `%s`.",
				// `KubernetesMachine.py:407`
				"Volumes of device `%s` will not be mounted.",
				// `KubernetesMachine.py:727`
				"Connect to device `%s` with shell: %s",
				// `KubernetesManager.py:220` and `:353`
				"Waiting for namespace deletion...",
				"Waiting for namespace deletion...",
				// `KubernetesManager.py:367`, `:567`, `:600`, `:634`, `:667`, `:777`,
				// `:868` — one per user-filtered entry point, three of which this
				// package reaches.
				"User-specific options have no effect on Megalos.",
				"User-specific options have no effect on Megalos.",
				"User-specific options have no effect on Megalos.",
				// `KubernetesManager.py:474`, `:505`
				"Wait option has no effect on Megalos.",
				// `KubernetesNamespace.py:97` and `:117`
				"Event: %s - Namespace: %s",
				"Event: %s - Namespace: %s",
				// `KubernetesSecret.py:81`
				"Event: %s - Secret: %s",

				// Go-only, no `logging` original: the event-dispatch failures
				// (`EventDispatcher.dispatch` returns nothing and cannot fail)
				// and the undeploy watchdog DIVERGENCES.md adds where Python
				// hangs forever.
				"Failed to dispatch machines_deploy_started.",
				"Failed to dispatch machine_deployed.",
				"Failed to dispatch machines_deploy_ended.",
				"Failed to dispatch machines_undeploy_started.",
				"Failed to dispatch machine_undeployed.",
				"Failed to dispatch machines_undeploy_ended.",
				"Stopped waiting for device shutdown: no pod event for %s seconds.",

				// `KubernetesMachine.py:817`, built by `execCommandLogLine` and
				// asserted in the kubernetes package's own test.
				"%s",
				"%s",
			},
		},
		{
			dir: "../../labfile",
			want: []string{
				// `LabParser.py:76-78`
				"In %s - Line %s: Device `%s` already has a value assigned to meta `%s`. " +
					"Previous value has been overwritten with `%s`.",
				// `DepParser.py:37`
				"lab.dep file is empty. Ignoring...",
			},
		},
		{
			dir: "../../model",
			want: []string{
				// `Lab.py:189`
				"Checking network scenario integrity...",
				// `Machine.py:365`
				"Checking `%s` integrity...",
				// `Machine.py:378`
				"`%s` interfaces are %s.",
			},
		},
		{
			dir: "../../internal/util",
			want: []string{
				// `utils.py:399`
				"Machine architecture is `%s`.",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.dir, func(t *testing.T) {
			var got []string
			for _, call := range parseSlogCalls(t, tc.dir) {
				got = append(got, call.template)
			}
			slices.Sort(got)

			want := slices.Clone(tc.want)
			slices.Sort(want)

			if !slices.Equal(got, want) {
				t.Errorf("log message templates in %s do not match the Python format strings\n got: %q\nwant: %q",
					tc.dir, diff(got, want), diff(want, got))
			}
		})
	}
}

// diff returns the entries of a that b does not account for, counting
// duplicates.
func diff(a, b []string) []string {
	remaining := slices.Clone(b)
	var out []string
	for _, item := range a {
		if i := slices.Index(remaining, item); i >= 0 {
			remaining = slices.Delete(remaining, i, i+1)
			continue
		}
		out = append(out, item)
	}
	return out
}

type slogCall struct {
	pos      string
	level    string
	args     []ast.Expr
	message  string
	template string
}

// parseSlogCalls returns every `slog.Debug|Info|Warn|Error` call in the
// non-test Go files of dir.
func parseSlogCalls(t *testing.T, dir string) []slogCall {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	levels := []string{"Debug", "Info", "Warn", "Error"}
	fset := token.NewFileSet()
	var calls []slogCall
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "slog" || !slices.Contains(levels, sel.Sel.Name) {
				return true
			}
			calls = append(calls, slogCall{
				pos:      fset.Position(call.Pos()).String(),
				level:    sel.Sel.Name,
				args:     call.Args,
				message:  firstStringLiteral(call.Args),
				template: messageTemplate(call.Args[0]),
			})
			return true
		})
	}
	return calls
}

// messageTemplate renders a message expression the way Python's format strings
// read: string literals verbatim, every interpolated operand as `%s`. A
// concatenation chain is walked left to right, which is the order the operands
// appear in the finished line.
func messageTemplate(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			if unquoted, err := strconv.Unquote(e.Value); err == nil {
				return unquoted
			}
		}
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return messageTemplate(e.X) + messageTemplate(e.Y)
		}
	case *ast.ParenExpr:
		return messageTemplate(e.X)
	}
	return "%s"
}

// firstStringLiteral returns the leading string literal of a call's argument
// list, or "" when the message is not a bare literal.
func firstStringLiteral(args []ast.Expr) string {
	if len(args) == 0 {
		return ""
	}
	lit, ok := args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	unquoted, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return unquoted
}
