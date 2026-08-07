// This file is `multiprocessing.dummy.Pool` + `utils.chunk_list`, the fan-out
// shape every parallel site in this package uses (CONCURRENCY.tsv rows 15, 18,
// 19, 22, 23).
//
// It is the same shape the Docker backend uses and it is spelled out again here
// rather than shared, because PACKAGE_GRAPH.md §1.2 gives this package no edge
// to `backend/docker` — the two backends are separately importable so that a
// `nok8s` build can drop `client-go` (PORT_SPEC §0.2 #8). The error semantics
// are neither of the two obvious ones and getting them wrong is invisible until
// a deploy half-fails, so the reasoning is repeated with it.

package kubernetes

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// chunk is `utils.chunk_list(iterable, size)` (`utils.py:102`).
//
// Python's body is `[iterable] if len(iterable) < size else list_chunks(...)`,
// where `list_chunks` yields successive `size`-element slices. The two branches
// produce the same content — a list shorter than `size` is exactly one chunk
// either way — and differ only in the Python type of the single chunk, which
// nothing downstream observes.
//
// A size of zero or less would spin forever in `list_chunks`; here it degrades
// to a single chunk, because [util.PoolSize] is `cpu_count()` and cannot be
// zero.
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
//  2. The first error to ARRIVE is the one returned; not the first by input
//     order, since `map` re-raises whichever exception reached the result queue
//     first.
//  3. Sibling errors are lost. Python discards them and so does this; joining
//     them would change what the user is told.
//  4. Later chunks are never started. The raise escapes the `for chunk` loop.
//
// `errgroup.Group` gives 1 and 2 for free — `Go` records the first error under
// a `sync.Once`, and a Group built WITHOUT `WithContext` cancels no sibling.
// The chunk loop gives 3 and 4.
//
// ctx is threaded to the task and on to the API calls (PORT_SPEC §0.2 #11), so
// a cancelled context fails the in-flight requests — that is the Ctrl-C path,
// and on this backend also the 180 s startup-watchdog path
// ([machineService.DeployMachines]). What it deliberately does NOT do is cancel
// siblings on the first *error*.
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
