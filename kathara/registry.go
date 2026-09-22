package kathara

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

// The two ways a [Registry.Register] call can be wrong.
var (
	// ErrInvalidBackend is a [Backend] with no name or no constructor.
	ErrInvalidBackend = errors.New("kathara: invalid backend registration")

	// ErrDuplicateBackend is a second registration under a name already
	// taken.
	ErrDuplicateBackend = errors.New("kathara: backend already registered")
)

// Factory constructs a backend. It is what `ManagerFactory().create_instance()`
// did, minus the reflection.
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
	FormattedName string

	// New builds the manager.
	New Factory
}

// ManagerInfo is one entry of the `Dict[str, str]` that
// `Kathara.get_available_managers_name()` returns — a manager name and its
// display name.
type ManagerInfo struct {
	// Name is the `manager_type` value, the dict's key.
	Name string

	// FormattedName is the dict's value.
	FormattedName string
}

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

// Register adds a backend. Registration order is also listing order.
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

func (r *Registry) Lookup(name string) (Backend, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	b, ok := r.backends[name]
	return b, ok
}

// Available is `Kathara.get_available_managers_name()`: every registered
// backend, in declared order, without constructing any of them.
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
