package labfile

import (
	"slices"

	"github.com/KatharaFramework/kathara-go/model"
)

// DepGraph is `depgen`'s dependency dictionary: a device name to the devices it
// depends on, in the order lab.dep listed them.
//
// It is ordered because the ORDER IS THE OUTPUT. [Flatten] is not a canonical
// topological sort — within one dependency level it emits names in lab.dep line
// order, so the same graph written `d: b c` and `d: c b` produces two different
// device deploy orders (RULINGS.md OQ-15b, README SURPRISE 19). A Go rewrite
// built on a map would be non-deterministic where Python is merely fragile.
type DepGraph = model.OrderedMap[string, []string]

// NewDepGraph returns an empty dependency graph.
func NewDepGraph() *DepGraph { return model.NewOrderedMap[string, []string]() }

// HasLoop is `depgen.has_loop` (`trdparty/depgen/depgen.py:88`): whether any
// dependency chain revisits a device.
//
// The walk starts from each dependency VALUE with an empty seen-list — never
// from the key — which is why a self-dependency `pc1: pc1` is still found: the
// walk begins at pc1 and meets it again one step later.
//
// It terminates on every input, cyclic ones included, because a repeat on the
// current path returns immediately: the recursion is bounded by the number of
// distinct device names.
func HasLoop(dependencies *DepGraph) bool {
	for _, entry := range dependencies.Entries() {
		for _, val := range entry.Value {
			if hasLoopFrom(dependencies, nil, val) {
				return true
			}
		}
	}
	return false
}

// hasLoopFrom is the recursive half, with `seen` copied per call the way
// Python's `seen=list(seen)` copies it.
func hasLoopFrom(dependencies *DepGraph, seen []string, val string) bool {
	if slices.Contains(seen, val) {
		return true
	}
	seen = append(seen, val)

	next, _ := dependencies.Get(val)
	for _, dep := range next {
		if hasLoopFrom(dependencies, slices.Clone(seen), dep) {
			return true
		}
	}
	return false
}

// Flatten is `depgen.flatten` (`trdparty/depgen/depgen.py:65`): the device
// names in an order that satisfies the dependencies.
//
// The algorithm is Python's, three steps and no shortcuts, because the result
// depends on all three: invert the graph, compute each name's greatest depth
// from a root, then group by depth and emit the groups in ascending order. The
// grouping preserves the order the depths were first assigned in, which is
// lab.dep line order — see [DepGraph].
//
// The result is never nil; an empty graph flattens to an empty slice, which is
// what a comments-only lab.dep produces.
//
// PORT DIVERGENCE (DIVERGENCES.md 46): a cyclic graph makes Python recurse
// until it raises RecursionError, and would make this exhaust the goroutine
// stack — a fatal error PORT_SPEC §10 forbids. The depth walk therefore refuses
// to re-enter a name already on the current path. [ParseDep] calls [HasLoop]
// first, so the guard is unreachable from any file; it exists because this
// function is exported.
func Flatten(dependencies *DepGraph) []string {
	byDepth := invertOrder(orderAll(invertDeps(dependencies)))

	depths := byDepth.Keys()
	slices.Sort(depths)

	output := []string{}
	for _, depth := range depths {
		names, _ := byDepth.Get(depth)
		output = append(output, names...)
	}
	return output
}

// invertDeps is `depgen._invert` over the dependency dictionary: `a: [b, c]`
// becomes `b: [a]`, `c: [a]`.
func invertDeps(dependencies *DepGraph) *DepGraph {
	inverted := NewDepGraph()
	for _, entry := range dependencies.Entries() {
		for _, dep := range entry.Value {
			dependents, _ := inverted.Get(dep)
			inverted.Set(dep, append(dependents, entry.Key))
		}
	}
	return inverted
}

// invertOrder is `depgen._invert` over the depth map, whose values are ints
// rather than lists: `a: 2` becomes `2: [a]`.
func invertOrder(order *model.OrderedMap[string, int]) *model.OrderedMap[int, []string] {
	inverted := model.NewOrderedMap[int, []string]()
	for _, entry := range order.Entries() {
		names, _ := inverted.Get(entry.Value)
		inverted.Set(entry.Value, append(names, entry.Key))
	}
	return inverted
}

// orderAll is `depgen._order` with `val=None`: the entry point, which seeds
// every key with depth 0 and then merges in the depths reachable from each of
// its dependents.
//
// `results.setdefault(k, 0)` sits INSIDE the inner loop, so a key with an empty
// dependent list never gets a 0 — which is exactly why it is spelled that way
// here too.
func orderAll(inverted *DepGraph) *model.OrderedMap[string, int] {
	results := model.NewOrderedMap[string, int]()
	for _, entry := range inverted.Entries() {
		for _, dep := range entry.Value {
			if !results.Has(entry.Key) {
				results.Set(entry.Key, 0)
			}
			mergeMax(results, orderFrom(inverted, dep, 1, nil))
		}
	}
	return results
}

// orderFrom is `depgen._order` with a `val`: the depth of val and of everything
// below it, each name keeping its GREATEST depth.
func orderFrom(inverted *DepGraph, val string, level int, path []string) *model.OrderedMap[string, int] {
	results := model.NewOrderedMap[string, int]()
	results.Set(val, level)

	deps, ok := inverted.Get(val)
	if !ok || len(deps) == 0 {
		return results
	}

	path = append(slices.Clone(path), val)
	for _, dep := range deps {
		// The cycle guard of [Flatten]'s doc comment.
		if slices.Contains(path, dep) {
			continue
		}
		mergeMax(results, orderFrom(inverted, dep, level+1, path))
	}
	return results
}

// mergeMax is `if dv > results.get(dk, 0): results[dk] = dv`, the merge both
// halves of `_order` use. A missing key counts as 0, so a depth of 0 never
// creates an entry.
func mergeMax(results *model.OrderedMap[string, int], other *model.OrderedMap[string, int]) {
	for _, entry := range other.Entries() {
		if current, _ := results.Get(entry.Key); entry.Value > current {
			results.Set(entry.Key, entry.Value)
		}
	}
}
