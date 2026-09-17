// This file is `multiprocessing.dummy.Pool` + `utils.chunk_list`, the fan-out
// shape every parallel site in this package uses (CONCURRENCY.tsv rows 5-13).
//
// It is worth its own file because the error semantics are neither of the two
// obvious ones, and getting them wrong is invisible until a deploy half-fails.

package docker

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/model"
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
// pool_size): p.map(func, c)`, with the error behaviour ERROR_CODES.md §6
// freezes for that loop.
//
// Python's own behaviour is neither fail-fast nor complete-then-aggregate
// (CONCURRENCY.tsv, "complete-chunk-then-raise-first-arriving"):
//
//  1. Every task of the CURRENT chunk runs to completion. A sibling's failure
//     cancels nothing — `Pool.map` has no cancellation — so a device that has
//     started deploying finishes deploying.
//  2. The first error to ARRIVE is the one raised. Not the first by input
//     order: `map` re-raises whichever exception reached the result queue
//     first, which is completion order — i.e. nondeterministic.
//  3. Sibling errors are lost: `map` discards every exception but that one.
//  4. Later chunks are never started. The raise escapes the `for chunk` loop.
//
// 1 and 4 are ported as they are. 2 and 3 are NOT: ERROR_CODES.md §6.2 replaces
// the nondeterministic pick with the accepted "nondeterministic Python order →
// canonical sorted order" ruling. EVERY failed item's error is collected, the
// batch is ordered by the item's name bytewise ascending, and the errors are
// combined with [errors.Join]. The join's first element is the primary error of
// §6.3 — the one the human `CRITICAL` line and the JSON envelope's `error`
// object render, which is why a total order over the batch is what makes that
// output reproducible — and the whole list becomes the envelope's `errors` key
// (§6.5). A single failure joins to one element, for which §6.5 emits no
// `errors` key at all, so the common case is byte-identical to Python's.
//
// name is the sort key: the device or collision-domain name of the item, per
// §6.2. It is a parameter rather than a method constraint because the items are
// four unrelated types — [model.Machine], [model.Link] and the two API objects
// — only two of which can carry a method.
//
// `errgroup.Group` still gives 1 for free: a Group built WITHOUT `WithContext`
// cancels no sibling, so `Wait` only reports that the chunk failed, while the
// failures themselves are collected under the mutex. `SetLimit` is redundant
// with the chunk size and is set anyway, so that the limit is stated where a
// reader looks for it.
//
// ctx is threaded to the task and on to the SDK calls (PORT_SPEC §0.2 #11), so
// a cancelled context fails the in-flight requests — that is the Ctrl-C path.
// What it deliberately does NOT do is cancel siblings on the first *error*,
// which is the distinction CONCURRENCY.tsv's "workers do NOT observe ctx
// cancellation" is drawing.
func runChunked[T any](ctx context.Context, items []T, name func(T) string, task func(context.Context, T) error) error {
	size := util.PoolSize()
	for _, c := range chunk(items, size) {
		var (
			mu     sync.Mutex
			failed []batchFailure
		)

		var group errgroup.Group
		group.SetLimit(size)
		for _, item := range c {
			group.Go(func() error {
				err := task(ctx, item)
				if err != nil {
					mu.Lock()
					failed = append(failed, batchFailure{name: name(item), err: err})
					mu.Unlock()
				}
				return err
			})
		}
		if group.Wait() != nil {
			return joinFailures(failed)
		}
	}
	return nil
}

// batchFailure is one failed item of a chunk, tagged with the name that orders
// it (ERROR_CODES.md §6.2).
type batchFailure struct {
	name string
	err  error
}

// joinFailures is §6.2's canonical order plus the join itself.
//
// The sort is bytewise on the item name and STABLE, so two items that somehow
// share a name keep their input order instead of swapping between runs; input
// order is the scenario's own, which is already deterministic.
func joinFailures(failed []batchFailure) error {
	slices.SortStableFunc(failed, func(a, b batchFailure) int {
		return strings.Compare(a.name, b.name)
	})

	errs := make([]error, len(failed))
	for i, f := range failed {
		errs[i] = f.err
	}
	return errors.Join(errs...)
}

// The four name accessors [runChunked]'s callers pass, one per item type.
//
// The API-object ones read the `name` label this package puts on everything it
// creates — the Kathará device or collision-domain name, which is what §6.2
// orders by — and fall back to the object's own (mangled) name for an object
// that carries no such label, so that the order stays total.

func machineItemName(m *model.Machine) string { return m.Name }

func linkItemName(l *model.Link) string { return l.Name }

func containerItemName(c *Container) string {
	if name := c.Label(labelName); name != "" {
		return name
	}
	return c.Name()
}

func networkItemName(n *Network) string {
	if name := n.Label(labelName); name != "" {
		return name
	}
	return n.Name()
}
