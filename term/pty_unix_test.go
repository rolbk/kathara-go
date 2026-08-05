//go:build linux || darwin

package term

import (
	"io"
	"os/exec"
	"strings"
	"testing"
)

// readAll drains the pty until EOF (Linux EIO is normalized by unixPty.Read).
func readAll(t *testing.T, p Pty) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := p.Read(buf)
		sb.Write(buf[:n])
		if err == io.EOF {
			return sb.String()
		}
		if err != nil {
			t.Fatalf("Read: %v (got %q so far)", err, sb.String())
		}
	}
}

func TestPtyEcho(t *testing.T) {
	p, err := New(Winsize{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	cmd := exec.Command("sh", "-c", "printf hello-pty")
	if err := p.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out := readAll(t, p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if !strings.Contains(out, "hello-pty") {
		t.Fatalf("output %q does not contain %q", out, "hello-pty")
	}
}

func TestPtyInitialSize(t *testing.T) {
	// The size handed to New must be visible to the child from its very first
	// instruction — the property the ConPTY leg mirrors by passing it to
	// CreatePseudoConsole (no initial-resize race; fixes OQ-19's
	// no-initial-size divergence symmetrically).
	p, err := New(Winsize{Cols: 101, Rows: 42})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	cmd := exec.Command("stty", "size")
	if err := p.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out := readAll(t, p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "42 101") {
		t.Fatalf("stty size = %q, want it to contain %q", out, "42 101")
	}
}

func TestPtyResize(t *testing.T) {
	p, err := New(Winsize{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()

	// The child blocks on `read` until we write a newline, which we only do
	// after Resize returns — so stty observes the post-resize geometry.
	cmd := exec.Command("sh", "-c", "read _ && stty size")
	if err := p.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Resize(Winsize{Cols: 90, Rows: 30}); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if _, err := p.Write([]byte("go\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	out := readAll(t, p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "30 90") {
		t.Fatalf("stty size after resize = %q, want it to contain %q", out, "30 90")
	}
}

func TestPtyLifecycleErrors(t *testing.T) {
	p, err := New(Winsize{})
	if err != nil {
		t.Fatal(err)
	}

	// Not started yet: I/O refuses, pre-start Resize is allowed (it sets the
	// startup size).
	if _, err := p.Read(make([]byte, 1)); err != errNotStarted {
		t.Fatalf("Read before Start = %v, want errNotStarted", err)
	}
	if _, err := p.Write([]byte("x")); err != errNotStarted {
		t.Fatalf("Write before Start = %v, want errNotStarted", err)
	}
	if err := p.Resize(Winsize{Cols: 10, Rows: 10}); err != nil {
		t.Fatalf("Resize before Start = %v, want nil", err)
	}

	cmd := exec.Command("sh", "-c", "exit 0")
	if err := p.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := p.Start(exec.Command("true")); err != errAlreadyStarted {
		t.Fatalf("second Start = %v, want errAlreadyStarted", err)
	}
	_ = readAll(t, p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil (idempotent)", err)
	}
	if err := p.Resize(Winsize{Cols: 1, Rows: 1}); err != errClosed {
		t.Fatalf("Resize after Close = %v, want errClosed", err)
	}
	if _, err := p.Read(make([]byte, 1)); err != errClosed {
		t.Fatalf("Read after Close = %v, want errClosed", err)
	}
}

func TestNewDefaultsSize(t *testing.T) {
	p, err := New(Winsize{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	cmd := exec.Command("stty", "size")
	if err := p.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out := readAll(t, p)
	if err := cmd.Wait(); err != nil {
		t.Fatalf("Wait: %v (output %q)", err, out)
	}
	if !strings.Contains(out, "24 80") {
		t.Fatalf("default size = %q, want it to contain %q", out, "24 80")
	}
}
