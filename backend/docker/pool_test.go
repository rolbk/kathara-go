package docker

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// TestChunk is `utils.chunk_list`: successive slices of `size`, with a list
// shorter than `size` coming back as one chunk.
//
// Python's two branches produce the same content and differ only in the type of
// the single chunk, which nothing observes; the boundaries are what matter,
// because they are the barriers the fan-out waits on.
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

// TestRunChunkedCompletesTheChunkThenReports is the error semantic
// CONCURRENCY.tsv records for all eight pool sites and which is neither of the
// two obvious ones: every task of the CURRENT chunk runs to completion even
// after a sibling has failed, and only then is the error reported.
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

	err := runChunked(context.Background(), items, func(_ context.Context, item int) error {
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

	err := runChunked(context.Background(), items, func(_ context.Context, item int) error {
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
	if err := runChunked(context.Background(), items, func(_ context.Context, item int) error {
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

// TestRunChunkedReportsOneErrorOnly pins the lossy half of the Python
// semantic: `map` re-raises ONE exception and the siblings' are discarded. A
// caller must not receive a join.
func TestRunChunkedReportsOneErrorOnly(t *testing.T) {
	first := errors.New("first")
	second := errors.New("second")

	err := runChunked(context.Background(), []int{1, 2}, func(_ context.Context, item int) error {
		if item == 1 {
			return first
		}
		return second
	})

	if err == nil {
		t.Fatal("runChunked reported no error")
	}
	if errors.Is(err, first) == errors.Is(err, second) {
		t.Errorf("runChunked = %v, want exactly one of the two worker errors", err)
	}
}

// TestRunChunkedThreadsTheContext: the context reaches the worker so the SDK
// calls inside it can be cancelled, which is the Ctrl-C path
// (JSON_CLI_CONTRACT.md §6.2). What it must NOT do is cancel siblings when one
// of them fails, which [TestRunChunkedCompletesTheChunkThenReports] covers.
func TestRunChunkedThreadsTheContext(t *testing.T) {
	type key struct{}
	ctx := context.WithValue(context.Background(), key{}, "threaded")

	var got atomic.Value
	if err := runChunked(ctx, []int{1}, func(ctx context.Context, _ int) error {
		got.Store(ctx.Value(key{}))
		return nil
	}); err != nil {
		t.Fatalf("runChunked: %v", err)
	}
	if got.Load() != "threaded" {
		t.Errorf("the worker received %v, want the caller's context", got.Load())
	}
}
