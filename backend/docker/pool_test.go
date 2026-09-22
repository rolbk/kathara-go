package docker

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// intItemName is the [runChunked] name accessor for the tests that fan out over
// plain integers, where the item's identity is all the sort key has to be.
func intItemName(item int) string { return strconv.Itoa(item) }

// TestChunk is `utils.chunk_list`: successive slices of `size`, with a list
// shorter than `size` coming back as one chunk.
func TestChunk(t *testing.T) {
	tests := []struct {
		name  string
		items []int
		size  int
		want  [][]int
	}{
		{"empty", nil, 4, nil},
		{"shorter than the pool is one chunk", []int{1, 2, 3}, 4, [][]int{{1, 2, 3}}},
		{"exactly the pool size is one chunk", []int{1, 2, 3, 4}, 4, [][]int{{1, 2, 3, 4}}},
		{"a ragged tail", []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, 4, [][]int{{1, 2, 3, 4}, {5, 6, 7, 8}, {9, 10}}},
		{"an exact multiple", []int{1, 2, 3, 4}, 2, [][]int{{1, 2}, {3, 4}}},
		// `list_chunks` would spin forever on a zero size; `get_pool_size` is
		// `cpu_count()` and cannot produce one, and a caller that computed one
		// should not hang.
		{"a zero size degrades to one chunk", []int{1, 2, 3}, 0, [][]int{{1, 2, 3}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := chunk(tt.items, tt.size); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("chunk = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRunChunkedCompletesTheChunkThenReports(t *testing.T) {
	size := util.PoolSize()
	items := make([]int, size)
	for i := range items {
		items[i] = i
	}

	var (
		mu        sync.Mutex
		completed int
		arrived   atomic.Int64
		allIn     = make(chan struct{})
		closeOnce sync.Once
	)
	boom := errors.New("boom")

	err := runChunked(context.Background(), items, intItemName, func(_ context.Context, item int) error {
		if arrived.Add(1) == int64(size) {
			closeOnce.Do(func() { close(allIn) })
		}
		if item == 0 {
			// Fail first, and let the siblings run on.
			return boom
		}
		// Block until every task of the chunk has started, which cannot happen
		// if the first failure cancelled any of them.
		<-allIn
		mu.Lock()
		completed++
		mu.Unlock()
		return nil
	})

	if !errors.Is(err, boom) {
		t.Fatalf("runChunked = %v, want the worker's error", err)
	}
	if completed != size-1 {
		t.Errorf("%d siblings completed, want %d — a failure must cancel none", completed, size-1)
	}
}

// TestRunChunkedAbandonsLaterChunks is the other half: `map` raises out of the
// `for chunk` loop, so nothing after the failing chunk is ever submitted.
func TestRunChunkedAbandonsLaterChunks(t *testing.T) {
	size := util.PoolSize()
	if size < 2 {
		t.Skip("needs a pool wide enough for two chunks to be distinguishable")
	}

	// Two full chunks: the first one fails, the second must not run at all.
	items := make([]int, 2*size)
	for i := range items {
		items[i] = i
	}

	var ran atomic.Int64
	boom := errors.New("boom")

	err := runChunked(context.Background(), items, intItemName, func(_ context.Context, item int) error {
		ran.Add(1)
		if item == 0 {
			return boom
		}
		return nil
	})

	if !errors.Is(err, boom) {
		t.Fatalf("runChunked = %v, want the worker's error", err)
	}
	if got := ran.Load(); got != int64(size) {
		t.Errorf("%d tasks ran, want exactly the first chunk's %d", got, size)
	}
}

// TestRunChunkedRunsEverythingOnSuccess is the ordinary path: every item is
// submitted exactly once, across as many chunks as it takes.
func TestRunChunkedRunsEverythingOnSuccess(t *testing.T) {
	items := make([]int, 3*util.PoolSize()+1)
	for i := range items {
		items[i] = i
	}

	var (
		mu   sync.Mutex
		seen = map[int]int{}
	)
	if err := runChunked(context.Background(), items, intItemName, func(_ context.Context, item int) error {
		mu.Lock()
		seen[item]++
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("runChunked: %v", err)
	}

	if len(seen) != len(items) {
		t.Fatalf("saw %d distinct items, want %d", len(seen), len(items))
	}
	for item, count := range seen {
		if count != 1 {
			t.Errorf("item %d ran %d times", item, count)
		}
	}
}

func TestJoinFailuresOrdersTheBatchBytewiseByName(t *testing.T) {
	// Arrival order, i.e. the order the workers happened to finish in.
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
				// Finish last: the canonical primary is the slowest worker.
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

// TestRunChunkedIsDeterministicAcrossRuns is the other half of the triage
// finding: the same half-failing batch must report the same primary EVERY time.
// The old code was live-proven nondeterministic over four runs, so the loop
// here is the cheapest thing that would have caught it.
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
				// No stagger: let the scheduler decide the arrival order, which
				// is exactly what used to leak into the output.
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
// domain name their `name` label carries — not by the mangled Docker name,
// which prefixes the user and suffixes the lab hash — and an object without
// that label still yields a key, so the order stays total.
func TestItemNamesAreTheKatharaNames(t *testing.T) {
	if got := containerItemName(newTestContainer("pc1", nil)); got != "pc1" {
		t.Errorf("containerItemName = %q, want the device name pc1", got)
	}

	unlabelled := &Container{Attrs: container.InspectResponse{
		ContainerJSONBase: &container.ContainerJSONBase{Name: "/kathara_user_pc1_" + fixtureHash},
	}}
	if got, want := containerItemName(unlabelled), "kathara_user_pc1_"+fixtureHash; got != want {
		t.Errorf("containerItemName = %q, want the container name %q as the fallback key", got, want)
	}

	net := &Network{Attrs: network.Inspect{
		Name:   "kathara_user_A_" + fixtureHash,
		Labels: map[string]string{labelName: "A"},
	}}
	if got := networkItemName(net); got != "A" {
		t.Errorf("networkItemName = %q, want the collision domain name A", got)
	}
	shared := &Network{Attrs: network.Inspect{Name: "kathara_A"}}
	if got := networkItemName(shared); got != "kathara_A" {
		t.Errorf("networkItemName = %q, want the network name as the fallback key", got)
	}
}

func TestRunChunkedThreadsTheContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "threaded")

	var got atomic.Value
	if err := runChunked(ctx, []int{1}, intItemName, func(ctx context.Context, _ int) error {
		got.Store(ctx.Value(key{}))
		return nil
	}); err != nil {
		t.Fatalf("runChunked: %v", err)
	}
	if got.Load() != "threaded" {
		t.Errorf("the worker received %v, want the caller's context", got.Load())
	}
}
