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
	"errors"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/model"
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
// pool_size): p.map(func, c)`, with the error behaviour ERROR_CODES.md §6
// freezes for that loop. It is the Docker backend's [docker.runChunked] to the
// letter — see the file header for why it is spelled out twice.
//
// Python's own behaviour is neither fail-fast nor complete-then-aggregate
// (CONCURRENCY.tsv, "complete-chunk-then-raise-first-arriving"):
//
//  1. Every task of the CURRENT chunk runs to completion. A sibling's failure
//     cancels nothing — `Pool.map` has no cancellation — so a device that has
//     started deploying finishes deploying.
//  2. The first error to ARRIVE is the one raised; not the first by input
//     order, since `map` re-raises whichever exception reached the result queue
//     first — i.e. nondeterministic.
//  3. Sibling errors are lost: `map` discards every exception but that one.
//  4. Later chunks are never started. The raise escapes the `for chunk` loop.
//
// 1 and 4 are ported as they are. 2 and 3 are NOT: ERROR_CODES.md §6.2 replaces
// the nondeterministic pick with the accepted "nondeterministic Python order →
// canonical sorted order" ruling. EVERY failed item's error is collected, the
// batch is ordered by the item's name bytewise ascending, and the errors are
// combined with [errors.Join]. The join's first element is the primary error of
// §6.3 — the one the human `CRITICAL` line and the JSON envelope's `error`
// object render — and the whole list becomes the envelope's `errors` key
// (§6.5). A single failure joins to one element, for which §6.5 emits no
// `errors` key, so the common case is byte-identical to Python's.
//
// name is the sort key: the device or collision-domain name of the item, per
// §6.2. It is a parameter rather than a method constraint because the items are
// four unrelated types — [model.Machine], [model.Link], [corev1.Pod] and
// [Network] — none of which this package may give a method to.
//
// `errgroup.Group` still gives 1 for free: a Group built WITHOUT `WithContext`
// cancels no sibling, so `Wait` only reports that the chunk failed, while the
// failures themselves are collected under the mutex.
//
// ctx is threaded to the task and on to the API calls (PORT_SPEC §0.2 #11), so
// a cancelled context fails the in-flight requests — that is the Ctrl-C path,
// and on this backend also the 180 s startup-watchdog path
// ([machineService.DeployMachines]). What it deliberately does NOT do is cancel
// siblings on the first *error*.
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
// orders by — and fall back to the object's own (mangled) Kubernetes name for
// an object that carries no such label, so that the order stays total.

func machineItemName(m *model.Machine) string { return m.Name }

func linkItemName(l *model.Link) string { return l.Name }

func podItemName(p *corev1.Pod) string {
	if name := p.Labels[labelName]; name != "" {
		return name
	}
	return p.Name
}

func networkItemName(n *Network) string {
	if name := NetworkLinkName(n); name != "" {
		return name
	}
	return NetworkNameOf(n)
}
