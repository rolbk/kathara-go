// This file replaces `foundation/factory/Factory.py` and
// `foundation/manager/ManagerFactory.py` (PORT_SPEC §0.2 #7): the two classes
// that turned the string "docker" into a dotted module path, imported it and
// called getattr on it.
//
// What goes away with them is one error-discrimination trick and one bug. The
// trick is `Factory.get_class`'s test `e.name == "%s.%s" % (module, class)`,
// which separated "no such backend" from "backend's dependency is missing" by
// string-comparing an ImportError's module path; the bug is the `raise
// ImportError from e` right after it, which builds a *fresh, argument-less*
// ImportError whose `.name` is None, so the entrypoint's "`{e.name}` is not
// installed in your system" prints "`None`"
// (analysis/manager-foundation.md §7 gotchas 1-2). A registry has neither
// path: a name is present or it is not.

package kathara

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// The two ways a [Registry.Register] call can be wrong. Neither is reachable
// from a Python program: registration is wiring that `cmd/kathara` does once,
// and a mistake in it is a build's mistake and not a user's. They carry no
// taxonomy code, so `kerrors.Code` buckets them to `InternalError`, which
// ERROR_CODES.md §1.2 makes the exhaustive fallback.
var (
	// ErrInvalidBackend is a [Backend] with no name or no constructor.
	ErrInvalidBackend = errors.New("kathara: invalid backend registration")

	// ErrDuplicateBackend is a second registration under a name already
	// taken.
	ErrDuplicateBackend = errors.New("kathara: backend already registered")
)

// Factory constructs a backend. It is what `ManagerFactory().create_instance()`
// did, minus the reflection.
//
// It is called once, from [NewClient], and it is allowed — expected, even — to
// be effectful: the Docker factory opens a client onto the daemon and checks
// the network plugin, the Kubernetes one loads the kubeconfig. `Kathara.__init__`
// did the same work at the same moment, and PORT_SPEC §9's goldens encode
// *when* a daemon-down error appears relative to the rest of a command
// (analysis/manager-foundation.md §7 gotcha 9), so a factory must not defer it
// to the first operation.
type Factory func(ctx context.Context, cfg Config) (Manager, error)

// Backend is one row of the registry: everything `cmd/kathara` has to say
// about a backend in order to make it selectable.
type Backend struct {
	// Name is the `manager_type` value that selects it — "docker",
	// "kubernetes". It is compared exactly and case-sensitively, which is
	// what makes a `manager_type` of "DOCKER" load out of the config file and
	// then fail (`settings.Settings.CheckManager`).
	Name string

	// FormattedName is `get_formatted_manager_name()`: "Docker (Kathara)",
	// "Kubernetes (Megalos)".
	//
	// It is here, and not read off a constructed [Manager], because
	// `Kathara.get_available_managers_name()` resolves the *class* and never
	// instantiates: listing the backends in the settings screen must not
	// connect to a Docker daemon (analysis/manager-foundation.md §7 gotcha
	// 10). [Registry.Available] answers from this field.
	FormattedName string

	// New builds the manager.
	New Factory
}

// ManagerInfo is one entry of the `Dict[str, str]` that
// `Kathara.get_available_managers_name()` returns — a manager name and its
// display name.
//
// Python's dict is insertion-ordered and the insertion order is
// `AVAILABLE_MANAGERS`, which the settings screen shows to the user
// (SYNTHESIS.md C-5, ORDERING.tsv row `manager/Kathara.py:617`). A Go map has
// no order, so the listing is a slice.
type ManagerInfo struct {
	// Name is the `manager_type` value, the dict's key.
	Name string

	// FormattedName is the dict's value.
	FormattedName string
}

// Registry is the explicit backend table that replaces the reflection
// (PORT_SPEC §0.2 #7).
//
// Order is declared, not discovered: entries come back in registration order,
// and `cmd/kathara` registers docker then kubernetes, which is
// `AVAILABLE_MANAGERS`' order. There is no `init()`-time self-registration —
// PACKAGE_GRAPH.md §1.2 rules it out precisely because import order is not a
// declared order, and because the `//go:build nok8s` build has to be able to
// leave `client-go` out of the binary by not naming the backend (PORT_SPEC
// §0.2 #8).
//
// A Registry is safe for concurrent use. In practice everything registers on
// the main goroutine before anything reads, but [DefaultRegistry] is package
// state and a test that registers a fake into its own Registry should not have
// to reason about what another test is doing to that one.
type Registry struct {
	mu       sync.RWMutex
	order    []string
	backends map[string]Backend
}

// NewRegistry returns an empty registry. Use it in tests and in embedders that
// would rather not share [DefaultRegistry]; [NewClient] takes one through
// [WithRegistry].
func NewRegistry() *Registry {
	return &Registry{backends: make(map[string]Backend)}
}

// DefaultRegistry is the package-level table the package-level [Register] and
// [AvailableManagers] operate on, and the one [NewClient] uses when no other is
// given.
var DefaultRegistry = NewRegistry()

// Register adds a backend. The registration order is the listing order, so the
// order of the calls in `cmd/kathara/backends_all.go` is a contract and not a
// detail.
//
// It fails rather than panicking on a bad call — an empty name, a nil
// constructor, a name already taken — because a panic here would be a panic
// during process startup, and the caller can report a duplicate far better
// than a stack trace can.
func (r *Registry) Register(b Backend) error {
	if b.Name == "" {
		return fmt.Errorf("%w: empty name", ErrInvalidBackend)
	}
	if b.New == nil {
		return fmt.Errorf("%w: %q has no constructor", ErrInvalidBackend, b.Name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.backends[b.Name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateBackend, b.Name)
	}
	r.backends[b.Name] = b
	r.order = append(r.order, b.Name)
	return nil
}

// Lookup returns the backend registered under name. The comparison is exact:
// `manager_type.capitalize()` lowercased everything after the first character
// in Python and would have turned "myBackend" into "Mybackend", which is a
// mangling nothing here reproduces because nothing here needs a class name
// (analysis/manager-foundation.md §7 gotcha 3).
func (r *Registry) Lookup(name string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.backends[name]
	return b, ok
}

// Available is `Kathara.get_available_managers_name()`: every registered
// backend, in declared order, without constructing any of them.
//
// The returned slice is a fresh copy; mutating it cannot reorder the registry.
func (r *Registry) Available() []ManagerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	infos := make([]ManagerInfo, 0, len(r.order))
	for _, name := range r.order {
		infos = append(infos, ManagerInfo{Name: name, FormattedName: r.backends[name].FormattedName})
	}
	return infos
}

// Names is [Registry.Available] reduced to the `manager_type` values, in the
// same declared order — the Go shape of `AVAILABLE_MANAGERS` as this build
// actually knows it.
//
// It is not the same list as `settings.AvailableManagers()`, and the gap is
// the point: that one is the frozen config-schema vocabulary, ["docker",
// "kubernetes"], and this one is what the binary can really run. A `nok8s`
// build accepts `manager_type: kubernetes` in the file and then answers
// [ErrSettings] from [NewClient].
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Clone(r.order)
}

// Register adds a backend to [DefaultRegistry]. See [Registry.Register].
func Register(b Backend) error { return DefaultRegistry.Register(b) }

// AvailableManagers is `Kathara.get_available_managers_name()` over
// [DefaultRegistry]. See [Registry.Available].
func AvailableManagers() []ManagerInfo { return DefaultRegistry.Available() }
