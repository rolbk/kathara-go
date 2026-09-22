//go:build unix

package util

import (
	"os"
	"syscall"
	"testing"
	"time"
)

func TestWaitUserInputDoesNotConsume(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		if err := writer.Close(); err != nil {
			t.Errorf("closing the write end: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Errorf("closing the read end: %v", err)
		}
	})

	ready, err := WaitUserInput()
	if err != nil {
		t.Fatalf("WaitUserInput: %v", err)
	}
	if ready {
		t.Fatal("an empty pipe must not report input")
	}

	if _, err := writer.Write([]byte("\r")); err != nil {
		t.Fatalf("write: %v", err)
	}

	ready, err = WaitUserInput()
	if err != nil {
		t.Fatalf("WaitUserInput: %v", err)
	}
	if !ready {
		t.Fatal("a readable pipe must report input")
	}

	// Twice, because a consuming implementation would pass the first check
	// and fail here.
	ready, err = WaitUserInput()
	if err != nil {
		t.Fatalf("WaitUserInput: %v", err)
	}
	if !ready {
		t.Fatal("WaitUserInput consumed the byte")
	}

	buf := make([]byte, 1)
	if _, err := reader.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if buf[0] != '\r' {
		t.Errorf("read %q, want the untouched carriage return", buf)
	}
}

// TestWaitUserInputAtEOF pins the behaviour that surprises people: a closed or
// redirected stdin is readable at EOF, so `kathara lstart < /dev/null` leaves
// the startup wait on its first iteration. Python does the same and the wait
// loop is written around it.
func TestWaitUserInputAtEOF(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		if err := reader.Close(); err != nil {
			t.Errorf("closing the read end: %v", err)
		}
	})

	if err := writer.Close(); err != nil {
		t.Fatalf("closing the write end: %v", err)
	}

	ready, err := WaitUserInput()
	if err != nil {
		t.Fatalf("WaitUserInput: %v", err)
	}
	if !ready {
		t.Error("EOF is readable; the probe must report it as input")
	}
}

// TestWaitUserInputEINTRKeepsTheDeadline pins PEP 475's half of the retry: a
// signal restarts the select with the *remaining* time, not with a fresh 0.1 s.
// Under the signal rate below, an implementation that re-arms the full timeout
// never finishes at all, so the bound is what makes the loop terminate.
func TestWaitUserInputEINTRKeepsTheDeadline(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = original
		if err := writer.Close(); err != nil {
			t.Errorf("closing the write end: %v", err)
		}
		if err := reader.Close(); err != nil {
			t.Errorf("closing the read end: %v", err)
		}
	})

	// SIGURG is the signal the Go runtime already uses for preemption, so
	// delivering more of them interrupts the select without changing what the
	// process does on receipt.
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := syscall.Kill(os.Getpid(), syscall.SIGURG); err != nil {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	defer close(stop)

	done := make(chan bool, 1)
	go func() {
		ready, err := WaitUserInput()
		if err != nil {
			t.Errorf("WaitUserInput: %v", err)
		}
		done <- ready
	}()

	select {
	case ready := <-done:
		if ready {
			t.Error("an empty pipe must not report input")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitUserInput did not return: the EINTR retry is re-arming the full timeout")
	}
}
