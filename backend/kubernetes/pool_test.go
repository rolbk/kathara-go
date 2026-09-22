package kubernetes

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestJoinFailuresOrdersTheBatchBytewiseByName(t *testing.T) {
	failed := []batchFailure{
		{name: "pc2", err: kerrors.NewMachineNotRunning("pc2")},
		{name: "router", err: kerrors.NewMachineBinary("frr", "router")},
		{name: "pc10", err: kerrors.NewMachineNotRunning("pc10")},
		{name: "PC1", err: kerrors.NewMachineNotRunning("PC1")},
		{name: "pc1", err: kerrors.NewMachineBinary("ip", "pc1")},
	}

	batch := kerrors.Joined(joinFailures(failed))
	want := []string{
		"Device `PC1` is not running.",
		"Binary `ip` not found in device `pc1`.",
		"Device `pc10` is not running.",
		"Device `pc2` is not running.",
		"Binary `frr` not found in device `router`.",
	}
	if len(batch) != len(want) {
		t.Fatalf("the join carries %d errors, want %d — every failure of the batch belongs in it", len(batch), len(want))
	}
	for i, err := range batch {
		if err.Error() != want[i] {
			t.Errorf("element %d = %q, want %q", i, err.Error(), want[i])
		}
	}
}

func TestJoinFailuresOfOneDoesNotGrowAnErrorsKey(t *testing.T) {
	only := kerrors.NewMachineNotRunning("pc1")
	err := joinFailures([]batchFailure{{name: "pc1", err: only}})

	if batch := kerrors.Joined(err); len(batch) != 1 {
		t.Fatalf("a single failure joined to %d elements, want 1", len(batch))
	}
	if got := err.Error(); got != only.Error() {
		t.Errorf("rendered %q, want the failure's own message %q", got, only.Error())
	}
	if !errors.Is(err, kerrors.ErrMachineNotRunning) {
		t.Error("errors.Is must still reach the class through the join")
	}
}

func TestRunChunkedJoinsEveryFailureInCanonicalOrder(t *testing.T) {
	size := util.PoolSize()
	if size < 3 {
		t.Skip("needs a pool wide enough for a primary and two decoys in one chunk")
	}

	// One full chunk of failures — a1 plus at least two decoys — followed by a
	// second chunk that must never run.
	names := make([]string, 0, 2*size)
	names = append(names, "a1")
	for i := 1; i < size; i++ {
		names = append(names, "z"+strconv.Itoa(i))
	}
	for i := range size {
		names = append(names, "never"+strconv.Itoa(i))
	}

	var ran atomic.Int64
	err := runChunked(context.Background(), names, func(name string) string { return name },
		func(_ context.Context, name string) error {
			ran.Add(1)
			if strings.HasPrefix(name, "never") {
				return nil
			}
			if name == "a1" {
				time.Sleep(50 * time.Millisecond)
			}
			return kerrors.NewMachineNotRunning(name)
		})

	if err == nil {
		t.Fatal("runChunked reported no error for a chunk that failed entirely")
	}
	if got := ran.Load(); got != int64(size) {
		t.Errorf("%d tasks ran, want exactly the failing chunk's %d — a later chunk must not start", got, size)
	}

	batch := kerrors.Joined(err)
	if len(batch) != size {
		t.Fatalf("the batch carries %d errors, want all %d failures of the chunk", len(batch), size)
	}

	want := make([]string, 0, size)
	want = append(want, "Device `a1` is not running.")
	for i := 1; i < size; i++ {
		want = append(want, "Device `z"+strconv.Itoa(i)+"` is not running.")
	}
	for i, e := range batch {
		if e.Error() != want[i] {
			t.Errorf("element %d = %q, want %q", i, e.Error(), want[i])
		}
	}

	if got := kerrors.Code(err); got != kerrors.CodeMachineNotRunning {
		t.Errorf("Code(join) = %q, want the primary's %q", got, kerrors.CodeMachineNotRunning)
	}
	var machine *kerrors.MachineError
	if !errors.As(batch[0], &machine) || machine.Machine != "a1" {
		t.Errorf("the primary error is %v, want the batch's bytewise-first device a1", batch[0])
	}
}

// TestRunChunkedIsDeterministicAcrossRuns: the same half-failing batch reports
// the same primary every time, with nothing staged — the arrival order is the
// scheduler's, which is exactly what used to leak into the output.
func TestRunChunkedIsDeterministicAcrossRuns(t *testing.T) {
	size := util.PoolSize()
	if size < 3 {
		t.Skip("needs a pool wide enough for a primary and two decoys in one chunk")
	}

	names := make([]string, 0, size)
	names = append(names, "a1")
	for i := 1; i < size; i++ {
		names = append(names, "z"+strconv.Itoa(i))
	}

	var first string
	for run := range 50 {
		err := runChunked(context.Background(), names, func(name string) string { return name },
			func(_ context.Context, name string) error {
				return kerrors.NewMachineNotRunning(name)
			})
		if err == nil {
			t.Fatal("runChunked reported no error")
		}
		got := err.Error()
		if run == 0 {
			first = got
			batch := kerrors.Joined(err)
			if len(batch) != len(names) {
				t.Fatalf("the batch carries %d errors, want all %d failures", len(batch), len(names))
			}
			if primary := batch[0].Error(); primary != "Device `a1` is not running." {
				t.Fatalf("primary = %q, want the bytewise-first device", primary)
			}
			continue
		}
		if got != first {
			t.Fatalf("run %d reported %q, run 0 reported %q — the batch must not depend on completion order", run, got, first)
		}
	}
}

// TestItemNamesAreTheKatharaNames pins the sort keys the call sites hand
// [runChunked]: the API objects are ordered by the Kathará device or collision
// domain name the `name` label carries — not by the mangled Kubernetes name —
// and an object without that label still yields a key, so the order stays
// total.
func TestItemNamesAreTheKatharaNames(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name:   "pc1-abcdefgh",
		Labels: map[string]string{labelName: "pc1"},
	}}
	if got := podItemName(pod); got != "pc1" {
		t.Errorf("podItemName = %q, want the device name pc1", got)
	}
	unlabelled := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pc1-abcdefgh"}}
	if got := podItemName(unlabelled); got != "pc1-abcdefgh" {
		t.Errorf("podItemName = %q, want the pod name as the fallback key", got)
	}

	network := NetworkDefinition("kt-abcdefgh-cd-a", "A", "abcdefgh", 1)
	if got := networkItemName(network); got != "A" {
		t.Errorf("networkItemName = %q, want the collision domain name A", got)
	}
}
