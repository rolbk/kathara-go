package kathara

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/KatharaFramework/kathara-go/settings"
)

// mustRegister registers b or fails the test. Registration failures are wiring
// bugs, and a test that swallows one would go on to assert something else.
func mustRegister(t *testing.T, r *Registry, b Backend) {
	t.Helper()
	if err := r.Register(b); err != nil {
		t.Fatalf("Register(%q): %v", b.Name, err)
	}
}

// stubBackend is a registrable [Backend] whose factory answers a bare fake.
func stubBackend(name, formatted string) Backend {
	return Backend{
		Name:          name,
		FormattedName: formatted,
		New: func(context.Context, Config) (Manager, error) {
			return &fakeManager{wantName: formatted}, nil
		},
	}
}

// TestRegistryKeepsDeclaredOrder is SYNTHESIS.md C-5 and ORDERING.tsv row
// `manager/Kathara.py:617`: `AVAILABLE_MANAGERS = ["docker", "kubernetes"]` is
// not dead code, it is the order `Kathara.get_available_managers_name()` builds
// its dict in, and that dict's order is what the settings screen shows.
//
// Registration order is the listing order. Nothing sorts, and nothing ranges
// over the map.
func TestRegistryKeepsDeclaredOrder(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	mustRegister(t, r, stubBackend("docker", "Docker (Kathara)"))
	mustRegister(t, r, stubBackend("kubernetes", "Kubernetes (Megalos)"))

	want := []ManagerInfo{
		{Name: "docker", FormattedName: "Docker (Kathara)"},
		{Name: "kubernetes", FormattedName: "Kubernetes (Megalos)"},
	}
	if got := r.Available(); !slices.Equal(got, want) {
		t.Errorf("Available() = %+v, want %+v", got, want)
	}
	if got := r.Names(); !slices.Equal(got, []string{"docker", "kubernetes"}) {
		t.Errorf("Names() = %q, want [docker kubernetes]", got)
	}

	// The declared order is the *registration* order and not an alphabetical
	// accident: registering the other way round must list the other way round.
	reversed := NewRegistry()
	mustRegister(t, reversed, stubBackend("kubernetes", "Kubernetes (Megalos)"))
	mustRegister(t, reversed, stubBackend("docker", "Docker (Kathara)"))
	if got := reversed.Names(); !slices.Equal(got, []string{"kubernetes", "docker"}) {
		t.Errorf("Names() = %q, want [kubernetes docker]", got)
	}
}

// TestRegistryOrderMatchesSettingsVocabulary ties the two lists together: the
// order `cmd/kathara` is expected to register in is the order the frozen
// settings schema declares (SYNTHESIS.md C-5 — `AVAILABLE_MANAGERS` lives in
// `Setting.py` and is iterated by the facade).
func TestRegistryOrderMatchesSettingsVocabulary(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	for _, name := range settings.AvailableManagers() {
		mustRegister(t, r, stubBackend(name, name))
	}
	if got := r.Names(); !slices.Equal(got, settings.AvailableManagers()) {
		t.Errorf("Names() = %q, want %q", got, settings.AvailableManagers())
	}
}

// TestRegistryLookup pins the two things that replace `Factory.get_class`'s
// dotted-path resolution: an exact, case-sensitive name match, and a plain
// "not found" instead of the ImportError/ClassNotFoundError discrimination.
func TestRegistryLookup(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	mustRegister(t, r, stubBackend("docker", "Docker (Kathara)"))

	tests := []struct {
		name  string
		want  bool
		about string
	}{
		{name: "docker", want: true},
		// `manager_type.capitalize()` lowercased everything after the first
		// character in Python; the registry does no case folding at all, so a
		// case-mangled value that loads out of the config file finds nothing
		// (analysis/manager-foundation.md §7 gotcha 3).
		{name: "DOCKER", want: false},
		{name: "Docker", want: false},
		{name: "kubernetes", want: false},
		{name: "podman", want: false},
		{name: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := r.Lookup(tt.name); ok != tt.want {
				t.Errorf("Lookup(%q) found = %v, want %v", tt.name, ok, tt.want)
			}
		})
	}
}

// TestRegistryRejectsBadRegistrations covers the wiring mistakes. None of them
// is reachable from a Python program, so none of them panics: `cmd/kathara`
// reports a duplicate far better than a stack trace does.
func TestRegistryRejectsBadRegistrations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		backend Backend
		wantErr error
	}{
		{
			name:    "no name",
			backend: Backend{FormattedName: "x", New: func(context.Context, Config) (Manager, error) { return nil, nil }},
			wantErr: ErrInvalidBackend,
		},
		{
			name:    "no constructor",
			backend: Backend{Name: "docker", FormattedName: "Docker (Kathara)"},
			wantErr: ErrInvalidBackend,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if err := NewRegistry().Register(tt.backend); !errors.Is(err, tt.wantErr) {
				t.Errorf("Register = %v, want %v", err, tt.wantErr)
			}
		})
	}

	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()

		r := NewRegistry()
		mustRegister(t, r, stubBackend("docker", "Docker (Kathara)"))
		if err := r.Register(stubBackend("docker", "Something Else")); !errors.Is(err, ErrDuplicateBackend) {
			t.Errorf("Register = %v, want ErrDuplicateBackend", err)
		}
		// The failed registration left nothing behind.
		if got := r.Names(); !slices.Equal(got, []string{"docker"}) {
			t.Errorf("Names() = %q, want [docker]", got)
		}
		if b, _ := r.Lookup("docker"); b.FormattedName != "Docker (Kathara)" {
			t.Errorf("FormattedName = %q, want the first registration's", b.FormattedName)
		}
	})
}

// TestRegistryListingDoesNotConstruct is
// analysis/manager-foundation.md §7 gotcha 10: `get_available_managers_name()`
// resolves the manager *class* and never instantiates it, so listing the
// backends in the settings screen must not open a Docker connection. That is
// why [Backend.FormattedName] is declared alongside the factory instead of
// being read off a constructed [Manager].
func TestRegistryListingDoesNotConstruct(t *testing.T) {
	t.Parallel()

	var built int
	r := NewRegistry()
	mustRegister(t, r, Backend{
		Name:          "docker",
		FormattedName: "Docker (Kathara)",
		New: func(context.Context, Config) (Manager, error) {
			built++
			return &fakeManager{}, nil
		},
	})

	for range 3 {
		_ = r.Available()
		_ = r.Names()
		_, _ = r.Lookup("docker")
	}
	if built != 0 {
		t.Errorf("factory ran %d times while listing, want 0", built)
	}
}

// TestRegistryReturnsCopies keeps a caller from reordering the registry by
// sorting the slice it was handed — the listing order is a contract.
func TestRegistryReturnsCopies(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	mustRegister(t, r, stubBackend("docker", "Docker (Kathara)"))
	mustRegister(t, r, stubBackend("kubernetes", "Kubernetes (Megalos)"))

	names := r.Names()
	slices.Reverse(names)
	infos := r.Available()
	slices.Reverse(infos)

	if got := r.Names(); !slices.Equal(got, []string{"docker", "kubernetes"}) {
		t.Errorf("Names() = %q after a caller reversed its copy", got)
	}
	if got := r.Available(); got[0].Name != "docker" {
		t.Errorf("Available()[0] = %q after a caller reversed its copy", got[0].Name)
	}
}

// TestRegistryIsConcurrencySafe exists for the race detector. Registration is a
// startup-only act in practice, but [DefaultRegistry] is package state and a
// test that registers into it must not be able to tear another one down.
func TestRegistryIsConcurrencySafe(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	var wg sync.WaitGroup
	for _, name := range []string{"docker", "kubernetes", "podman", "containerd"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Register(stubBackend(name, name))
			_ = r.Available()
			_ = r.Names()
			_, _ = r.Lookup("docker")
		}()
	}
	wg.Wait()

	if got := len(r.Names()); got != 4 {
		t.Errorf("registered %d backends, want 4", got)
	}
}

// TestDefaultRegistryIsWiredToThePackageFunctions checks that the package-level
// [Register] and [AvailableManagers] — the ones `cmd/kathara` calls — reach
// [DefaultRegistry] and not a copy.
func TestDefaultRegistryIsWiredToThePackageFunctions(t *testing.T) {
	// Not parallel: it mutates package state.
	name := "kathara_test_backend"
	if err := Register(stubBackend(name, "Test Backend")); err != nil {
		t.Fatalf("Register: %v", err)
	}
	t.Cleanup(func() {
		DefaultRegistry.mu.Lock()
		defer DefaultRegistry.mu.Unlock()
		delete(DefaultRegistry.backends, name)
		DefaultRegistry.order = slices.DeleteFunc(DefaultRegistry.order, func(s string) bool { return s == name })
	})

	if _, ok := DefaultRegistry.Lookup(name); !ok {
		t.Fatal("package-level Register did not reach DefaultRegistry")
	}
	if !slices.ContainsFunc(AvailableManagers(), func(i ManagerInfo) bool { return i.Name == name }) {
		t.Error("package-level AvailableManagers did not reach DefaultRegistry")
	}
}
