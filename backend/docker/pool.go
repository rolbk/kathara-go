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

type batchFailure struct {
	name string
	err  error
}

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
