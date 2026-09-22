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
