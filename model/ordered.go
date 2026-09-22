package model

import "slices"

// OrderedMap is a Python dict: insertion-ordered, with re-assignment of an
// existing key keeping that key's original position.
type OrderedMap[K comparable, V any] struct {
	keys   []K
	values map[K]V
}

// Entry is one key/value pair of an [OrderedMap], in iteration order.
type Entry[K comparable, V any] struct {
	Key   K
	Value V
}

// NewOrderedMap returns an empty map.
func NewOrderedMap[K comparable, V any]() *OrderedMap[K, V] {
	return &OrderedMap[K, V]{values: make(map[K]V)}
}

// Len is len(d).
func (o *OrderedMap[K, V]) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Get is d[key], reporting whether the key is present.
func (o *OrderedMap[K, V]) Get(key K) (V, bool) {
	if o == nil {
		var zero V
		return zero, false
	}
	v, ok := o.values[key]
	return v, ok
}

// Has is `key in d`.
func (o *OrderedMap[K, V]) Has(key K) bool {
	_, ok := o.Get(key)
	return ok
}

// Set is d[key] = value. It returns the previous value and whether there was
// one, which is what `Machine.add_meta` returns to its caller.
func (o *OrderedMap[K, V]) Set(key K, value V) (V, bool) {
	prev, existed := o.values[key]
	if !existed {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return prev, existed
}

// Delete is `del d[key]`, reporting whether the key was there.
func (o *OrderedMap[K, V]) Delete(key K) bool {
	if o == nil {
		return false
	}
	if _, ok := o.values[key]; !ok {
		return false
	}
	delete(o.values, key)
	o.keys = slices.DeleteFunc(o.keys, func(k K) bool { return k == key })
	return true
}

// Keys returns the keys in insertion order. The result is a fresh slice.
func (o *OrderedMap[K, V]) Keys() []K {
	if o == nil {
		return nil
	}
	return slices.Clone(o.keys)
}

// Values returns the values in key insertion order.
func (o *OrderedMap[K, V]) Values() []V {
	if o == nil {
		return nil
	}
	out := make([]V, 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, o.values[k])
	}
	return out
}

// Entries returns the pairs in insertion order — the `.items()` of the Python
// dict, and the only safe way to iterate one of these maps.
func (o *OrderedMap[K, V]) Entries() []Entry[K, V] {
	if o == nil {
		return nil
	}
	out := make([]Entry[K, V], 0, len(o.keys))
	for _, k := range o.keys {
		out = append(out, Entry[K, V]{Key: k, Value: o.values[k]})
	}
	return out
}

// SortStableFunc reorders the map in place, keeping equal elements in their
// current relative order. It is `Lab.apply_dependencies`'s
// `OrderedDict(sorted(items, key=…))` (`model/Lab.py:252`), whose stability is
// what puts the machines absent from lab.dep first, in insertion order.
func (o *OrderedMap[K, V]) SortStableFunc(cmp func(a, b Entry[K, V]) int) {
	if o == nil {
		return
	}
	entries := o.Entries()
	slices.SortStableFunc(entries, cmp)
	for i, e := range entries {
		o.keys[i] = e.Key
	}
}
