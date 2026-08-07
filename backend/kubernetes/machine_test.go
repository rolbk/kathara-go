package kubernetes

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// TestBuildDefinitionGoldens is EXPECTATIONS-k8s §1 "_build_definition →
// Deployment object": every generated Deployment compared, as JSON, against the
// object 3.8.3 submits.
//
// The devices here have NOT been through `create`, exactly as the Python
// fixtures have not, so `machine.meta['sysctls']` is empty and the postStart
// hook's `{sysctl_commands}` expands to "" — which the
// `test_build_definition_no_config` expectation states outright. The merged
// sysctls are covered by [TestCreateGoldens].
func TestBuildDefinitionGoldens(t *testing.T) {
	tests := []struct {
		name      string
		golden    string
		metas     [][2]string
		settings  func(*settings.Settings)
		configMap *corev1.ConfigMap
	}{
		{
			name:   "no config map",
			golden: "build_no_config.json",
		},
		{
			name:      "config map mounts hostlab",
			golden:    "build_config_map.json",
			configMap: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test_device_config_map"}},
		},
		{
			name:      "docker config json adds the image pull secret",
			golden:    "build_docker_config_json.json",
			configMap: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "test_device_config_map"}},
			settings:  func(s *settings.Settings) { s.DockerConfigJSON = ptr("eyJhdXRocyI6IHt9fQ==") },
		},
		{
			name:     "host_shared mounts /shared",
			golden:   "build_host_shared.json",
			settings: func(s *settings.Settings) { s.HostShared = true },
		},
		{
			name:   "entrypoint is shell-split into command",
			golden: "build_entrypoint.json",
			metas:  [][2]string{{"entrypoint", "/bin/test hello"}},
		},
		{
			name:   "args are shell-split",
			golden: "build_args.json",
			metas:  [][2]string{{"args", "-n 20 -c 10 -f 30"}},
		},
		{
			name:   "entrypoint and args together",
			golden: "build_entrypoint_args.json",
			metas:  [][2]string{{"entrypoint", "/bin/test hello"}, {"args", "-n 20 -c 10 -f 30"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			if test.settings != nil {
				test.settings(s)
			}

			m, _, _, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab, test.metas...)

			definition, err := m.machine.buildDefinition(device, test.configMap)
			if err != nil {
				t.Fatalf("buildDefinition: %v", err)
			}
			assertGolden(t, test.golden, definition)
		})
	}
}

// TestCreateGoldens is EXPECTATIONS-k8s §1 "create": the Deployment `create`
// actually submits, which differs from [TestBuildDefinitionGoldens] by the
// sysctl merge and the `real_name` assignment.
func TestCreateGoldens(t *testing.T) {
	volume0 := t.TempDir()
	volume1 := t.TempDir()

	tests := []struct {
		name         string
		golden       string
		metas        [][2]string
		settings     func(*settings.Settings)
		replacements [][2]string
	}{
		{
			name:   "default device merges the ipv6-off sysctls",
			golden: "create_default.json",
		},
		{
			name:     "ipv6 device merges the ipv6-on sysctls",
			golden:   "create_ipv6.json",
			metas:    [][2]string{{"ipv6", "True"}},
			settings: func(s *settings.Settings) { s.EnableIPv6 = true },
		},
		{
			name:   "device sysctls, envs and shell",
			golden: "create_env_sysctl_shell.json",
			metas: [][2]string{
				{"env", "MY_VAR=value"},
				{"sysctl", "net.ipv4.tcp_syncookies=1"},
				{"shell", "/bin/sh"},
			},
		},
		{
			name:   "published port",
			golden: "create_ports.json",
			metas:  [][2]string{{"port", "3001:56/udp"}},
		},
		{
			name:   "two volumes are mounted and declared in order",
			golden: "create_volumes.json",
			metas: [][2]string{
				{"volume", volume0 + "|/test|ro"},
				{"volume", volume1 + "|/test2|rw"},
			},
			replacements: [][2]string{{"<VOLUME0>", volume0}, {"<VOLUME1>", volume1}},
		},
		{
			// EXPECTATIONS-k8s `test_create_volume_never`: the quirk that the
			// pod still DECLARES the hostPath volume the policy forbids
			// mounting (k8s-backend.md G10).
			name:         "volume_mount_policy Never declares but does not mount",
			golden:       "create_volume_never.json",
			metas:        [][2]string{{"volume", volume0 + "|/test|ro"}},
			settings:     func(s *settings.Settings) { s.VolumeMountPolicy = "Never" },
			replacements: [][2]string{{"<VOLUME0>", volume0}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			if test.settings != nil {
				test.settings(s)
			}

			m, clientset, _, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab, test.metas...)

			if err := m.machine.Create(context.Background(), device); err != nil {
				t.Fatalf("Create: %v", err)
			}

			created, namespace := createdDeployment(t, clientset)
			if namespace != defaultScenarioHash {
				t.Errorf("namespace = %q, want %q", namespace, defaultScenarioHash)
			}
			assertGolden(t, test.golden, created, test.replacements...)
		})
	}
}

// TestCreateInterfacesGolden is the annotation half of `_build_definition`: the
// `k8s.v1.cni.cncf.io/networks` array, in interface order, with the MAC present
// only for the interface that declares one.
func TestCreateInterfacesGolden(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	for _, iface := range []struct{ cd, mac string }{{"A", ""}, {"B", "00:11:22:33:44:55"}} {
		link := lab.GetOrNewLink(iface.cd)
		link.APIObject = newTestNetwork(lab.Hash, iface.cd, 1)
		if _, err := device.AddInterface(link, model.AddInterfaceOptions{MAC: iface.mac}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
	}

	if err := m.machine.Create(context.Background(), device); err != nil {
		t.Fatalf("Create: %v", err)
	}
	created, _ := createdDeployment(t, clientset)
	assertGolden(t, "create_interfaces.json", created)
}

// createdDeployment pulls the object out of the fake clientset's action log —
// the Go equivalent of `assert_called_once_with(body=…, namespace=…)`.
func createdDeployment(t *testing.T, clientset actionRecorder) (*appsv1.Deployment, string) {
	t.Helper()

	var found *appsv1.Deployment
	var namespace string
	count := 0
	for _, action := range clientset.Actions() {
		create, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetResource() != (schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}) {
			continue
		}
		deployment, ok := create.GetObject().(*appsv1.Deployment)
		if !ok {
			continue
		}
		found = deployment
		namespace = create.GetNamespace()
		count++
	}
	if count != 1 {
		t.Fatalf("create_namespaced_deployment called %d times, want 1", count)
	}
	return found, namespace
}

type actionRecorder interface{ Actions() []k8stesting.Action }

// TestCreateConflictIsMachineAlreadyExists is
// EXPECTATIONS-k8s / `KubernetesMachine.py:366-370`: a 409 from the API server
// is the device already existing, and every other status is the passthrough
// code.
func TestCreateConflictIsMachineAlreadyExists(t *testing.T) {
	tests := []struct {
		name   string
		status error
		want   error
	}{
		{
			name:   "409 conflict",
			status: apierrors.NewConflict(schema.GroupResource{Group: "apps", Resource: "deployments"}, "d", errors.New("boom")),
			want:   kerrors.ErrMachineAlreadyExists,
		},
		{
			name:   "500 internal",
			status: apierrors.NewInternalError(errors.New("boom")),
			want:   kerrors.ErrKubernetesAPI,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, clientset, _, _ := newTestManager(t, s)
			clientset.PrependReactor("create", "deployments",
				func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, test.status })

			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab)

			err := m.machine.Create(context.Background(), device)
			if !errors.Is(err, test.want) {
				t.Fatalf("Create error = %v, want %v", err, test.want)
			}
		})
	}
}

// TestCreateVolumeMissingPermission is EXPECTATIONS-k8s
// `test_create_volume_no_w_permission`: a `rw` volume whose host directory the
// user cannot write is a PermissionError and NOTHING is submitted.
func TestCreateVolumeMissingPermission(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses the permission check, so the refusal is unreachable")
	}

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab, [2]string{"volume", dir + "|/test|rw"})

	err := m.machine.Create(context.Background(), device)
	if !errors.Is(err, kerrors.ErrPermission) {
		t.Fatalf("Create error = %v, want a permission error", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "deployments" {
			t.Fatal("a Deployment was submitted despite the permission refusal")
		}
	}
}

// TestMergeSysctlsOrder pins k8s-backend.md O4: the defaults in their literal
// order, then the device's own keys — and a device key that collides with a
// default keeps the DEFAULT's position while taking the device's value.
func TestMergeSysctlsOrder(t *testing.T) {
	tests := []struct {
		name  string
		ipv6  bool
		metas [][2]string
		want  []string
	}{
		{
			name: "ipv6 off",
			want: []string{
				"net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter", "net.ipv4.conf.lo.rp_filter",
				"net.ipv4.ip_forward", "net.ipv4.icmp_ratelimit",
				"net.ipv6.conf.default.disable_ipv6", "net.ipv6.conf.all.disable_ipv6",
				"net.ipv6.conf.default.forwarding", "net.ipv6.conf.all.forwarding",
			},
		},
		{
			name: "ipv6 on",
			ipv6: true,
			want: []string{
				"net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter", "net.ipv4.conf.lo.rp_filter",
				"net.ipv4.ip_forward", "net.ipv4.icmp_ratelimit",
				"net.ipv6.conf.all.forwarding", "net.ipv6.conf.all.accept_ra", "net.ipv6.icmp.ratelimit",
				"net.ipv6.conf.default.disable_ipv6", "net.ipv6.conf.all.disable_ipv6",
			},
		},
		{
			name:  "a device key that collides keeps the default's position",
			metas: [][2]string{{"sysctl", "net.ipv4.ip_forward=0"}, {"sysctl", "net.ipv4.tcp_syncookies=1"}},
			want: []string{
				"net.ipv4.conf.all.rp_filter", "net.ipv4.conf.default.rp_filter", "net.ipv4.conf.lo.rp_filter",
				"net.ipv4.ip_forward", "net.ipv4.icmp_ratelimit",
				"net.ipv6.conf.default.disable_ipv6", "net.ipv6.conf.all.disable_ipv6",
				"net.ipv6.conf.default.forwarding", "net.ipv6.conf.all.forwarding",
				"net.ipv4.tcp_syncookies",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			s.EnableIPv6 = test.ipv6
			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab, test.metas...)

			if err := mergeSysctls(device); err != nil {
				t.Fatalf("mergeSysctls: %v", err)
			}
			got := device.Sysctls().Keys()
			if strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("sysctl order:\n got %v\nwant %v", got, test.want)
			}
		})
	}
}

// TestSysctlOverrideValue checks that the colliding key takes the DEVICE's
// value while keeping the default's slot — the half of `{**a, **b}` an
// order-only assertion cannot see.
func TestSysctlOverrideValue(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab, [2]string{"sysctl", "net.ipv4.ip_forward=0"})

	if err := mergeSysctls(device); err != nil {
		t.Fatalf("mergeSysctls: %v", err)
	}
	value, ok := device.Sysctls().Get("net.ipv4.ip_forward")
	if !ok || value.String() != "0" {
		t.Fatalf("net.ipv4.ip_forward = %v (present=%v), want 0", value.String(), ok)
	}
}

// TestNetworkAttachmentsCrashes reproduces the two Python crashes the
// annotation loop reaches: a tombstoned interface slot and a collision domain
// that was never deployed (k8s-backend.md G2/O9).
func TestNetworkAttachmentsCrashes(t *testing.T) {
	t.Run("tombstoned interface", func(t *testing.T) {
		s := testSettings()
		lab := newTestLab(t, s)
		device := newBaseDevice(t, lab)

		link := lab.GetOrNewLink("A")
		link.APIObject = newTestNetwork(lab.Hash, "A", 1)
		if _, err := device.AddInterface(link, model.AddInterfaceOptions{}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		if err := device.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		_, err := networkAttachments(device)
		if !errors.Is(err, model.ErrPyAttributeError) {
			t.Fatalf("error = %v, want an AttributeError", err)
		}
	})

	t.Run("undeployed collision domain", func(t *testing.T) {
		s := testSettings()
		lab := newTestLab(t, s)
		device := newBaseDevice(t, lab)

		link := lab.GetOrNewLink("A")
		if _, err := device.AddInterface(link, model.AddInterfaceOptions{}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}

		_, err := networkAttachments(device)
		if !errors.Is(err, model.ErrPyTypeError) {
			t.Fatalf("error = %v, want a TypeError", err)
		}
	})
}

// TestDeployMachinesFilters is EXPECTATIONS-k8s §1 "deploy_machines": which
// devices are created for each filter shape, and that both filters together are
// refused before anything is created.
func TestDeployMachinesFilters(t *testing.T) {
	tests := []struct {
		name     string
		selected kathara.NameSet
		excluded kathara.NameSet
		want     []string
		wantErr  error
	}{
		{name: "no filter deploys every device", want: []string{"pc1", "pc2"}},
		{name: "selected", selected: kathara.NewNameSet("pc1"), want: []string{"pc1"}},
		{name: "excluded", excluded: kathara.NewNameSet("pc1"), want: []string{"pc2"}},
		{
			name:     "both filters",
			selected: kathara.NewNameSet("pc1"),
			excluded: kathara.NewNameSet("pc2"),
			wantErr:  kerrors.ErrSelectedOrExcludedMachines,
		},
		{
			// TRUTHINESS on this path: two empty sets are "no filter", not an
			// error (SYNTHESIS §1.7).
			name:     "two empty sets deploy everything",
			selected: kathara.NewNameSet(),
			excluded: kathara.NewNameSet(),
			want:     []string{"pc1", "pc2"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, clientset, _, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			for _, name := range []string{"pc1", "pc2"} {
				if _, err := lab.NewMachine(name, nil); err != nil {
					t.Fatalf("NewMachine: %v", err)
				}
			}
			injectPodsReady(clientset, lab.Hash, test.want...)

			err := m.machine.DeployMachines(context.Background(), lab, test.selected, test.excluded)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				if len(deployedNames(clientset)) != 0 {
					t.Fatal("devices were deployed despite the refusal")
				}
				return
			}
			if err != nil {
				t.Fatalf("DeployMachines: %v", err)
			}
			if got := deployedNames(clientset); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("deployed %v, want %v", got, test.want)
			}
		})
	}
}

// deployedNames is the `name` label of every Deployment the fake clientset was
// asked to create.
//
// The result is SORTED, not in submission order: the fan-out is a thread pool in
// Python and an errgroup here, and CONCURRENCY.tsv row
// `KubernetesMachine.py:197` records intra-chunk order as scheduler-dependent
// (k8s-backend.md O8). Asserting on it would be asserting on a race.
func deployedNames(clientset actionRecorder) []string {
	var names []string
	for _, action := range clientset.Actions() {
		create, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetResource().Resource != "deployments" {
			continue
		}
		if deployment, ok := create.GetObject().(*appsv1.Deployment); ok {
			names = append(names, deployment.Labels[labelName])
		}
	}
	slices.Sort(names)
	return names
}

// TestDeployMachinesDispatchesEvents pins CONCURRENCY.tsv row
// `KubernetesMachine.py:184`: all three device events come from the WATCHER,
// not from the deploy workers — `machines_deploy_started` carries the devices
// the wait set covers, `machine_deployed` fires when a pod reports Ready (with
// the device NAME, since Megalos has no Machine to pass), and
// `machines_deploy_ended` only when every watched device reported.
//
// It also pins that `DeployMachines` BLOCKS until then: the assertions run
// after it returns, and nothing has joined the watcher but the call itself.
func TestDeployMachinesDispatchesEvents(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)

	var order []string
	event.Subscribe(m.dispatcher, func(e event.MachinesDeployStarted) error {
		names := make([]string, 0, len(e.Machines))
		for _, machine := range e.Machines {
			names = append(names, machine.Name)
		}
		order = append(order, "started:"+strings.Join(names, "+"))
		return nil
	})
	event.Subscribe(m.dispatcher, func(e event.MachineDeployed) error {
		if e.Machine != nil {
			t.Error("machine_deployed carried a Machine; Megalos dispatches the name")
		}
		order = append(order, "deployed:"+e.Name)
		return nil
	})
	event.Subscribe(m.dispatcher, func(event.MachinesDeployEnded) error {
		order = append(order, "ended")
		return nil
	})

	lab := newTestLab(t, s)
	for _, name := range []string{"pc1", "pc2"} {
		if _, err := lab.NewMachine(name, nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
	}

	injectPodsReady(clientset, lab.Hash, "pc1")

	if err := m.machine.DeployMachines(context.Background(), lab, kathara.NewNameSet("pc1"), nil); err != nil {
		t.Fatalf("DeployMachines: %v", err)
	}
	want := "started:pc1,deployed:pc1,ended"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// TestDeployMachinesLeavesNoWatcherBehind pins the half of DIVERGENCES.md 72
// that is not the watchdog: a fan-out failure stops the watcher and returns the
// worker's error PROMPTLY, where Python skips the join and leaks the thread
// until its own 180 s timer fires.
func TestDeployMachinesLeavesNoWatcherBehind(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	clientset.PrependReactor("create", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewConflict(
			schema.GroupResource{Group: "apps", Resource: "deployments"}, "d", errors.New("boom"))
	})

	lab := newTestLab(t, s)
	if _, err := lab.NewMachine("pc1", nil); err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	// No Ready events are injected: the point is that the call does not wait
	// for them.

	done := make(chan error, 1)
	go func() { done <- m.machine.DeployMachines(context.Background(), lab, nil, nil) }()

	select {
	case err := <-done:
		if !errors.Is(err, kerrors.ErrMachineAlreadyExists) {
			t.Fatalf("error = %v, want MachineAlreadyExists", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("DeployMachines waited for the watchdog after a fan-out failure")
	}
}

// TestDeployMachinesWatchdog is the OQ-10 ruling: with no pod events at all the
// deploy ends after [maxTimeError] with [context.DeadlineExceeded], not with a
// process-wide SIGINT.
//
// The timer is 180 s, so the test drives the watcher directly rather than
// waiting for it.
func TestDeployMachinesWatchdog(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	if _, err := lab.NewMachine("pc1", nil); err != nil {
		t.Fatalf("NewMachine: %v", err)
	}

	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	// A watcher that never produces an event: the timer is the only way out.
	watcher := &neverWatcher{done: make(chan struct{})}
	defer close(watcher.done)

	m.machine.startupTimeout = 20 * time.Millisecond
	m.machine.waitMachinesStartup(ctx, cancel, watcher, lab, nil)

	if cause := context.Cause(ctx); !errors.Is(cause, context.DeadlineExceeded) {
		t.Fatalf("cause = %v, want DeadlineExceeded", cause)
	}
}

// TestUndeployFilters is EXPECTATIONS-k8s §1 "undeploy": which pods are
// deleted, and the `is not None` guard that makes two empty sets an error here
// where they are "no filter" on the deploy path.
func TestUndeployFilters(t *testing.T) {
	tests := []struct {
		name     string
		pods     []string
		selected kathara.NameSet
		excluded kathara.NameSet
		want     []string
		// watched is the wait set the call blocks on, which is NOT `want`: with
		// a selection it is the caller's names, with an exclusion it is the
		// complement of the exclusion over every listed pod, and with no filter
		// it is every listed pod (`KubernetesMachine.py:590-596`).
		watched []string
		wantErr error
	}{
		{
			name: "no filter deletes every pod", pods: []string{"a", "b", "c"},
			want: []string{"a", "b", "c"}, watched: []string{"a", "b", "c"},
		},
		{name: "no pods deletes nothing", pods: nil, want: nil},
		{
			name: "selected", pods: []string{"a", "b", "c"}, selected: kathara.NewNameSet("a"),
			want: []string{"a"}, watched: []string{"a"},
		},
		{
			name: "excluded", pods: []string{"a", "b", "c"}, excluded: kathara.NewNameSet("a"),
			want: []string{"b", "c"}, watched: []string{"b", "c"},
		},
		{
			name:     "both filters",
			pods:     []string{"a"},
			selected: kathara.NewNameSet("a"),
			excluded: kathara.NewNameSet("b"),
			wantErr:  kerrors.ErrSelectedOrExcludedMachines,
		},
		{
			// `is not None` on this path: two non-nil empty sets are refused
			// even though the manager's own pre-check lets them through
			// (k8s-backend.md G3).
			name:     "two empty sets are refused",
			pods:     []string{"a"},
			selected: kathara.NewNameSet(),
			excluded: kathara.NewNameSet(),
			wantErr:  kerrors.ErrSelectedOrExcludedMachines,
		},
		{
			// A non-nil EMPTY selection matches nothing.
			name:     "empty selection deletes nothing",
			pods:     []string{"a"},
			selected: kathara.NewNameSet(),
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			objects := make([]runtime.Object, 0, 2*len(test.pods))
			for _, name := range test.pods {
				objects = append(objects, newTestPod(defaultScenarioHash, name), newTestDeployment(defaultScenarioHash, name))
			}
			m, clientset, _, _ := newTestManager(t, s, objects...)
			injectPodsDeleted(clientset, defaultScenarioHash, test.watched...)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := m.machine.Undeploy(ctx, defaultScenarioHash, test.selected, test.excluded)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Undeploy: %v", err)
			}

			got := deletedDeployments(clientset)
			want := make([]string, 0, len(test.want))
			for _, name := range test.want {
				want = append(want, DeploymentName("devprefix", name))
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("deleted %v, want %v", got, want)
			}
		})
	}
}

// TestUndeployDispatchesEventsAndWaits is the undeploy twin of
// [TestDeployMachinesDispatchesEvents]: `wait_thread.join()`
// (`KubernetesMachine.py:609`) is unconditional on the success path, so the call
// BLOCKS until every watched device has produced its DELETED event and all three
// events are observable by the time it returns.
func TestUndeployDispatchesEventsAndWaits(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s,
		newTestPod(defaultScenarioHash, "pc1"), newTestDeployment(defaultScenarioHash, "pc1"),
		newTestPod(defaultScenarioHash, "pc2"), newTestDeployment(defaultScenarioHash, "pc2"),
	)

	var mu sync.Mutex
	var order []string
	record := func(entry string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, entry)
	}
	event.Subscribe(m.dispatcher, func(e event.MachinesUndeployStarted) error {
		names := slices.Clone(e.Names)
		slices.Sort(names)
		record("started:" + strings.Join(names, "+"))
		return nil
	})
	event.Subscribe(m.dispatcher, func(e event.MachineUndeployed) error {
		record("undeployed:" + e.Name)
		return nil
	})
	event.Subscribe(m.dispatcher, func(event.MachinesUndeployEnded) error {
		record("ended")
		return nil
	})

	// Held back past the fan-out, as a cluster holds them back: a call that
	// cancels the watcher after the deletions sees none of the three events.
	injectPodsDeletedAfter(clientset, 50*time.Millisecond, defaultScenarioHash, "pc1", "pc2")

	if err := m.machine.Undeploy(context.Background(), defaultScenarioHash, nil, nil); err != nil {
		t.Fatalf("Undeploy: %v", err)
	}

	// No synchronisation beyond the call itself: if `Undeploy` returned before
	// joining the watcher, the two DELETED events would still be in flight.
	mu.Lock()
	defer mu.Unlock()
	want := "started:pc1+pc2,undeployed:pc1,undeployed:pc2,ended"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// TestUndeployLeavesNoWatcherBehind is the undeploy half of DIVERGENCES.md 72:
// a fan-out failure is reported PROMPTLY, without waiting out the shutdown
// watchdog — Python skips `join()` there and leaks the thread instead.
func TestUndeployLeavesNoWatcherBehind(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s,
		newTestPod(defaultScenarioHash, "pc1"), newTestDeployment(defaultScenarioHash, "pc1"))
	clientset.PrependReactor("delete", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewInternalError(errors.New("boom"))
	})
	// No DELETED events: the point is that the call does not wait for them.
	m.machine.shutdownTimeout = time.Minute

	done := make(chan error, 1)
	go func() { done <- m.machine.Undeploy(context.Background(), defaultScenarioHash, nil, nil) }()

	select {
	case err := <-done:
		if !errors.Is(err, kerrors.ErrKubernetesAPI) {
			t.Fatalf("error = %v, want KubernetesAPI", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Undeploy waited for the watchdog after a fan-out failure")
	}
}

// TestUndeployWatchdog is CONCURRENCY.tsv row `KubernetesMachine.py:599`: with
// no DELETED event at all the wait ends after the timeout instead of hanging
// forever as Python's `join()` does, and the undeploy still succeeds.
func TestUndeployWatchdog(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s,
		newTestPod(defaultScenarioHash, "pc1"), newTestDeployment(defaultScenarioHash, "pc1"))
	m.machine.shutdownTimeout = 20 * time.Millisecond

	var ended atomic.Bool
	event.Subscribe(m.dispatcher, func(event.MachinesUndeployEnded) error {
		ended.Store(true)
		return nil
	})

	done := make(chan error, 1)
	go func() { done <- m.machine.Undeploy(context.Background(), defaultScenarioHash, nil, nil) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Undeploy: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the shutdown wait did not time out")
	}
	if ended.Load() {
		t.Error("machines_undeploy_ended fired without a DELETED event")
	}
}

// TestUndeployUnlabelledPodIsAKeyError is `{item.metadata.labels["name"] for
// item in pods}` (`KubernetesMachine.py:590`) on a foreign pod that carries
// `app=kathara` and no `name`: Python raises KeyError out of `undeploy` and so
// does this — unlike the two watchers, which skip it (DIVERGENCES.md 73).
func TestUndeployUnlabelledPodIsAKeyError(t *testing.T) {
	s := testSettings()
	foreign := newTestPod(defaultScenarioHash, "pc1")
	foreign.Labels = map[string]string{labelApp: labelAppValue}

	m, clientset, _, _ := newTestManager(t, s, foreign)

	err := m.machine.Undeploy(context.Background(), defaultScenarioHash, nil, nil)
	if !errors.Is(err, model.ErrPyKeyError) {
		t.Fatalf("error = %v, want a KeyError", err)
	}
	if len(deletedDeployments(clientset)) != 0 {
		t.Error("a Deployment was deleted despite the crash")
	}
}

// TestUndeployAPIErrorsCarryThePassthroughCode is ERROR_CODES.md §1.3: an
// `ApiException` nobody translated reaches the CLI as `KubernetesAPI` /
// `ApiException`, not as the `InternalError` fallback — the pod listing and the
// Deployment delete are the two undeploy sites that can raise one.
func TestUndeployAPIErrorsCarryThePassthroughCode(t *testing.T) {
	tests := []struct {
		name     string
		verb     string
		resource string
	}{
		{name: "pod listing", verb: "list", resource: "pods"},
		{name: "deployment delete", verb: "delete", resource: "deployments"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, clientset, _, _ := newTestManager(t, s,
				newTestPod(defaultScenarioHash, "pc1"), newTestDeployment(defaultScenarioHash, "pc1"))
			clientset.PrependReactor(test.verb, test.resource, func(k8stesting.Action) (bool, runtime.Object, error) {
				return true, nil, apierrors.NewNotFound(
					schema.GroupResource{Resource: test.resource}, "x")
			})
			m.machine.shutdownTimeout = time.Second

			err := m.machine.Undeploy(context.Background(), defaultScenarioHash, nil, nil)
			if !errors.Is(err, kerrors.ErrKubernetesAPI) {
				t.Fatalf("error = %v, want KubernetesAPI", err)
			}
			// The cause stays reachable, so an outer translation rule can still
			// read the status.
			if !isAPIException(err) {
				t.Error("the wrap hid the ApiException from the discrimination helpers")
			}
		})
	}
}

// TestBuildDefinitionRejectsUnsupportedMemoryUnit is the `mem` unit Kubernetes
// has no suffix for. `Machine.get_mem` accepts b/k/m/g
// (`model/Machine.py:499`), and `memory.upper()` turns `100k` into `100K` —
// which Python posts and the API server refuses with an ApiException, and which
// client-go refuses locally. Same code either way (DIVERGENCES.md 87).
func TestBuildDefinitionRejectsUnsupportedMemoryUnit(t *testing.T) {
	for _, mem := range []string{"100k", "5b"} {
		t.Run(mem, func(t *testing.T) {
			s := testSettings()
			m, clientset, _, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab, [2]string{"mem", mem})

			err := m.machine.Create(context.Background(), device)
			if !errors.Is(err, kerrors.ErrKubernetesAPI) {
				t.Fatalf("Create error = %v, want KubernetesAPI", err)
			}
			for _, action := range clientset.Actions() {
				if action.GetVerb() == "create" && action.GetResource().Resource == "deployments" {
					t.Fatal("a Deployment was submitted with an unparseable memory limit")
				}
			}
		})
	}

	// `g` and `m` DO parse, so the neighbouring units still deploy.
	for _, mem := range []string{"64m", "2g"} {
		t.Run(mem, func(t *testing.T) {
			s := testSettings()
			m, _, _, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			device := newBaseDevice(t, lab, [2]string{"mem", mem})

			if err := m.machine.Create(context.Background(), device); err != nil {
				t.Fatalf("Create: %v", err)
			}
		})
	}
}

// deletedDeployments is the name of every Deployment the fake clientset was
// asked to delete, sorted so that the fan-out's completion order does not make
// the assertion flaky.
func deletedDeployments(clientset actionRecorder) []string {
	var names []string
	for _, action := range clientset.Actions() {
		del, ok := action.(k8stesting.DeleteAction)
		if !ok || action.GetResource().Resource != "deployments" {
			continue
		}
		names = append(names, del.GetName())
	}
	slices.Sort(names)
	return names
}

// TestWipeMachines is EXPECTATIONS-k8s §1 "wipe": every Kathará pod in every
// Kathará namespace, one delete each.
func TestWipeMachines(t *testing.T) {
	tests := []struct {
		name string
		pods []string
	}{
		{name: "no devices", pods: nil},
		{name: "one device", pods: []string{"a"}},
		{name: "three devices", pods: []string{"a", "b", "c"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			objects := []runtime.Object{&corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: defaultScenarioHash, Labels: map[string]string{labelApp: labelAppValue}},
			}}
			for _, name := range test.pods {
				objects = append(objects, newTestPod(defaultScenarioHash, name), newTestDeployment(defaultScenarioHash, name))
			}
			m, clientset, _, _ := newTestManager(t, s, objects...)

			if err := m.machine.Wipe(context.Background()); err != nil {
				t.Fatalf("Wipe: %v", err)
			}
			if got := len(deletedDeployments(clientset)); got != len(test.pods) {
				t.Errorf("%d deletes, want %d", got, len(test.pods))
			}
		})
	}
}

// TestGetMachinesByFilters is EXPECTATIONS-k8s §1
// "get_machines_api_objects_by_filters": the exact label selector, and the
// namespace enumeration that a lab hash skips.
func TestGetMachinesByFilters(t *testing.T) {
	s := testSettings()
	objects := []runtime.Object{
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1", Labels: map[string]string{labelApp: labelAppValue}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns2", Labels: map[string]string{labelApp: labelAppValue}}},
		newTestPod("ns1", "pc1"),
		newTestPod("ns2", "pc2"),
	}

	tests := []struct {
		name         string
		labHash      string
		machineName  string
		wantSelector string
		wantListed   int
	}{
		{name: "no filter enumerates namespaces", wantSelector: "app=kathara", wantListed: 2},
		{name: "machine name only", machineName: "test_device", wantSelector: "app=kathara,name=test_device", wantListed: 2},
		{name: "lab hash skips enumeration", labHash: "ns1", wantSelector: "app=kathara", wantListed: 1},
		{name: "both", labHash: "ns1", machineName: "pc1", wantSelector: "app=kathara,name=pc1", wantListed: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m, clientset, _, _ := newTestManager(t, s, objects...)
			if _, err := m.machine.getByFilters(context.Background(), test.labHash, test.machineName); err != nil {
				t.Fatalf("getByFilters: %v", err)
			}

			listed := 0
			for _, action := range clientset.Actions() {
				list, ok := action.(k8stesting.ListAction)
				if !ok || action.GetResource().Resource != "pods" {
					continue
				}
				listed++
				if got := list.GetListRestrictions().Labels.String(); got != test.wantSelector {
					t.Errorf("selector = %q, want %q", got, test.wantSelector)
				}
			}
			if listed != test.wantListed {
				t.Errorf("%d pod listings, want %d", listed, test.wantListed)
			}
		})
	}
}

// TestDeploymentNameOnUndeployUsesRealName pins the ConfigMap-name asymmetry of
// [ConfigMapName]: the create path reads `real_name` and the delete path
// recomputes `get_deployment_name(machine_name)` — two different expressions
// that must produce the same string, `devprefix-test-device-ec84ad3b`, or the
// ConfigMap leaks on every teardown.
func TestDeploymentNameOnUndeployUsesRealName(t *testing.T) {
	s := testSettings()
	pod := newTestPod(defaultScenarioHash, "test_device")
	m, clientset, _, _ := newTestManager(t, s, pod, newTestDeployment(defaultScenarioHash, "test_device"))

	if err := m.machine.undeployMachine(context.Background(), pod); err != nil {
		t.Fatalf("undeployMachine: %v", err)
	}

	want := ConfigMapName(DeploymentName("devprefix", "test_device"), defaultScenarioHash)
	found := false
	for _, action := range clientset.Actions() {
		del, ok := action.(k8stesting.DeleteAction)
		if !ok || action.GetResource().Resource != "configmaps" {
			continue
		}
		found = true
		if del.GetName() != want {
			t.Errorf("deleted config map %q, want %q", del.GetName(), want)
		}
	}
	if !found {
		t.Fatal("no config map delete was issued")
	}
}

// TestUndeployMachineRunsShutdownCommands pins the shutdown exec: the command
// is `[shell, "-c", SHUTDOWN_COMMANDS]` with the shell taken from the pod's
// `_MEGALOS_SHELL`.
func TestUndeployMachineRunsShutdownCommands(t *testing.T) {
	s := testSettings()
	pod := newTestPod(defaultScenarioHash, "test_device")
	m, _, _, executor := newTestManager(t, s, pod, newTestDeployment(defaultScenarioHash, "test_device"))

	if err := m.machine.undeployMachine(context.Background(), pod); err != nil {
		t.Fatalf("undeployMachine: %v", err)
	}

	req := executor.lastRequest(t)
	want := []string{"/bin/bash", "-c", ShutdownCommandsString("test_device")}
	if strings.Join(req.Command, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("command = %q, want %q", req.Command, want)
	}
}

// neverWatcher is a [watch.Interface] that produces no event at all — the
// cluster the 180 s watchdog exists for.
type neverWatcher struct{ done chan struct{} }

func (w *neverWatcher) Stop() {}

func (w *neverWatcher) ResultChan() <-chan watch.Event { return make(chan watch.Event) }
