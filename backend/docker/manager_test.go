package docker

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// TestBackendRow is the registry entry `cmd/kathara` registers. The two strings
// are user-visible: `manager_type: docker` selects the backend and
// "Docker (Kathara)" is what the settings screen shows.
func TestBackendRow(t *testing.T) {
	backend := Backend()
	if backend.Name != "docker" {
		t.Errorf("Name = %q, want \"docker\"", backend.Name)
	}
	if backend.FormattedName != "Docker (Kathara)" {
		t.Errorf("FormattedName = %q", backend.FormattedName)
	}
	if backend.New == nil {
		t.Error("New is nil; the registry rejects a row without a constructor")
	}

	// `get_formatted_manager_name` answers the same string from an instance.
	if got := (&Manager{}).GetFormattedManagerName(); got != backend.FormattedName {
		t.Errorf("GetFormattedManagerName = %q, want %q", got, backend.FormattedName)
	}
}

// TestResolveRequired is the two lines that open eleven `DockerManager`
// methods: the exactly-one guard, then the object-wins-over-name dispatch.
func TestResolveRequired(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())

	tests := []struct {
		name    string
		ref     kathara.LabRef
		want    string
		wantErr bool
	}{
		{"a hash is used as given", kathara.LabRef{Hash: "abc"}, "abc", false},
		{"a name is hashed", kathara.LabRef{Name: "Default scenario"}, fixtureHash, false},
		{"an object contributes its hash", kathara.LabRef{Lab: lab}, fixtureHash, false},
		{"nothing at all is an error", kathara.LabRef{}, "", true},
		{"two of them is an error", kathara.LabRef{Hash: "abc", Lab: lab}, "", true},
		{"name and hash together is an error", kathara.LabRef{Hash: "abc", Name: "x"}, "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRequired(tt.ref)
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrInvocation) {
					t.Fatalf("err = %v, want an InvocationError", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRequired: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveRequired = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveRequiredMessages(t *testing.T) {
	_, err := resolveRequired(kathara.LabRef{})
	if err == nil || err.Error() != "You must specify a parameter among lab_hash, lab_name, lab" {
		t.Errorf("no-ref message = %v", err)
	}

	_, err = resolveRequired(kathara.LabRef{Hash: "a", Name: "b"})
	if err == nil || err.Error() != "You must specify only a parameter among lab_hash, lab_name, lab" {
		t.Errorf("two-ref message = %v", err)
	}
}

// TestResolveAtMostOne is the plural getters' guard: none at all is legal and
// means "every scenario of the selected users", which is an unfiltered hash.
func TestResolveAtMostOne(t *testing.T) {
	got, err := resolveAtMostOne(kathara.LabRef{})
	if err != nil {
		t.Fatalf("an empty ref was rejected: %v", err)
	}
	if got != "" {
		t.Errorf("resolveAtMostOne = %q, want the unfiltered empty hash", got)
	}

	if _, err := resolveAtMostOne(kathara.LabRef{Hash: "a", Name: "b"}); !errors.Is(err, kerrors.ErrInvocation) {
		t.Errorf("two refs gave %v, want an InvocationError", err)
	}
}

// TestResolveHashPrefersTheObject is Python's `if lab: … elif lab_name: …`
// order — a LabRef carrying both is rejected by the guards above, so this only
// pins the dispatch a caller reaches through `get_lab_from_api`, where
// lab_name wins over lab_hash and nothing errors.
func TestResolveHashPrefersTheObject(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	if got := resolveHash(kathara.LabRef{Lab: lab, Name: "other", Hash: "raw"}); got != fixtureHash {
		t.Errorf("resolveHash = %q, want the object's hash", got)
	}
	if got := resolveHash(kathara.LabRef{Name: "Default scenario", Hash: "raw"}); got != fixtureHash {
		t.Errorf("resolveHash = %q, want the name's hash", got)
	}
	if got := resolveHash(kathara.LabRef{Hash: "raw"}); got != "raw" {
		t.Errorf("resolveHash = %q, want the raw hash", got)
	}
}

// TestScopedUser is `get_current_user_name() if not all_users else None`: the
// empty string is that None, and [ObjectFilters] then drops the term.
func TestScopedUser(t *testing.T) {
	all, err := scopedUser(true)
	if err != nil {
		t.Fatalf("scopedUser(true): %v", err)
	}
	if all != "" {
		t.Errorf("scopedUser(true) = %q, want the empty (unfiltered) user", all)
	}

	one, err := scopedUser(false)
	if err != nil {
		t.Fatalf("scopedUser(false): %v", err)
	}
	if one == "" {
		t.Error("scopedUser(false) produced no user name")
	}
}

func TestFilterMachines(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	for _, name := range []string{"pc1", "pc2", "pc3"} {
		if _, err := lab.NewMachine(name, nil); err != nil {
			t.Fatalf("NewMachine %s: %v", name, err)
		}
	}
	machines := lab.Machines()

	names := func(in []*model.Machine) []string {
		out := make([]string, 0, len(in))
		for _, m := range in {
			out = append(out, m.Name)
		}
		return out
	}

	tests := []struct {
		name               string
		selected, excluded kathara.NameSet
		want               []string
	}{
		{"no filters", nil, nil, []string{"pc1", "pc2", "pc3"}},
		{"an EMPTY selected set deploys everything", kathara.NewNameSet(), nil, []string{"pc1", "pc2", "pc3"}},
		{"an EMPTY excluded set deploys everything", nil, kathara.NewNameSet(), []string{"pc1", "pc2", "pc3"}},
		{"selected narrows, in scenario order", kathara.NewNameSet("pc3", "pc1"), nil, []string{"pc1", "pc3"}},
		{"excluded removes", nil, kathara.NewNameSet("pc2"), []string{"pc1", "pc3"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := names(filterMachines(machines, tt.selected, tt.excluded)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterMachines = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFilterLinks is the same comprehension for collision domains.
func TestFilterLinks(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	for _, name := range []string{"A", "B", "C"} {
		lab.GetOrNewLink(name)
	}

	names := func(in []*model.Link) []string {
		out := make([]string, 0, len(in))
		for _, l := range in {
			out = append(out, l.Name)
		}
		return out
	}

	if got := names(filterLinks(lab.Links(), nil, nil)); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("no filters = %q", got)
	}
	if got := names(filterLinks(lab.Links(), kathara.NewNameSet("A"), nil)); !reflect.DeepEqual(got, []string{"A"}) {
		t.Errorf("selected = %q", got)
	}
	if got := names(filterLinks(lab.Links(), nil, kathara.NewNameSet("A"))); !reflect.DeepEqual(got, []string{"B", "C"}) {
		t.Errorf("excluded = %q", got)
	}
	if got := names(filterLinks(lab.Links(), kathara.NewNameSet(), nil)); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Errorf("an empty selected set = %q, want everything", got)
	}
}

// TestFilterContainers is the undeploy path's list comprehension, which filters
// on the `name` LABEL — the device name — and not on the Docker container name.
func TestFilterContainers(t *testing.T) {
	containers := []*Container{
		newTestContainer("pc1", nil),
		newTestContainer("pc2", nil),
		newTestContainer("pc3", nil),
	}

	selected := kathara.NewNameSet("pc2")
	kept := filterContainers(containers, func(name string) bool { return selected.Has(name) })
	if len(kept) != 1 || kept[0].Label(labelName) != "pc2" {
		t.Errorf("selected filter kept %d containers", len(kept))
	}

	kept = filterContainers(containers, func(name string) bool { return !selected.Has(name) })
	if len(kept) != 2 {
		t.Errorf("excluded filter kept %d containers, want 2", len(kept))
	}
}

func TestMissingMachines(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	for _, name := range []string{"pc1", "pc2"} {
		if _, err := lab.NewMachine(name, nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
	}

	got := missingMachines(lab, kathara.NewNameSet("pc9", "pc1", "pc3"))
	if !reflect.DeepEqual(got, []string{"pc3", "pc9"}) {
		t.Errorf("missingMachines = %q, want the absent names sorted", got)
	}
}

// TestInterfaceLinkNames is the set comprehension `deploy_machine` and
// `undeploy_machine` build, including the exception a tombstoned slot causes.
func TestInterfaceLinkNames(t *testing.T) {
	lab := model.NewLab("Default scenario", model.DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	linkA := lab.GetOrNewLink("A")
	if _, err := machine.AddInterface(linkA, model.AddInterfaceOptions{}); err != nil {
		t.Fatalf("AddInterface: %v", err)
	}
	if _, err := machine.AddInterface(lab.GetOrNewLink("B"), model.AddInterfaceOptions{}); err != nil {
		t.Fatalf("AddInterface: %v", err)
	}

	names, err := interfaceLinkNames(machine)
	if err != nil {
		t.Fatalf("interfaceLinkNames: %v", err)
	}
	if !reflect.DeepEqual(names.Names(), []string{"A", "B"}) {
		t.Errorf("interfaceLinkNames = %q", names.Names())
	}

	// `remove_interface` leaves a tombstone, and the comprehension has no
	// guard: `x.link` on None is an AttributeError.
	if err := machine.RemoveInterface(linkA); err != nil {
		t.Fatalf("RemoveInterface: %v", err)
	}
	if _, err := interfaceLinkNames(machine); !errors.Is(err, model.ErrPyAttributeError) {
		t.Errorf("a tombstoned slot gave %v, want the AttributeError Python raises", err)
	}
}

func TestFirstInterface(t *testing.T) {
	t.Run("no interfaces at all", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		iface, ok, err := firstInterface(machine)
		if err != nil || ok {
			t.Fatalf("firstInterface = (%v, %v, %v), want the absent case", iface, ok, err)
		}
	})

	t.Run("interface 0 present", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		attach(t, machine, "A", 0, "")
		iface, ok, err := firstInterface(machine)
		if err != nil || !ok || iface.Link.Name != "A" {
			t.Fatalf("firstInterface = (%v, %v, %v)", iface, ok, err)
		}
	})

	t.Run("interfaces but no slot 0 is a KeyError", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		attach(t, machine, "A", 3, "")
		_, _, err := firstInterface(machine)
		if !errors.Is(err, model.ErrPyKeyError) {
			t.Fatalf("err = %v, want KeyError: 0", err)
		}
		if err.Error() != "0" {
			// CPython's KeyError carries repr(key), and an int's repr has no
			// quotes — unlike every other KeyError in this package.
			t.Errorf("KeyError message = %q, want the unquoted 0", err.Error())
		}
	})

	t.Run("a tombstoned slot 0 is an AttributeError", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		link := machine.Lab.GetOrNewLink("A")
		if _, err := machine.AddInterface(link, model.AddInterfaceOptions{Number: model.InterfaceNumber(0)}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		if err := machine.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}
		if _, _, err := firstInterface(machine); !errors.Is(err, model.ErrPyAttributeError) {
			t.Errorf("err = %v, want the `.link` AttributeError", err)
		}
	})
}

// TestEntrypointAndArgs is `DockerMachine.create:358-361`, where the two metas
// are gated differently: `entrypoint` on PRESENCE and `args` on TRUTHINESS.
func TestEntrypointAndArgs(t *testing.T) {
	t.Run("both absent", func(t *testing.T) {
		entrypoint, args, err := entrypointAndArgs(newFixtureMachine(t, false))
		if err != nil || entrypoint != nil || args != nil {
			t.Fatalf("= (%q, %q, %v), want nils", entrypoint, args, err)
		}
	})

	t.Run("an entrypoint is shlex-split", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		machine.Meta.Entrypoint = model.Str("/bin/sh -c 'echo hi'")
		entrypoint, _, err := entrypointAndArgs(machine)
		if err != nil {
			t.Fatalf("entrypointAndArgs: %v", err)
		}
		if !reflect.DeepEqual(entrypoint, []string{"/bin/sh", "-c", "echo hi"}) {
			t.Errorf("entrypoint = %q", entrypoint)
		}
	})

	t.Run("an EMPTY entrypoint is still applied", func(t *testing.T) {
		// The gate is `"entrypoint" in machine.meta`, with no truthiness test,
		// so an empty value splits to the empty list and `'Entrypoint': []` is
		// POSTED — which resets the image's entrypoint. The list must be
		// non-nil: a nil slice marshals as `null`, which means "keep the
		// image's" and runs a different process.
		machine := newFixtureMachine(t, false)
		machine.Meta.Entrypoint = model.Str("")
		entrypoint, _, err := entrypointAndArgs(machine)
		if err != nil {
			t.Fatalf("entrypointAndArgs: %v", err)
		}
		if entrypoint == nil || len(entrypoint) != 0 {
			t.Errorf("entrypoint = %#v, want a non-nil empty slice", entrypoint)
		}

		payload, err := json.Marshal(&container.Config{Entrypoint: entrypoint})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if !strings.Contains(string(payload), `"Entrypoint":[]`) {
			t.Errorf("payload = %s, want an empty Entrypoint array", payload)
		}
	})

	t.Run("string args are shlex-split", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		machine.Meta.Args = model.Str("-c 'sleep 1'")
		_, args, err := entrypointAndArgs(machine)
		if err != nil {
			t.Fatalf("entrypointAndArgs: %v", err)
		}
		if !reflect.DeepEqual(args, []string{"-c", "sleep 1"}) {
			t.Errorf("args = %q", args)
		}
	})

	t.Run("list args are passed through", func(t *testing.T) {
		machine := newFixtureMachine(t, false)
		machine.Meta.Args = model.Strings([]string{"-c", "sleep 1"})
		_, args, err := entrypointAndArgs(machine)
		if err != nil {
			t.Fatalf("entrypointAndArgs: %v", err)
		}
		if !reflect.DeepEqual(args, []string{"-c", "sleep 1"}) {
			t.Errorf("args = %q", args)
		}
	})

	t.Run("falsy args are dropped entirely", func(t *testing.T) {
		// `if "args" in meta and meta["args"]` — a present but empty value
		// becomes None, not an empty command.
		machine := newFixtureMachine(t, false)
		machine.Meta.Args = model.Str("")
		_, args, err := entrypointAndArgs(machine)
		if err != nil || args != nil {
			t.Fatalf("= (%q, %v), want nil args", args, err)
		}
	})
}

func TestBridgedIfaceNumber(t *testing.T) {
	machine := newFixtureMachine(t, false)
	if _, ok := bridgedIfaceNumber(machine); ok {
		t.Error("an unset bridged_iface reported a number")
	}

	machine.Meta.BridgedIface = model.Int(3)
	if got, ok := bridgedIfaceNumber(machine); !ok || got != 3 {
		t.Errorf("bridgedIfaceNumber = (%d, %v), want (3, true)", got, ok)
	}

	machine.Meta.BridgedIface = model.Str("3")
	if _, ok := bridgedIfaceNumber(machine); ok {
		t.Error("a string bridged_iface was treated as a number; Python's `>` raises on it")
	}
}

// TestNewAPIClientKeepsTheSocketDialer is a regression test for the option
// ORDER in [newAPIClient].
func TestNewAPIClientKeepsTheSocketDialer(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	api, err := newAPIClient(&settings.Settings{})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })

	if got := api.DaemonHost(); got != "unix:///var/run/docker.sock" {
		t.Errorf("DaemonHost = %q, want the DOCKER_HOST value", got)
	}
}

// TestNewAPIClientHonoursRemoteURL is the other arm of
// `DockerManager.__init__`: a configured `remote_url` replaces the environment
// entirely, and `cert_path` adds CA verification with no client certificate.
func TestNewAPIClientHonoursRemoteURL(t *testing.T) {
	// Set an environment host too, to prove the setting wins over it.
	t.Setenv("DOCKER_HOST", "unix:///var/run/docker.sock")

	remote := "tcp://198.51.100.7:2376"
	api, err := newAPIClient(&settings.Settings{RemoteURL: &remote})
	if err != nil {
		t.Fatalf("newAPIClient: %v", err)
	}
	t.Cleanup(func() { _ = api.Close() })

	if got := api.DaemonHost(); got != remote {
		t.Errorf("DaemonHost = %q, want the configured remote_url", got)
	}
}
