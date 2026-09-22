package kathara

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// ctxKey types the value the delegation tests thread through the context, so
// that "the same context arrived" is checkable without comparing interfaces
// that happen to be equal for another reason.
type ctxKey struct{}

func testContext() context.Context {
	return context.WithValue(context.Background(), ctxKey{}, "delegation")
}

// TestClientDelegates is `manager/Kathara.py:63-605`: thirty bodies that each
// forward to the identically named manager method with the identical
// arguments, positionally.
func TestClientDelegates(t *testing.T) {
	t.Parallel()

	ctx := testContext()
	lab := model.NewLab("delegation", model.DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	ref := LabRef{Hash: "FwFaxbiuhvSWb2KpN5zw"}
	deployOpts := DeployLabOptions{SelectedMachines: NewNameSet("pc1")}
	undeployOpts := UndeployLabOptions{SelectedLinks: NewNameSet("A")}
	ttyOpts := DefaultConnectTTYOptions()
	cmd := NewCommand("ls", "-la")
	wait := WaitRetries(3, 250*time.Millisecond)
	files := []CopyEntry{{GuestPath: "/etc/frr/frr.conf", HostPath: "/tmp/frr.conf"}}

	tests := []struct {
		name     string
		invoke   func(c *Client)
		method   string
		wantArgs []any
	}{
		{
			name:     "DeployMachine",
			invoke:   func(c *Client) { _ = c.DeployMachine(ctx, machine) },
			method:   "DeployMachine",
			wantArgs: []any{ctx, machine},
		},
		{
			name:     "DeployLink",
			invoke:   func(c *Client) { _ = c.DeployLink(ctx, link) },
			method:   "DeployLink",
			wantArgs: []any{ctx, link},
		},
		{
			name:     "DeployLab",
			invoke:   func(c *Client) { _ = c.DeployLab(ctx, lab, deployOpts) },
			method:   "DeployLab",
			wantArgs: []any{ctx, lab, deployOpts},
		},
		{
			name:     "ConnectMachineToLink",
			invoke:   func(c *Client) { _ = c.ConnectMachineToLink(ctx, machine, link, "00:00:00:00:00:01") },
			method:   "ConnectMachineToLink",
			wantArgs: []any{ctx, machine, link, "00:00:00:00:00:01"},
		},
		{
			name:     "DisconnectMachineFromLink",
			invoke:   func(c *Client) { _ = c.DisconnectMachineFromLink(ctx, machine, link, true) },
			method:   "DisconnectMachineFromLink",
			wantArgs: []any{ctx, machine, link, true},
		},
		{
			name:     "UndeployMachine",
			invoke:   func(c *Client) { _ = c.UndeployMachine(ctx, machine, true) },
			method:   "UndeployMachine",
			wantArgs: []any{ctx, machine, true},
		},
		{
			name:     "UndeployLink",
			invoke:   func(c *Client) { _ = c.UndeployLink(ctx, link) },
			method:   "UndeployLink",
			wantArgs: []any{ctx, link},
		},
		{
			name:     "UndeployLab",
			invoke:   func(c *Client) { _ = c.UndeployLab(ctx, ref, undeployOpts) },
			method:   "UndeployLab",
			wantArgs: []any{ctx, ref, undeployOpts},
		},
		{
			name:     "Wipe",
			invoke:   func(c *Client) { _ = c.Wipe(ctx, true) },
			method:   "Wipe",
			wantArgs: []any{ctx, true},
		},
		{
			name:     "ConnectTTY",
			invoke:   func(c *Client) { _, _ = c.ConnectTTY(ctx, "pc1", ref, ttyOpts) },
			method:   "ConnectTTY",
			wantArgs: []any{ctx, "pc1", ref, ttyOpts},
		},
		{
			name:     "ConnectTTYObj",
			invoke:   func(c *Client) { _, _ = c.ConnectTTYObj(ctx, machine, ttyOpts) },
			method:   "ConnectTTYObj",
			wantArgs: []any{ctx, machine, ttyOpts},
		},
		{
			name:     "Exec",
			invoke:   func(c *Client) { _, _, _, _ = c.Exec(ctx, "pc1", cmd, ref, wait) },
			method:   "Exec",
			wantArgs: []any{ctx, "pc1", cmd, ref, wait},
		},
		{
			name:     "ExecObj",
			invoke:   func(c *Client) { _, _, _, _ = c.ExecObj(ctx, machine, cmd, wait) },
			method:   "ExecObj",
			wantArgs: []any{ctx, machine, cmd, wait},
		},
		{
			name:     "ExecStream",
			invoke:   func(c *Client) { _, _ = c.ExecStream(ctx, "pc1", cmd, ref, wait) },
			method:   "ExecStream",
			wantArgs: []any{ctx, "pc1", cmd, ref, wait},
		},
		{
			name:     "ExecStreamObj",
			invoke:   func(c *Client) { _, _ = c.ExecStreamObj(ctx, machine, cmd, wait) },
			method:   "ExecStreamObj",
			wantArgs: []any{ctx, machine, cmd, wait},
		},
		{
			name:     "CopyFiles",
			invoke:   func(c *Client) { _ = c.CopyFiles(ctx, machine, files) },
			method:   "CopyFiles",
			wantArgs: []any{ctx, machine, files},
		},
		{
			name:     "RetrieveFiles",
			invoke:   func(c *Client) { _ = c.RetrieveFiles(ctx, machine, "/var/log", "/tmp/log") },
			method:   "RetrieveFiles",
			wantArgs: []any{ctx, machine, "/var/log", "/tmp/log"},
		},
		{
			name:     "GetMachineAPIObject",
			invoke:   func(c *Client) { _, _ = c.GetMachineAPIObject(ctx, "pc1", ref, true) },
			method:   "GetMachineAPIObject",
			wantArgs: []any{ctx, "pc1", ref, true},
		},
		{
			name:     "GetMachinesAPIObjects",
			invoke:   func(c *Client) { _, _ = c.GetMachinesAPIObjects(ctx, ref, false) },
			method:   "GetMachinesAPIObjects",
			wantArgs: []any{ctx, ref, false},
		},
		{
			name:     "GetLinkAPIObject",
			invoke:   func(c *Client) { _, _ = c.GetLinkAPIObject(ctx, "A", ref, true) },
			method:   "GetLinkAPIObject",
			wantArgs: []any{ctx, "A", ref, true},
		},
		{
			name:     "GetLinksAPIObjects",
			invoke:   func(c *Client) { _, _ = c.GetLinksAPIObjects(ctx, ref, false) },
			method:   "GetLinksAPIObjects",
			wantArgs: []any{ctx, ref, false},
		},
		{
			name:     "GetLabFromAPI",
			invoke:   func(c *Client) { _, _ = c.GetLabFromAPI(ctx, "hash", "name") },
			method:   "GetLabFromAPI",
			wantArgs: []any{ctx, "hash", "name"},
		},
		{
			name:     "UpdateLabFromAPI",
			invoke:   func(c *Client) { _ = c.UpdateLabFromAPI(ctx, lab) },
			method:   "UpdateLabFromAPI",
			wantArgs: []any{ctx, lab},
		},
		{
			name:     "GetMachinesStats",
			invoke:   func(c *Client) { _, _ = c.GetMachinesStats(ctx, ref, "pc1", true) },
			method:   "GetMachinesStats",
			wantArgs: []any{ctx, ref, "pc1", true},
		},
		{
			name:     "GetMachineStats",
			invoke:   func(c *Client) { _ = c.GetMachineStats(ctx, "pc1", ref, false) },
			method:   "GetMachineStats",
			wantArgs: []any{ctx, "pc1", ref, false},
		},
		{
			name:     "GetMachineStatsObj",
			invoke:   func(c *Client) { _, _ = c.GetMachineStatsObj(ctx, machine, true) },
			method:   "GetMachineStatsObj",
			wantArgs: []any{ctx, machine, true},
		},
		{
			name:     "GetLinksStats",
			invoke:   func(c *Client) { _, _ = c.GetLinksStats(ctx, ref, "A", true) },
			method:   "GetLinksStats",
			wantArgs: []any{ctx, ref, "A", true},
		},
		{
			name:     "GetLinkStats",
			invoke:   func(c *Client) { _ = c.GetLinkStats(ctx, "A", ref, false) },
			method:   "GetLinkStats",
			wantArgs: []any{ctx, "A", ref, false},
		},
		{
			name:     "GetLinkStatsObj",
			invoke:   func(c *Client) { _, _ = c.GetLinkStatsObj(ctx, link, true) },
			method:   "GetLinkStatsObj",
			wantArgs: []any{ctx, link, true},
		},
		{
			name:     "CheckImage",
			invoke:   func(c *Client) { _ = c.CheckImage(ctx, "kathara/base") },
			method:   "CheckImage",
			wantArgs: []any{ctx, "kathara/base"},
		},
		{
			name:     "GetReleaseVersion",
			invoke:   func(c *Client) { _, _ = c.GetReleaseVersion(ctx) },
			method:   "GetReleaseVersion",
			wantArgs: []any{ctx},
		},
		{
			name:     "GetFormattedManagerName",
			invoke:   func(c *Client) { _ = c.GetFormattedManagerName() },
			method:   "GetFormattedManagerName",
			wantArgs: []any{},
		},
	}

	if want := reflect.TypeOf((*Manager)(nil)).Elem().NumMethod(); len(tests) != want {
		t.Fatalf("delegation table covers %d methods, Manager declares %d", len(tests), want)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeManager{}
			tt.invoke(NewClientWithManager(fake))

			if len(fake.calls) != 1 {
				t.Fatalf("recorded %d calls, want exactly 1: %+v", len(fake.calls), fake.calls)
			}
			got := fake.last()
			if got.method != tt.method {
				t.Errorf("delegated to %s, want %s", got.method, tt.method)
			}
			if len(got.args) != len(tt.wantArgs) {
				t.Fatalf("delegated %d args, want %d", len(got.args), len(tt.wantArgs))
			}
			for i := range tt.wantArgs {
				if !reflect.DeepEqual(got.args[i], tt.wantArgs[i]) {
					t.Errorf("arg %d = %#v, want %#v", i, got.args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// TestClientReturnsBackendResults is the other half of the delegation contract:
// what comes back is what the manager produced, untouched. `manager/Kathara.py`
// spells fifteen of its thirty bodies `return self.manager…` and the other
// fifteen bare, and neither shape inspects the result.
func TestClientReturnsBackendResults(t *testing.T) {
	t.Parallel()

	ctx := testContext()
	lab := model.NewLab("results", model.DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	apiObject := struct{ ID string }{ID: "container"}
	fake := &fakeManager{
		err:               errBackend,
		wantAPIObject:     apiObject,
		wantAPIObjects:    []any{apiObject},
		wantLab:           lab,
		wantSession:       &fakeSession{id: "session"},
		wantStream:        &fakeExecStream{id: "stream"},
		wantStdout:        []byte("out"),
		wantStderr:        []byte("err"),
		wantExitCode:      42,
		wantVersion:       "27.3.1",
		wantName:          "Docker (Kathara)",
		wantMachinesStats: &fakeMachinesStats{id: "machines"},
		wantMachineStats:  &fakeMachineStats{id: "machine"},
		wantLinksStats:    &fakeLinksStats{id: "links"},
		wantLinkStats:     &fakeLinkStats{id: "link"},
	}
	client := NewClientWithManager(fake)

	t.Run("errors travel unchanged", func(t *testing.T) {
		if err := client.DeployMachine(ctx, machine); !errors.Is(err, errBackend) {
			t.Errorf("DeployMachine err = %v, want errBackend", err)
		}
		if err := client.Wipe(ctx, false); !errors.Is(err, errBackend) {
			t.Errorf("Wipe err = %v, want errBackend", err)
		}
	})

	t.Run("exec tuple", func(t *testing.T) {
		stdout, stderr, code, err := client.Exec(ctx, "pc1", NewCommand("ls"), LabRef{Hash: "h"}, NoWait())
		if string(stdout) != "out" || string(stderr) != "err" || code != 42 || !errors.Is(err, errBackend) {
			t.Errorf("Exec = %q, %q, %d, %v", stdout, stderr, code, err)
		}
	})

	t.Run("interface values keep their identity", func(t *testing.T) {
		session, _ := client.ConnectTTY(ctx, "pc1", LabRef{Hash: "h"}, DefaultConnectTTYOptions())
		if session != fake.wantSession {
			t.Errorf("ConnectTTY session = %#v, want the manager's", session)
		}
		stream, _ := client.ExecStream(ctx, "pc1", NewCommand("ls"), LabRef{Hash: "h"}, NoWait())
		if stream != fake.wantStream {
			t.Errorf("ExecStream = %#v, want the manager's", stream)
		}
		machines, _ := client.GetMachinesStats(ctx, LabRef{}, "", false)
		if machines != fake.wantMachinesStats {
			t.Errorf("GetMachinesStats = %#v, want the manager's", machines)
		}
		if single := client.GetMachineStats(ctx, "pc1", LabRef{Hash: "h"}, false); single != fake.wantMachineStats {
			t.Errorf("GetMachineStats = %#v, want the manager's", single)
		}
		links, _ := client.GetLinksStats(ctx, LabRef{}, "", false)
		if links != fake.wantLinksStats {
			t.Errorf("GetLinksStats = %#v, want the manager's", links)
		}
		if single := client.GetLinkStats(ctx, "A", LabRef{Hash: "h"}, false); single != fake.wantLinkStats {
			t.Errorf("GetLinkStats = %#v, want the manager's", single)
		}
	})

	t.Run("plain values", func(t *testing.T) {
		if got, _ := client.GetMachineAPIObject(ctx, "pc1", LabRef{Hash: "h"}, false); got != any(apiObject) {
			t.Errorf("GetMachineAPIObject = %#v", got)
		}
		if got, _ := client.GetLabFromAPI(ctx, "h", ""); got != lab {
			t.Errorf("GetLabFromAPI = %#v, want the manager's lab", got)
		}
		if got, _ := client.GetReleaseVersion(ctx); got != "27.3.1" {
			t.Errorf("GetReleaseVersion = %q", got)
		}
		if got := client.GetFormattedManagerName(); got != "Docker (Kathara)" {
			t.Errorf("GetFormattedManagerName = %q", got)
		}
		if got, _ := client.GetLinkStatsObj(ctx, link, false); got != fake.wantLinkStats {
			t.Errorf("GetLinkStatsObj = %#v", got)
		}
	})
}

// TestClientDoesNotValidate is the load-bearing negative: `manager/Kathara.py`
// performs no argument validation at all, and a client-side
// `check_required_single_not_none_var` would be observable.
func TestClientDoesNotValidate(t *testing.T) {
	t.Parallel()

	ctx := testContext()
	fake := &fakeManager{}
	client := NewClientWithManager(fake)

	// An empty ref fails RequireSingle, and an over-full one fails it too.
	// Neither is rejected here; both are handed down.
	for _, ref := range []LabRef{{}, {Hash: "h", Name: "n"}} {
		fake.calls = nil

		if err := client.UndeployLab(ctx, ref, UndeployLabOptions{}); err != nil {
			t.Errorf("UndeployLab(%+v) = %v, want the call to be delegated", ref, err)
		}
		if got := fake.last(); got.method != "UndeployLab" {
			t.Fatalf("UndeployLab(%+v) did not reach the manager", ref)
		}

		_ = client.GetMachineStats(ctx, "pc1", ref, false)
		if got := fake.last(); got.method != "GetMachineStats" {
			t.Fatalf("GetMachineStats(%+v) did not reach the manager", ref)
		}
	}
}

func TestClientPreservesNilVersusEmptyFilters(t *testing.T) {
	t.Parallel()

	ctx := testContext()
	lab := model.NewLab("filters", model.DefaultDefaults())

	tests := []struct {
		name     string
		selected NameSet
		wantNil  bool
		wantLen  int
	}{
		{name: "nil stays nil", selected: nil, wantNil: true},
		{name: "empty stays empty and non-nil", selected: NewNameSet(), wantLen: 0},
		{name: "populated stays populated", selected: NewNameSet("pc1", "pc2"), wantLen: 2},
	}

	for _, tt := range tests {
		t.Run("deploy/"+tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeManager{}
			client := NewClientWithManager(fake)
			if err := client.DeployLab(ctx, lab, DeployLabOptions{SelectedMachines: tt.selected}); err != nil {
				t.Fatalf("DeployLab: %v", err)
			}

			opts, ok := fake.last().args[2].(DeployLabOptions)
			if !ok {
				t.Fatalf("third argument is %T, want DeployLabOptions", fake.last().args[2])
			}
			if (opts.SelectedMachines == nil) != tt.wantNil {
				t.Fatalf("SelectedMachines nil = %v, want %v", opts.SelectedMachines == nil, tt.wantNil)
			}
			if !tt.wantNil && len(opts.SelectedMachines) != tt.wantLen {
				t.Errorf("SelectedMachines len = %d, want %d", len(opts.SelectedMachines), tt.wantLen)
			}
		})

		t.Run("undeploy/"+tt.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeManager{}
			client := NewClientWithManager(fake)
			opts := UndeployLabOptions{SelectedMachines: tt.selected, SelectedLinks: tt.selected}
			if err := client.UndeployLab(ctx, LabRef{Hash: "h"}, opts); err != nil {
				t.Fatalf("UndeployLab: %v", err)
			}

			got, ok := fake.last().args[2].(UndeployLabOptions)
			if !ok {
				t.Fatalf("third argument is %T, want UndeployLabOptions", fake.last().args[2])
			}
			for label, set := range map[string]NameSet{
				"SelectedMachines": got.SelectedMachines,
				"SelectedLinks":    got.SelectedLinks,
			} {
				if (set == nil) != tt.wantNil {
					t.Errorf("%s nil = %v, want %v", label, set == nil, tt.wantNil)
				}
				if !tt.wantNil && len(set) != tt.wantLen {
					t.Errorf("%s len = %d, want %d", label, len(set), tt.wantLen)
				}
			}
		})
	}
}

// TestNewClient covers `Kathara.__init__`: read `manager_type`, resolve it,
// construct the backend eagerly, and hand it the two things the deleted
// singletons used to supply.
func TestNewClient(t *testing.T) {
	t.Parallel()

	t.Run("selects by manager_type and constructs eagerly", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		var built int
		var gotCfg Config
		fake := &fakeManager{wantName: "Docker (Kathara)"}
		mustRegister(t, registry, Backend{
			Name:          "docker",
			FormattedName: "Docker (Kathara)",
			New: func(_ context.Context, cfg Config) (Manager, error) {
				built++
				gotCfg = cfg
				return fake, nil
			},
		})
		mustRegister(t, registry, Backend{
			Name:          "kubernetes",
			FormattedName: "Kubernetes (Megalos)",
			New:           func(context.Context, Config) (Manager, error) { t.Fatal("wrong backend built"); return nil, nil },
		})

		s := settings.Defaults()
		s.ManagerType = "docker"
		s.Image = "kathara/frr"
		s.DeviceShell = "/bin/zsh"
		s.EnableIPv6 = true
		s.VolumeMountPolicy = "Never"

		dispatcher := event.New()
		client, err := NewClient(testContext(), s, WithRegistry(registry), WithDispatcher(dispatcher))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if built != 1 {
			t.Errorf("factory ran %d times, want 1", built)
		}
		if client.Manager() != fake {
			t.Errorf("Manager() = %#v, want the constructed backend", client.Manager())
		}
		if gotCfg.Settings != s {
			t.Errorf("Config.Settings = %#v, want the settings passed in", gotCfg.Settings)
		}
		if gotCfg.Dispatcher != dispatcher {
			t.Errorf("Config.Dispatcher = %#v, want the dispatcher passed in", gotCfg.Dispatcher)
		}
		// The model's settings reads become injected defaults, resolved
		// once, here.
		want := model.Defaults{
			Image:             "kathara/frr",
			DeviceShell:       "/bin/zsh",
			EnableIPv6:        true,
			VolumeMountPolicy: "Never",
		}
		if gotCfg.Defaults != want {
			t.Errorf("Config.Defaults = %+v, want %+v", gotCfg.Defaults, want)
		}
	})

	t.Run("unknown manager_type is SettingsError", func(t *testing.T) {
		t.Parallel()

		s := settings.Defaults()
		s.ManagerType = "podman"

		_, err := NewClient(testContext(), s, WithRegistry(NewRegistry()))

		if !errors.Is(err, kerrors.ErrSettings) {
			t.Fatalf("NewClient err = %v, want a SettingsError", err)
		}
		// `SettingsError` wraps every reason in its own sentence
		// (`exceptions.py`), so this is the whole line a user is shown.
		want := "Settings file is not valid: Manager Type not allowed. Fix it or delete it before launching."
		if got := err.Error(); got != want {
			t.Errorf("message = %q, want %q", got, want)
		}
		if got, want := Code(err), CodeSettings; got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	})

	t.Run("a nok8s build refuses a kubernetes manager_type", func(t *testing.T) {
		t.Parallel()

		// `settings.AvailableManagers` still lists kubernetes — the config
		// schema is frozen — so `Settings.CheckManager` passes and only the
		// registry knows the binary cannot run it.
		s := settings.Defaults()
		s.ManagerType = "kubernetes"
		if err := s.CheckManager(); err != nil {
			t.Fatalf("CheckManager: %v, want the frozen schema to accept kubernetes", err)
		}

		dockerOnly := NewRegistry()
		mustRegister(t, dockerOnly, Backend{
			Name:          "docker",
			FormattedName: "Docker (Kathara)",
			New:           func(context.Context, Config) (Manager, error) { return &fakeManager{}, nil },
		})

		if _, err := NewClient(testContext(), s, WithRegistry(dockerOnly)); !errors.Is(err, kerrors.ErrSettings) {
			t.Errorf("NewClient err = %v, want a SettingsError", err)
		}
	})

	t.Run("nil settings", func(t *testing.T) {
		t.Parallel()

		_, err := NewClient(testContext(), nil, WithRegistry(NewRegistry()))
		if !errors.Is(err, ErrNoSettings) {
			t.Errorf("NewClient(nil) = %v, want ErrNoSettings", err)
		}
		// A Go-only failure mode gets no taxonomy row of its own.
		if got, want := Code(err), CodeInternalError; got != want {
			t.Errorf("code = %q, want %q", got, want)
		}
	})

	t.Run("factory failure travels", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		mustRegister(t, registry, Backend{
			Name:          "docker",
			FormattedName: "Docker (Kathara)",
			New:           func(context.Context, Config) (Manager, error) { return nil, kerrors.NewDaemonConnection(errBackend) },
		})

		s := settings.Defaults()
		s.ManagerType = "docker"

		_, err := NewClient(testContext(), s, WithRegistry(registry))
		if !errors.Is(err, kerrors.ErrDaemonConnection) {
			t.Errorf("NewClient err = %v, want the factory's DockerDaemonConnectionError", err)
		}
	})

	t.Run("factory returning nothing", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		mustRegister(t, registry, Backend{
			Name:          "docker",
			FormattedName: "Docker (Kathara)",
			New:           func(context.Context, Config) (Manager, error) { return nil, nil },
		})

		s := settings.Defaults()
		s.ManagerType = "docker"

		if _, err := NewClient(testContext(), s, WithRegistry(registry)); !errors.Is(err, ErrInvalidBackend) {
			t.Errorf("NewClient err = %v, want ErrInvalidBackend", err)
		}
	})

	t.Run("WithDefaults overrides the settings-derived ones", func(t *testing.T) {
		t.Parallel()

		registry := NewRegistry()
		var gotCfg Config
		mustRegister(t, registry, Backend{
			Name:          "docker",
			FormattedName: "Docker (Kathara)",
			New: func(_ context.Context, cfg Config) (Manager, error) {
				gotCfg = cfg
				return &fakeManager{}, nil
			},
		})

		s := settings.Defaults()
		s.ManagerType = "docker"
		override := model.Defaults{Image: "kathara/quagga", DeviceShell: "/bin/sh"}

		if _, err := NewClient(testContext(), s, WithRegistry(registry), WithDefaults(override)); err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		if gotCfg.Defaults != override {
			t.Errorf("Config.Defaults = %+v, want %+v", gotCfg.Defaults, override)
		}
	})
}

// TestClientAvailableManagersUsesItsOwnRegistry is
// `Kathara.get_available_managers_name()`.
func TestClientAvailableManagersUsesItsOwnRegistry(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	mustRegister(t, registry, Backend{
		Name:          "docker",
		FormattedName: "Docker (Kathara)",
		New:           func(context.Context, Config) (Manager, error) { return &fakeManager{}, nil },
	})
	mustRegister(t, registry, Backend{
		Name:          "kubernetes",
		FormattedName: "Kubernetes (Megalos)",
		New:           func(context.Context, Config) (Manager, error) { return &fakeManager{}, nil },
	})

	s := settings.Defaults()
	s.ManagerType = "docker"
	client, err := NewClient(testContext(), s, WithRegistry(registry))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	got := client.AvailableManagers()
	want := []ManagerInfo{
		{Name: "docker", FormattedName: "Docker (Kathara)"},
		{Name: "kubernetes", FormattedName: "Kubernetes (Megalos)"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("AvailableManagers() = %+v, want %+v", got, want)
	}

	// The fallback: no registry was consulted to build this one, so the
	// package-level table is the only answer available.
	direct := NewClientWithManager(&fakeManager{})
	if !reflect.DeepEqual(direct.AvailableManagers(), AvailableManagers()) {
		t.Errorf("NewClientWithManager client listed %+v, want DefaultRegistry's %+v",
			direct.AvailableManagers(), AvailableManagers())
	}
}

// TestNewClientWithManagerRejectsNil keeps a mis-wired embedder from getting a
// Client that panics on its first delegation.
func TestNewClientWithManagerRejectsNil(t *testing.T) {
	t.Parallel()

	if c := NewClientWithManager(nil); c != nil {
		t.Errorf("NewClientWithManager(nil) = %#v, want nil", c)
	}
}

// TestDefaultsFrom checks which setting backs each model default, including the
// image used by a device with no `image=` line.
func TestDefaultsFrom(t *testing.T) {
	t.Parallel()

	s := settings.Defaults()
	s.Image = "kathara/frr"
	s.DeviceShell = "/bin/bash"
	s.EnableIPv6 = true
	s.VolumeMountPolicy = "Prompt"

	got := DefaultsFrom(s)
	want := model.Defaults{
		Image:             "kathara/frr",
		DeviceShell:       "/bin/bash",
		EnableIPv6:        true,
		VolumeMountPolicy: "Prompt",
	}
	if got != want {
		t.Errorf("DefaultsFrom = %+v, want %+v", got, want)
	}

	if got, want := DefaultsFrom(nil), model.DefaultDefaults(); got != want {
		t.Errorf("DefaultsFrom(nil) = %+v, want %+v", got, want)
	}
}
