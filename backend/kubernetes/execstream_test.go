package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// TestExecExitCode is the `try: response.returncode / except ValueError` block
// (`KubernetesMachine.py:913-918`), which client-go reaches by a different
// route and which must land on the same three answers.
func TestExecExitCode(t *testing.T) {
	tests := []struct {
		name      string
		streamErr error
		want      int
		wantErr   error
	}{
		{
			name: "success is 0",
		},
		{
			name:      "a real exit code is reported",
			streamErr: utilexec.CodeExitError{Err: errors.New("command terminated with exit code 42"), Code: 42},
			want:      42,
		},
		{
			// Python's `int()` fails, the regexp matches, and the class is
			// MachineBinary carrying `shlex.join(command)`.
			name:      "an OCI runtime failure is MachineBinary",
			streamErr: errors.New(`OCI runtime exec failed: exec failed: unable to start container process: exec: "nope": executable file not found in $PATH: unknown`),
			wantErr:   kerrors.ErrMachineBinary,
		},
		{
			// Python's `int()` fails and the regexp does not match, so the
			// exit code is the literal 1.
			name:      "any other failure is exit code 1",
			streamErr: errors.New("error dialing backend: connection refused"),
			want:      1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := execExitCode(context.Background(), test.streamErr, "pc1", []string{"ls", "-la"})
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				// `binary` is the whole command, quoted — Megalos never learns
				// which word was missing.
				if !strings.Contains(err.Error(), "Binary `ls -la` not found in device `pc1`.") {
					t.Errorf("message = %q", err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("execExitCode: %v", err)
			}
			if got != test.want {
				t.Errorf("exit code = %d, want %d", got, test.want)
			}
		})
	}
}

// TestExecExitCodeCancellation pins that a cancelled exec is NOT turned into
// exit code 1: the Ctrl-C path reports cancellation.
func TestExecExitCodeCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := execExitCode(ctx, context.Canceled, "pc1", []string{"ls"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestManagerExec is `KubernetesManager.exec(..., stream=False)`: the request
// it builds (`stderr=True, tty=False`) and the two output sides it separates.
func TestManagerExec(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, executor := newTestManager(t, s, newTestPod(hash, "pc1"))
	executor.stdout = []byte("out")
	executor.stderr = []byte("err")
	executor.err = utilexec.CodeExitError{Err: errors.New("x"), Code: 3}

	stdout, stderr, code, err := m.Exec(context.Background(), "pc1",
		kathara.NewShellCommand("ls -la"), kathara.LabRef{Hash: hash}, kathara.NoWait())
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if string(stdout) != "out" || string(stderr) != "err" || code != 3 {
		t.Errorf("got (%q, %q, %d), want (out, err, 3)", stdout, stderr, code)
	}

	req := executor.lastRequest(t)
	if !req.Stderr || req.TTY || req.Stdin {
		t.Errorf("request = %+v, want stderr=true tty=false stdin=false", req)
	}
	if strings.Join(req.Command, " ") != "ls -la" {
		t.Errorf("command = %q, want the shell-split form", req.Command)
	}
	if req.Namespace != hash {
		t.Errorf("namespace = %q, want %q", req.Namespace, hash)
	}
}

// TestManagerExecMachineNotRunning is the empty-pod-listing branch of `exec`.
func TestManagerExecMachineNotRunning(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	_, _, _, err := m.Exec(context.Background(), "pc1",
		kathara.NewCommand("ls"), kathara.LabRef{Hash: defaultScenarioHash}, kathara.NoWait())
	if !errors.Is(err, kerrors.ErrMachineNotRunning) {
		t.Fatalf("error = %v, want MachineNotRunning", err)
	}
}

// TestManagerExecInvocationError is EXPECTATIONS-k8s §4
// `test_exec_invocation_error`: no lab identity, and no exec attempted.
func TestManagerExecInvocationError(t *testing.T) {
	s := testSettings()
	m, _, _, executor := newTestManager(t, s)

	_, _, _, err := m.Exec(context.Background(), "pc1",
		kathara.NewCommand("ls"), kathara.LabRef{}, kathara.NoWait())
	if !errors.Is(err, kerrors.ErrInvocation) {
		t.Fatalf("error = %v, want an InvocationError", err)
	}
	if len(executor.requests) != 0 {
		t.Error("an exec was attempted despite the bad invocation")
	}
}

// TestExecStreamFrames is `KubernetesExecStream`: chunks arrive as frames, the
// end is io.EOF, and the exit code is available afterwards.
func TestExecStreamFrames(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, executor := newTestManager(t, s, newTestPod(hash, "pc1"))
	executor.stdout = []byte("hello")
	executor.stderr = []byte("oops")

	stream, err := m.ExecStream(context.Background(), "pc1",
		kathara.NewCommand("ls"), kathara.LabRef{Hash: hash}, kathara.NoWait())
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var stdout, stderr bytes.Buffer
	for {
		out, errOut, err := stream.Next(context.Background())
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("Next: %v", err)
		}
		stdout.Write(out)
		stderr.Write(errOut)
	}
	if stdout.String() != "hello" || stderr.String() != "oops" {
		t.Errorf("got (%q, %q), want (hello, oops)", stdout.String(), stderr.String())
	}

	code, err := stream.ExitCode(context.Background())
	if err != nil || code != 0 {
		t.Errorf("exit code = %d, %v; want 0, nil", code, err)
	}
}

// TestExecStreamCloseIsIdempotent pins [kathara.ExecStream.Close]'s contract:
// safe to call more than once and before the stream is exhausted. CPython's
// refcounting gave Python this for free.
func TestExecStreamCloseIsIdempotent(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, executor := newTestManager(t, s, newTestPod(hash, "pc1"))
	executor.stdout = bytes.Repeat([]byte("x"), 1024)

	stream, err := m.ExecStream(context.Background(), "pc1",
		kathara.NewCommand("ls"), kathara.LabRef{Hash: hash}, kathara.NoWait())
	if err != nil {
		t.Fatalf("ExecStream: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestCopyFilesPushesTheArchive is `copy_files`: `tar xvfz - -C /` with the
// archive on stdin.
func TestCopyFilesPushesTheArchive(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	pod := newTestPod(hash, "pc1")
	m, _, _, executor := newTestManager(t, s, pod)

	var received []byte
	executor.stdin = &received

	device := &kathara.CopyEntry{GuestPath: "/etc/motd", Content: strings.NewReader("hello\n")}
	machine := newTestLab(t, s)
	pc1, err := machine.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	pc1.APIObject = pod

	if err := m.CopyFiles(context.Background(), pc1, []kathara.CopyEntry{*device}); err != nil {
		t.Fatalf("CopyFiles: %v", err)
	}

	req := executor.lastRequest(t)
	if strings.Join(req.Command, " ") != "tar xvfz - -C /" {
		t.Errorf("command = %q", req.Command)
	}
	if !req.Stdin {
		t.Error("stdin was not opened")
	}
	if len(received) == 0 {
		t.Error("no archive reached stdin")
	}
}

// TestRetrieveFilesExtracts is `retrieve_files`: `tar cf - <src>` on stdout,
// buffered and extracted into dst.
func TestRetrieveFilesExtracts(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	pod := newTestPod(hash, "pc1")
	m, _, _, executor := newTestManager(t, s, pod)
	executor.stdout = tarWithFile(t, "etc/motd", "hello\n")

	lab := newTestLab(t, s)
	pc1, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	pc1.APIObject = pod

	dst := t.TempDir()
	if err := m.RetrieveFiles(context.Background(), pc1, "/etc/motd", dst); err != nil {
		t.Fatalf("RetrieveFiles: %v", err)
	}

	req := executor.lastRequest(t)
	if strings.Join(req.Command, " ") != "tar cf - /etc/motd" {
		t.Errorf("command = %q", req.Command)
	}
	content, err := readFile(dst + "/etc/motd")
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if content != "hello\n" {
		t.Errorf("extracted %q, want %q", content, "hello\n")
	}
}

// TestConnectTTYReadinessAndShell is EXPECTATIONS-k8s §1 "connect": the
// not-running and not-ready branches, and the shell fallback chain.
func TestConnectTTYReadinessAndShell(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)

	t.Run("no pod is MachineNotRunning", func(t *testing.T) {
		m, _, _, _ := newTestManager(t, s)
		_, err := m.ConnectTTY(context.Background(), "pc1", kathara.LabRef{Hash: hash}, kathara.ConnectTTYOptions{})
		if !errors.Is(err, kerrors.ErrMachineNotRunning) {
			t.Fatalf("error = %v, want MachineNotRunning", err)
		}
	})

	t.Run("a pending pod is MachineNotReady", func(t *testing.T) {
		pod := newTestPod(hash, "pc1")
		pod.Status.Phase = "Pending"
		m, _, _, _ := newTestManager(t, s, pod)

		_, err := m.ConnectTTY(context.Background(), "pc1", kathara.LabRef{Hash: hash}, kathara.ConnectTTYOptions{})
		if !errors.Is(err, kerrors.ErrMachineNotReady) {
			t.Fatalf("error = %v, want MachineNotReady", err)
		}
	})

	shellTests := []struct {
		name     string
		option   string
		podShell string
		want     []string
	}{
		{name: "explicit shell is split", option: "/usr/bin/env zsh", want: []string{"/usr/bin/env", "zsh"}},
		{name: "pod shell is used", podShell: "/bin/zsh", want: []string{"/bin/zsh"}},
		{name: "empty pod shell falls back to the setting", podShell: "", want: []string{"/bin/bash"}},
	}

	for _, test := range shellTests {
		t.Run(test.name, func(t *testing.T) {
			pod := newTestPod(hash, "pc1")
			pod.Spec.Containers[0].Env = nil
			if test.podShell != "" {
				pod.Spec.Containers[0].Env = []corev1.EnvVar{corev1EnvVar(megalosShellEnv, test.podShell)}
			}
			m, _, _, executor := newTestManager(t, s, pod)

			session, err := m.ConnectTTY(context.Background(), "pc1", kathara.LabRef{Hash: hash},
				kathara.ConnectTTYOptions{Shell: test.option})
			if err != nil {
				t.Fatalf("ConnectTTY: %v", err)
			}
			defer func() { _ = session.Close() }()

			req := executor.lastRequest(t)
			if strings.Join(req.Command, " ") != strings.Join(test.want, " ") {
				t.Errorf("shell = %q, want %q", req.Command, test.want)
			}
			if !req.TTY || !req.Stdin {
				t.Errorf("request = %+v, want tty=true stdin=true", req)
			}
		})
	}
}

// TestConnectTTYStartupLog is the `logs and print_startup_log` block: the two
// banners and the device's log bytes, written to the caller's writer rather
// than to stdout (JSON_CLI_CONTRACT.md §1.3).
func TestConnectTTYStartupLog(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, executor := newTestManager(t, s, newTestPod(hash, "pc1"))
	executor.stdout = []byte("boot output\n")

	var log bytes.Buffer
	session, err := m.ConnectTTY(context.Background(), "pc1", kathara.LabRef{Hash: hash},
		kathara.ConnectTTYOptions{Logs: true, LogWriter: &log})
	if err != nil {
		t.Fatalf("ConnectTTY: %v", err)
	}
	defer func() { _ = session.Close() }()

	got := log.String()
	if !strings.HasPrefix(got, "--- Startup Commands Log\n\n") {
		t.Errorf("log = %q, want the opening banner", got)
	}
	if !strings.Contains(got, "boot output\n") {
		t.Errorf("log = %q, want the device output", got)
	}
	if !strings.HasSuffix(got, "\n--- End Startup Commands Log\n\n") {
		t.Errorf("log = %q, want the closing banner", got)
	}

	// The log exec is a separate request from the shell one, and it carries the
	// `cat` of the three log paths — shell-split, so the glob is a literal.
	if len(executor.requests) != 2 {
		t.Fatalf("%d exec requests, want 2 (log then shell)", len(executor.requests))
	}
	if strings.Join(executor.requests[0].Command, " ") !=
		"/bin/cat /var/log/shared.log /var/log/startup.log /var/kathara/*" {
		t.Errorf("log command = %q", executor.requests[0].Command)
	}
}

// TestTTYSessionResizeCoalesces pins that a resize never blocks the caller: the
// caller is a SIGWINCH handler and only the latest size matters.
func TestTTYSessionResizeCoalesces(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, executor := newTestManager(t, s, newTestPod(hash, "pc1"))
	executor.block = make(chan struct{})
	defer close(executor.block)

	session, err := m.ConnectTTY(context.Background(), "pc1", kathara.LabRef{Hash: hash}, kathara.ConnectTTYOptions{})
	if err != nil {
		t.Fatalf("ConnectTTY: %v", err)
	}
	defer func() { _ = session.Close() }()

	for i := range 100 {
		if err := session.Resize(uint16(80+i), 24); err != nil {
			t.Fatalf("Resize: %v", err)
		}
	}
}
