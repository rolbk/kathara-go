// This file is `multiprocessing.dummy.Pool` + `utils.chunk_list`, the fan-out
// shape every parallel site in this package uses (CONCURRENCY.tsv rows 5-13).
//
// It is worth its own file because the error semantics are neither of the two
// obvious ones, and getting them wrong is invisible until a deploy half-fails.

package docker

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// chunk is `utils.chunk_list(iterable, size)` (`utils.py:102`).
//
// Python's body is `[iterable] if len(iterable) < size else list_chunks(...)`,
// where `list_chunks` yields successive `size`-element slices. The two branches
// produce the same *content* — a list shorter than `size` is exactly one chunk
// either way — and differ only in the Python type of the single chunk
// (`dict_items` vs `list`), which nothing downstream observes. So one loop
// covers both.
//
// A size of zero or less would spin forever in `list_chunks`; here it degrades
// to a single chunk, because [util.PoolSize] is `cpu_count()` and cannot be
// zero, and a caller that computed one anyway should not hang.
func chunk[T any](items []T, size int) [][]T {
	if len(items) == 0 {
		return nil
	}
	if size < 1 || len(items) <= size {
		return [][]T{items}
	}

	chunks := make([][]T, 0, (len(items)+size-1)/size)
	for start := 0; start < len(items); start += size {
		end := min(start+size, len(items))
		chunks = append(chunks, items[start:end])
	}
	return chunks
}

// runChunked is `with Pool(pool_size) as p: for c in chunk_list(items,
// pool_size): p.map(func, c)`, with the error behaviour that loop actually has.
//
// That behaviour is neither fail-fast nor complete-then-aggregate
// (CONCURRENCY.tsv, "complete-chunk-then-raise-first-arriving"):
//
//  1. Every task of the CURRENT chunk runs to completion. A sibling's failure
//     cancels nothing — `Pool.map` has no cancellation — so a device that has
//     started deploying finishes deploying.
//  2. The first error to ARRIVE is the one returned. Not the first by input
//     order: `map` re-raises whichever exception reached the result queue
//     first, which is completion order.
//  3. Sibling errors are lost. Python discards them and so does this; joining
//     them would change what the user is told.
//  4. Later chunks are never started. The raise escapes the `for chunk` loop.
//
// `errgroup.Group` gives 1 and 2 for free — `Go` records the first error under
// a `sync.Once`, and a Group built WITHOUT `WithContext` cancels no sibling.
// The chunk loop gives 3 and 4. `SetLimit` is redundant with the chunk size and
// is set anyway, so that the limit is stated where a reader looks for it.
//
// ctx is threaded to the task and on to the SDK calls (PORT_SPEC §0.2 #11), so
// a cancelled context fails the in-flight requests — that is the Ctrl-C path.
// What it deliberately does NOT do is cancel siblings on the first *error*,
// which is the distinction CONCURRENCY.tsv's "workers do NOT observe ctx
// cancellation" is drawing.
func runChunked[T any](ctx context.Context, items []T, task func(context.Context, T) error) error {
	size := util.PoolSize()
	for _, c := range chunk(items, size) {
		var group errgroup.Group
		group.SetLimit(size)
		for _, item := range c {
			group.Go(func() error { return task(ctx, item) })
		}
		if err := group.Wait(); err != nil {
			return err
		}
	}
	return nil
}
