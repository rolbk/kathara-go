package kubernetes

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"

	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// TestBackendRow pins the registry row `cmd/kathara` registers.
func TestBackendRow(t *testing.T) {
	backend := Backend()
	if backend.Name != "kubernetes" {
		t.Errorf("name = %q, want %q", backend.Name, "kubernetes")
	}
	if backend.FormattedName != "Kubernetes (Megalos)" {
		t.Errorf("formatted name = %q", backend.FormattedName)
	}
	if backend.New == nil {
		t.Error("no constructor")
	}
}

// TestLabHashResolution is EXPECTATIONS-k8s §4 "Lab-identity resolution": one
// of `lab`, `lab_name`, `lab_hash`, resolved in that precedence and then
// LOWERCASED — the fold that makes Megalos' effective scenario id different
// from the Docker backend's (k8s-backend.md G1).
func TestLabHashResolution(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)

	tests := []struct {
		name string
		ref  kathara.LabRef
		want string
	}{
		{name: "hash is folded", ref: kathara.LabRef{Hash: "FwFaxbiuhvSWb2KpN5zw"}, want: "fwfaxbiuhvswb2kpn5zw"},
		{name: "name is hashed then folded", ref: kathara.LabRef{Name: "default_scenario"}, want: "fwfaxbiuhvswb2kpn5zw"},
		{name: "object wins and is folded", ref: kathara.LabRef{Lab: lab}, want: "fwfaxbiuhvswb2kpn5zw"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveRequired(test.ref)
			if err != nil {
				t.Fatalf("resolveRequired: %v", err)
			}
			if got != test.want {
				t.Errorf("hash = %q, want %q", got, test.want)
			}
		})
	}

	t.Run("no identity is an InvocationError", func(t *testing.T) {
		if _, err := resolveRequired(kathara.LabRef{}); !errors.Is(err, kerrors.ErrInvocation) {
			t.Fatalf("error = %v, want an InvocationError", err)
		}
	})

	t.Run("two identities are an InvocationError", func(t *testing.T) {
		ref := kathara.LabRef{Hash: "h", Name: "n"}
		if _, err := resolveRequired(ref); !errors.Is(err, kerrors.ErrInvocation) {
			t.Fatalf("error = %v, want an InvocationError", err)
		}
	})

	t.Run("at-most-one accepts nothing", func(t *testing.T) {
		got, err := resolveAtMostOne(kathara.LabRef{})
		if err != nil || got != "" {
			t.Fatalf("resolveAtMostOne(nothing) = %q, %v; want \"\", nil", got, err)
		}
	})
}

// TestLowerLabHashMutates pins k8s-backend.md G1: the deploy and undeploy
// object paths fold the hash ON THE SHARED SCENARIO OBJECT, not on a local, so
// a caller that holds the same [model.Lab] sees the folded value afterwards.
func TestLowerLabHashMutates(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	lowerLabHash(lab)
	if lab.Hash != strings.ToLower(defaultScenarioHash) {
		t.Errorf("lab.Hash = %q, want the folded form", lab.Hash)
	}
}

// TestDeployLabValidationOrder is EXPECTATIONS-k8s §4 "deploy_lab": the order
// errors surface in, and that nothing at all is created when one fires.
func TestDeployLabValidationOrder(t *testing.T) {
	tests := []struct {
		name string
		opts kathara.DeployLabOptions
		want error
	}{
		{
			name: "both filters",
			opts: kathara.DeployLabOptions{
				SelectedMachines: kathara.NewNameSet("pc1"),
				ExcludedMachines: kathara.NewNameSet("pc2"),
			},
			want: kerrors.ErrSelectOrExcludeDevices,
		},
		{
			name: "unknown selected device",
			opts: kathara.DeployLabOptions{SelectedMachines: kathara.NewNameSet("pc3")},
			want: kerrors.ErrMachineNotFound,
		},
		{
			name: "unknown excluded device",
			opts: kathara.DeployLabOptions{ExcludedMachines: kathara.NewNameSet("pc3")},
			want: kerrors.ErrMachineNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, clientset, dynamicClient, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			for _, name := range []string{"pc1", "pc2"} {
				if _, err := lab.NewMachine(name, nil); err != nil {
					t.Fatalf("NewMachine: %v", err)
				}
			}

			err := m.DeployLab(context.Background(), lab, test.opts)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if len(deployedNames(clientset)) != 0 || len(createdNetworks(dynamicClient)) != 0 {
				t.Error("objects were created despite the validation failure")
			}
			for _, action := range clientset.Actions() {
				if action.GetVerb() == "create" {
					t.Errorf("a %s was created despite the validation failure", action.GetResource().Resource)
				}
			}
		})
	}
}

// TestDeployLabLinkNarrowing is EXPECTATIONS-k8s §4 `test_deploy_lab_*`: the
// collision-domain scope each machine filter produces.
//
// The exclusion arm is the interesting one: a domain shared with a device that
// is STILL being deployed survives, so only domains used exclusively by
// excluded devices are dropped.
func TestDeployLabLinkNarrowing(t *testing.T) {
	tests := []struct {
		name string
		opts kathara.DeployLabOptions
		want []string
	}{
		{
			name: "no filter deploys every collision domain",
			want: []string{"netprefix-a", "netprefix-b", "netprefix-c"},
		},
		{
			name: "selected devices narrow to the domains they touch",
			opts: kathara.DeployLabOptions{SelectedMachines: kathara.NewNameSet("pc1")},
			want: []string{"netprefix-a", "netprefix-b"},
		},
		{
			// pc3 is on A and C; A survives because pc1 and pc2 use it.
			name: "excluded devices drop only the domains they alone use",
			opts: kathara.DeployLabOptions{ExcludedMachines: kathara.NewNameSet("pc3")},
			want: []string{"netprefix-a", "netprefix-b"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, clientset, dynamicClient, _ := newTestManager(t, s)
			injectNamespaceActive(clientset, strings.ToLower(defaultScenarioHash))
			injectPodsReady(clientset, strings.ToLower(defaultScenarioHash), "pc1", "pc2", "pc3")
			lab := newTestLab(t, s)

			// pc1 on A,B; pc2 on A; pc3 on A,C.
			wiring := map[string][]string{"pc1": {"A", "B"}, "pc2": {"A"}, "pc3": {"A", "C"}}
			for _, name := range []string{"pc1", "pc2", "pc3"} {
				for _, cd := range wiring[name] {
					if _, _, err := lab.ConnectMachineToLink(name, cd, model.AddInterfaceOptions{}); err != nil {
						t.Fatalf("ConnectMachineToLink: %v", err)
					}
				}
			}

			// The exclusion arm computes the domains of the REMAINING devices,
			// which for `excluded={pc3}` are pc1's and pc2's — so `netprefix-c`
			// is the only one dropped.
			if err := m.DeployLab(context.Background(), lab, test.opts); err != nil {
				t.Fatalf("DeployLab: %v", err)
			}
			if got := createdNetworks(dynamicClient); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("created %v, want %v", got, test.want)
			}
		})
	}
}

// TestDeployLabOrderIsNamespaceSecretLinksMachines is EXPECTATIONS-k8s §4
// "Operation order (all deploy tests)".
func TestDeployLabOrderIsNamespaceSecretLinksMachines(t *testing.T) {
	s := testSettings()
	s.DockerConfigJSON = ptr("eyJhdXRocyI6IHt9fQ==")
	m, clientset, _, _ := newTestManager(t, s)
	injectNamespaceActive(clientset, strings.ToLower(defaultScenarioHash))
	injectSecretAdded(clientset, strings.ToLower(defaultScenarioHash), privateRegistrySecretName)
	injectPodsReady(clientset, strings.ToLower(defaultScenarioHash), "pc1")

	lab := newTestLab(t, s)
	if _, _, err := lab.ConnectMachineToLink("pc1", "A", model.AddInterfaceOptions{}); err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}

	if err := m.DeployLab(context.Background(), lab, kathara.DeployLabOptions{}); err != nil {
		t.Fatalf("DeployLab: %v", err)
	}

	var order []string
	for _, action := range clientset.Actions() {
		if action.GetVerb() != "create" {
			continue
		}
		order = append(order, action.GetResource().Resource)
	}
	want := "namespaces,secrets,deployments"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("create order = %q, want %q", got, want)
	}
}

// TestDeployLabForbiddenIsLabTerminating is `KubernetesManager.py:141-145`: the
// 403 the API server answers while a namespace is Terminating.
func TestDeployLabForbiddenIsLabTerminating(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	injectNamespaceActive(clientset, strings.ToLower(defaultScenarioHash))
	clientset.PrependReactor("create", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "apps", Resource: "deployments"}, "d", errors.New("terminating"))
	})

	lab := newTestLab(t, s)
	if _, err := lab.NewMachine("pc1", nil); err != nil {
		t.Fatalf("NewMachine: %v", err)
	}

	err := m.DeployLab(context.Background(), lab, kathara.DeployLabOptions{})
	if !errors.Is(err, kerrors.ErrLabAlreadyExists) {
		t.Fatalf("error = %v, want LabAlreadyExists", err)
	}
}

// TestTranslateForbiddenTranslatesABatchElementwise is the interaction between
// `deploy_lab`'s `except ApiException` and the batch errors of
// ERROR_CODES.md §6: a half-failed chunk reports a JOIN, and `errors.As` walks
// into every branch of one.
//
// So the translation has to be applied per element. Applied to the join as a
// whole, one device's 403 would answer `isForbidden` for the batch and replace
// all of it — the primary error included — with a single
// [kerrors.ErrLabTerminating], which is neither Python's behaviour nor §6.5's
// `errors` array.
func TestTranslateForbiddenTranslatesABatchElementwise(t *testing.T) {
	primary := kerrors.NewMachineBinary("frr", "pc1")
	forbidden := apierrors.NewForbidden(
		schema.GroupResource{Group: "apps", Resource: "deployments"}, "pc2", errors.New("terminating"))

	got := translateForbidden(errors.Join(primary, forbidden))

	batch := kerrors.Joined(got)
	if len(batch) != 2 {
		t.Fatalf("the batch carries %d errors, want both of them", len(batch))
	}
	if !errors.Is(batch[0], kerrors.ErrMachineBinary) {
		t.Errorf("element 0 = %v, want the untouched primary", batch[0])
	}
	if !errors.Is(batch[1], kerrors.ErrLabTerminating) {
		t.Errorf("element 1 = %v, want the 403 translated to LabTerminating", batch[1])
	}
	if got := kerrors.Code(got); got != kerrors.CodeMachineBinary {
		t.Errorf("Code = %q, want the primary's %q", got, kerrors.CodeMachineBinary)
	}
}

// TestDeployMachineAndLinkGuards is EXPECTATIONS-k8s §4 "deploy_machine /
// deploy_link": the LabNotFound spellings, which differ between the two.
func TestDeployMachineAndLinkGuards(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	t.Run("device without a scenario", func(t *testing.T) {
		err := m.DeployMachine(context.Background(), &model.Machine{Name: "pc1"})
		if !errors.Is(err, kerrors.ErrLabNotFound) {
			t.Fatalf("error = %v, want LabNotFound", err)
		}
		if !strings.Contains(err.Error(), "Machine `pc1`") {
			t.Errorf("message = %q, want the \"Machine\" spelling", err.Error())
		}
	})

	t.Run("collision domain without a scenario", func(t *testing.T) {
		err := m.DeployLink(context.Background(), &model.Link{Name: "A"})
		if !errors.Is(err, kerrors.ErrLabNotFound) {
			t.Fatalf("error = %v, want LabNotFound", err)
		}
		if !strings.Contains(err.Error(), "Collision domain `A`") {
			t.Errorf("message = %q, want the \"Collision domain\" spelling", err.Error())
		}
	})
}

// TestDeployLinkCreatesNoSecret pins `KubernetesManager.py:82-83`: a collision
// domain pulls no image, so the private-registry Secret is not created.
func TestDeployLinkCreatesNoSecret(t *testing.T) {
	s := testSettings()
	s.DockerConfigJSON = ptr("eyJhdXRocyI6IHt9fQ==")
	m, clientset, _, _ := newTestManager(t, s)
	injectNamespaceActive(clientset, strings.ToLower(defaultScenarioHash))

	lab := newTestLab(t, s)
	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	if err := m.DeployLink(context.Background(), link); err != nil {
		t.Fatalf("DeployLink: %v", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("deploy_link created a Secret")
		}
	}
}

// TestNotSupported is EXPECTATIONS-k8s §4 "connect/disconnect machine-link" and
// "update_lab_from_api": three permanent refusals, not deferrals.
func TestNotSupported(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)
	ctx := context.Background()

	if err := m.ConnectMachineToLink(ctx, nil, nil, ""); !errors.Is(err, kerrors.ErrNotSupported) {
		t.Errorf("ConnectMachineToLink = %v, want NotSupported", err)
	}
	if err := m.DisconnectMachineFromLink(ctx, nil, nil, false); !errors.Is(err, kerrors.ErrNotSupported) {
		t.Errorf("DisconnectMachineFromLink = %v, want NotSupported", err)
	}
	if err := m.UpdateLabFromAPI(ctx, nil); !errors.Is(err, kerrors.ErrNotSupported) {
		t.Errorf("UpdateLabFromAPI = %v, want NotSupported", err)
	}
}

// TestUndeployMachineLinkGC is EXPECTATIONS-k8s §4 "undeploy_machine": a
// collision domain is deleted unless a still-running pod references it, and the
// namespace goes only when the departing device was the last one.
func TestUndeployMachineLinkGC(t *testing.T) {
	tests := []struct {
		name          string
		otherPods     map[string][]string // device -> attached network names
		keepLinks     bool
		wantNetworks  []string
		wantNamespace bool
	}{
		{
			name:          "last device deletes its domain and the namespace",
			wantNetworks:  []string{"netprefix-a"},
			wantNamespace: true,
		},
		{
			name:          "a surviving device keeps the shared domain",
			otherPods:     map[string][]string{"pc2": {"netprefix-a"}},
			wantNetworks:  nil,
			wantNamespace: false,
		},
		{
			name:          "keep_links keeps everything",
			keepLinks:     true,
			wantNetworks:  nil,
			wantNamespace: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			lab := newTestLab(t, s)
			device, _, err := lab.ConnectMachineToLink("pc1", "A", model.AddInterfaceOptions{})
			if err != nil {
				t.Fatalf("ConnectMachineToLink: %v", err)
			}

			hash := strings.ToLower(lab.Hash)
			objects := []runtime.Object{
				newTestPod(hash, "pc1", podNetworkAttachment{Name: "netprefix-a", Namespace: hash, Interface: "net0", KatharaLink: "A"}),
				newTestDeployment(hash, "pc1"),
			}
			for other, networks := range test.otherPods {
				attachments := make([]podNetworkAttachment, 0, len(networks))
				for i, network := range networks {
					attachments = append(attachments, podNetworkAttachment{
						Name: network, Namespace: hash, Interface: "net" + itoaTest(i), KatharaLink: "A",
					})
				}
				objects = append(objects, newTestPod(hash, other, attachments...), newTestDeployment(hash, other))
			}

			m, clientset, _, _ := newTestManager(t, s, objects...)
			dynamicClient := newFakeDynamic(newTestNetwork(hash, "A", 1))
			m.link.dynamic = dynamicClient
			// `undeploy(selected_machines={pc1})` blocks on pc1's DELETED event.
			injectPodsDeleted(clientset, hash, "pc1")

			if err := m.UndeployMachine(context.Background(), device, test.keepLinks); err != nil {
				t.Fatalf("UndeployMachine: %v", err)
			}

			if got := deletedNetworks(dynamicClient); strings.Join(got, ",") != strings.Join(test.wantNetworks, ",") {
				t.Errorf("deleted networks %v, want %v", got, test.wantNetworks)
			}
			if got := namespaceDeleted(clientset); got != test.wantNamespace {
				t.Errorf("namespace deleted = %v, want %v", got, test.wantNamespace)
			}
		})
	}
}

func itoaTest(i int) string { return strconv.Itoa(i) }

// sortedMapKeys keeps the fixture deterministic: a Go map has no order and the
// pods it builds decide which network survives a partial teardown.
func sortedMapKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

func namespaceDeleted(clientset actionRecorder) bool {
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "namespaces" {
			return true
		}
	}
	return false
}

// TestUndeployLinkSkipsWhenStillUsed is EXPECTATIONS-k8s §4
// `test_undeploy_link_machine_running`: a silent no-op — no deletion, no error,
// no event — when a running pod still references the collision domain.
func TestUndeployLinkSkipsWhenStillUsed(t *testing.T) {
	s := testSettings()
	lab := newTestLab(t, s)
	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	hash := strings.ToLower(lab.Hash)

	pod := newTestPod(hash, "pc1", podNetworkAttachment{
		Name: "netprefix-a", Namespace: hash, Interface: "net0", KatharaLink: "A",
	})
	m, _, _, _ := newTestManager(t, s, pod)
	dynamicClient := newFakeDynamic(newTestNetwork(hash, "A", 1))
	m.link.dynamic = dynamicClient

	if err := m.UndeployLink(context.Background(), link); err != nil {
		t.Fatalf("UndeployLink: %v", err)
	}
	if got := deletedNetworks(dynamicClient); len(got) != 0 {
		t.Errorf("deleted %v, want nothing", got)
	}
}

// TestUndeployLabMatrix is EXPECTATIONS-k8s §4 "undeploy_lab": the ten rows of
// the partial-teardown table, reduced to the three questions each of them asks —
// which networks are deleted, and whether the namespace goes.
func TestUndeployLabMatrix(t *testing.T) {
	// pods: pc1(a,b), pc2(a), pc3(c,d)
	pods := map[string][]string{
		"pc1": {"netprefix-a", "netprefix-b"},
		"pc2": {"netprefix-a"},
		"pc3": {"netprefix-c", "netprefix-d"},
	}
	networks := []string{"A", "B", "C", "D"}

	tests := []struct {
		name          string
		opts          kathara.UndeployLabOptions
		wantNetworks  []string
		wantNamespace bool
	}{
		{
			name:          "no selection deletes everything",
			wantNetworks:  []string{"netprefix-a", "netprefix-b", "netprefix-c", "netprefix-d"},
			wantNamespace: true,
		},
		{
			name:          "selected pc1 keeps a (pc2) and c,d (pc3)",
			opts:          kathara.UndeployLabOptions{SelectedMachines: kathara.NewNameSet("pc1")},
			wantNetworks:  []string{"netprefix-b"},
			wantNamespace: false,
		},
		{
			name: "selecting every device deletes every network and the namespace",
			opts: kathara.UndeployLabOptions{
				SelectedMachines: kathara.NewNameSet("pc1", "pc2", "pc3"),
			},
			wantNetworks:  []string{"netprefix-a", "netprefix-b", "netprefix-c", "netprefix-d"},
			wantNamespace: true,
		},
		{
			name:          "excluding pc3 keeps c,d",
			opts:          kathara.UndeployLabOptions{ExcludedMachines: kathara.NewNameSet("pc3")},
			wantNetworks:  []string{"netprefix-a", "netprefix-b"},
			wantNamespace: false,
		},
		{
			name:          "selected_links names a collision domain and keeps the namespace",
			opts:          kathara.UndeployLabOptions{SelectedLinks: kathara.NewNameSet("B")},
			wantNetworks:  []string{"netprefix-b"},
			wantNamespace: false,
		},
		{
			name: "selected machines and links intersect",
			opts: kathara.UndeployLabOptions{
				SelectedMachines: kathara.NewNameSet("pc1"),
				SelectedLinks:    kathara.NewNameSet("B"),
			},
			wantNetworks:  []string{"netprefix-b"},
			wantNamespace: false,
		},
		{
			name: "an intersection that is empty deletes nothing",
			opts: kathara.UndeployLabOptions{
				SelectedMachines: kathara.NewNameSet("pc1"),
				SelectedLinks:    kathara.NewNameSet("A"),
			},
			wantNetworks:  nil,
			wantNamespace: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			hash := strings.ToLower(defaultScenarioHash)

			objects := []runtime.Object{}
			for _, name := range sortedMapKeys(pods) {
				attachments := make([]podNetworkAttachment, 0, len(pods[name]))
				for i, network := range pods[name] {
					attachments = append(attachments, podNetworkAttachment{
						Name: network, Namespace: hash, Interface: "net" + itoaTest(i), KatharaLink: strings.ToUpper(network[len("netprefix-"):]),
					})
				}
				objects = append(objects, newTestPod(hash, name, attachments...), newTestDeployment(hash, name))
			}

			m, clientset, _, _ := newTestManager(t, s, objects...)
			nads := make([]*Network, 0, len(networks))
			for _, cd := range networks {
				nads = append(nads, newTestNetwork(hash, cd, 1))
			}
			dynamicClient := newFakeDynamic(nads...)
			m.link.dynamic = dynamicClient
			// The wait set varies by row — the selection, the complement of the
			// exclusion, or every pod — so every pod's DELETED event is offered
			// and the watcher takes the ones it is waiting for.
			injectPodsDeleted(clientset, hash, sortedMapKeys(pods)...)

			if err := m.UndeployLab(context.Background(), kathara.LabRef{Hash: hash}, test.opts); err != nil {
				t.Fatalf("UndeployLab: %v", err)
			}

			if got := deletedNetworks(dynamicClient); strings.Join(got, ",") != strings.Join(test.wantNetworks, ",") {
				t.Errorf("deleted networks %v, want %v", got, test.wantNetworks)
			}
			if got := namespaceDeleted(clientset); got != test.wantNamespace {
				t.Errorf("namespace deleted = %v, want %v", got, test.wantNamespace)
			}
		})
	}
}

// TestUndeployLabBothFilters is the manager's own TRUTHINESS pre-check, which
// is a different test from the machine layer's `is not None` one.
func TestUndeployLabBothFilters(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	err := m.UndeployLab(context.Background(), kathara.LabRef{Hash: defaultScenarioHash}, kathara.UndeployLabOptions{
		SelectedMachines: kathara.NewNameSet("pc1"),
		ExcludedMachines: kathara.NewNameSet("pc2"),
	})
	if !errors.Is(err, kerrors.ErrSelectOrExcludeDevices) {
		t.Fatalf("error = %v, want SelectOrExcludeDevices", err)
	}
}

// TestWipeDeletesNamespacesOnly is EXPECTATIONS-k8s §4 "wipe": Megalos drops
// the namespaces and lets the API server cascade — it walks neither pods nor
// networks (k8s-backend.md G26).
func TestWipeDeletesNamespacesOnly(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "k1", Labels: map[string]string{labelApp: labelAppValue}}},
		newTestPod("k1", "pc1"),
	)

	watcher := watchWithDeletes("k1")
	clientset.PrependWatchReactor("namespaces", k8stesting.DefaultWatchReactor(watcher, nil))

	if err := m.Wipe(context.Background(), false); err != nil {
		t.Fatalf("Wipe: %v", err)
	}

	for _, action := range clientset.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource != "namespaces" {
			t.Errorf("wipe deleted a %s", action.GetResource().Resource)
		}
	}
	if !namespaceDeleted(clientset) {
		t.Error("no namespace was deleted")
	}
}

// TestGetAPIObjects is EXPECTATIONS-k8s §4 "get_machine_api_object" and
// "get_link_api_object": the last match, and the not-found messages, which use
// the UNQUOTED spellings unique to this backend.
func TestGetAPIObjects(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, _ := newTestManager(t, s, newTestPod(hash, "pc1"))
	m.link.dynamic = newFakeDynamic(newTestNetwork(hash, "A", 1))

	ctx := context.Background()
	ref := kathara.LabRef{Hash: hash}

	t.Run("machine found", func(t *testing.T) {
		obj, err := m.GetMachineAPIObject(ctx, "pc1", ref, false)
		if err != nil {
			t.Fatalf("GetMachineAPIObject: %v", err)
		}
		if pod, ok := obj.(*corev1.Pod); !ok || pod.Labels[labelName] != "pc1" {
			t.Errorf("object = %#v", obj)
		}
	})

	t.Run("machine not found", func(t *testing.T) {
		_, err := m.GetMachineAPIObject(ctx, "pc9", ref, false)
		if !errors.Is(err, kerrors.ErrMachineNotFound) {
			t.Fatalf("error = %v, want MachineNotFound", err)
		}
		if !strings.Contains(err.Error(), "Device pc9 not found.") {
			t.Errorf("message = %q, want the unquoted spelling", err.Error())
		}
	})

	t.Run("link found", func(t *testing.T) {
		obj, err := m.GetLinkAPIObject(ctx, "A", ref, false)
		if err != nil {
			t.Fatalf("GetLinkAPIObject: %v", err)
		}
		if network, ok := obj.(*Network); !ok || NetworkNameOf(network) != "netprefix-a" {
			t.Errorf("object = %#v", obj)
		}
	})

	t.Run("link not found", func(t *testing.T) {
		_, err := m.GetLinkAPIObject(ctx, "Z", ref, false)
		if !errors.Is(err, kerrors.ErrLinkNotFound) {
			t.Fatalf("error = %v, want LinkNotFound", err)
		}
		if !strings.Contains(err.Error(), "Collision Domain Z not found.") {
			t.Errorf("message = %q, want the capital-D unquoted spelling", err.Error())
		}
	})

	t.Run("plural getters accept no identity", func(t *testing.T) {
		if _, err := m.GetMachinesAPIObjects(ctx, kathara.LabRef{}, true); err != nil {
			t.Errorf("GetMachinesAPIObjects: %v", err)
		}
		if _, err := m.GetLinksAPIObjects(ctx, kathara.LabRef{}, true); err != nil {
			t.Errorf("GetLinksAPIObjects: %v", err)
		}
	})
}

// TestGetLabFromAPI is EXPECTATIONS-k8s §4 "get_lab_from_api": the whole
// reconstruction, including the two Python details that are preserved because
// they are observable — the CPU meta is written under `cpu` (which nothing
// reads) as a FLOAT, and interface numbers come from the annotation's array
// position.
func TestGetLabFromAPI(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)

	pod := &corev1.Pod{
		ObjectMeta: podMeta(hash, "pc1",
			podNetworkAttachment{Name: "netprefix-a", Namespace: hash, Interface: "net0", KatharaLink: "A"},
			podNetworkAttachment{Name: "netprefix-b", Namespace: hash, Interface: "net1", KatharaLink: "B", MAC: "00:11:22:33:44:55"},
		),
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "devprefix-pc1",
			Image: "kathara/test",
			Env: []corev1.EnvVar{
				{Name: megalosShellEnv, Value: "/bin/zsh"},
				{Name: "MY_VAR", Value: "value"},
			},
			Ports: []corev1.ContainerPort{{HostPort: 3001, ContainerPort: 56, Protocol: corev1.ProtocolUDP}},
			Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
				corev1.ResourceMemory: mustQuantity(t, "64M"),
				// 1500m, not 1000m: a Quantity canonicalises `1000m` to `1`,
				// and `int("1") / 1000` is 0.001 — which is exactly what Python
				// computes when it reads the same limit back off a real API
				// server, since the server canonicalises too (k8s-backend.md
				// G22). A value that keeps its `m` suffix is what exercises the
				// intended path.
				corev1.ResourceCPU: mustQuantity(t, "1500m"),
			}},
		}}},
	}

	m, _, _, _ := newTestManager(t, s, pod)
	m.link.dynamic = newFakeDynamic(newTestNetwork(hash, "A", 1), newTestNetwork(hash, "B", 2))

	lab, err := m.GetLabFromAPI(context.Background(), hash, "")
	if err != nil {
		t.Fatalf("GetLabFromAPI: %v", err)
	}

	if lab.Name() != "reconstructed_lab" {
		t.Errorf("name = %q, want reconstructed_lab", lab.Name())
	}
	if lab.Hash != hash {
		t.Errorf("hash = %q, want %q", lab.Hash, hash)
	}

	device, err := lab.GetMachine("pc1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	if got := device.GetImage(); got != "kathara/test" {
		t.Errorf("image = %q", got)
	}
	if got := device.GetShell(); got != "/bin/zsh" {
		t.Errorf("shell = %q", got)
	}
	if got, err := device.GetMem(); err != nil || got != "64m" {
		t.Errorf("mem = %q, %v", got, err)
	}
	// `add_meta("cpu", …)` — the key `get_cpu` does NOT read, holding a float.
	if value, ok := device.Meta.Extras.Get("cpu"); !ok || value.Kind() != model.KindFloat {
		t.Errorf("cpu meta = %v (present=%v), want a float", value.Value(), ok)
	} else if value.Value().(float64) != 1.5 {
		t.Errorf("cpu = %v, want 1.5", value.Value())
	}
	if value, ok := device.Envs().Get("MY_VAR"); !ok || value != "value" {
		t.Errorf("envs = %v", device.Envs().Entries())
	}
	if device.Envs().Has(megalosShellEnv) {
		t.Error("_MEGALOS_SHELL leaked into the envs meta")
	}
	if value, ok := device.Ports().Get(model.PortKey{HostPort: 3001, Protocol: "udp"}); !ok || value != 56 {
		t.Errorf("ports = %v", device.Ports().Entries())
	}
	if device.Sysctls().Len() != 0 {
		t.Errorf("sysctls = %v, want empty (not reconstructable)", device.Sysctls().Entries())
	}

	interfaces := device.Interfaces()
	if len(interfaces) != 2 {
		t.Fatalf("%d interfaces, want 2", len(interfaces))
	}
	if interfaces[0].Link.Name != "A" || interfaces[0].Number != 0 {
		t.Errorf("iface 0 = %v", interfaces[0])
	}
	if interfaces[1].Link.Name != "B" || interfaces[1].Number != 1 || interfaces[1].MAC != "00:11:22:33:44:55" {
		t.Errorf("iface 1 = %v", interfaces[1])
	}
}

// TestGetLabFromAPICPUQuirk is k8s-backend.md G22 reproduced: a CPU limit
// WITHOUT the `m` suffix — which is what the API server stores for any whole
// number of cores — divides by 1000 all the same, so a 2-core device comes back
// with `cpu = 0.002`. Python does exactly this, and the meta it lands in
// (`cpu`) is one `get_cpu` never reads.
func TestGetLabFromAPICPUQuirk(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)

	pod := &corev1.Pod{
		ObjectMeta: podMeta(hash, "pc1"),
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Image: "kathara/test",
			Resources: corev1.ResourceRequirements{Limits: corev1.ResourceList{
				corev1.ResourceCPU: mustQuantity(t, "2"),
			}},
		}}},
	}

	m, _, _, _ := newTestManager(t, s, pod)
	lab, err := m.GetLabFromAPI(context.Background(), hash, "")
	if err != nil {
		t.Fatalf("GetLabFromAPI: %v", err)
	}
	device, err := lab.GetMachine("pc1")
	if err != nil {
		t.Fatalf("GetMachine: %v", err)
	}
	value, ok := device.Meta.Extras.Get("cpu")
	if !ok || value.Value().(float64) != 0.002 {
		t.Errorf("cpu = %v (present=%v), want 0.002", value.Value(), ok)
	}
}

// TestGetLabFromAPIByName pins the other identity branch: with a name, the
// scenario carries it and the hash is derived.
func TestGetLabFromAPIByName(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	lab, err := m.GetLabFromAPI(context.Background(), "", "default_scenario")
	if err != nil {
		t.Fatalf("GetLabFromAPI: %v", err)
	}
	if lab.Name() != "default_scenario" || lab.Hash != defaultScenarioHash {
		t.Errorf("lab = %q/%q", lab.Name(), lab.Hash)
	}
}

// TestGetLabFromAPINoIdentity is the truthiness guard unique to this method:
// `if not lab_hash and not lab_name`, which is why an empty string genuinely is
// absent here (NILABILITY.tsv:65).
func TestGetLabFromAPINoIdentity(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	if _, err := m.GetLabFromAPI(context.Background(), "", ""); !errors.Is(err, kerrors.ErrInvocation) {
		t.Fatalf("error = %v, want an InvocationError", err)
	}
}

// TestStatsEagerness is EXPECTATIONS-k8s §4 "get_machines_stats" /
// "get_machine_stats": the plural getter validates NOW and the singular one
// defers to the first `next()`, because the Python bodies differ in whether
// they hold a `yield`.
func TestStatsEagerness(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)
	ctx := context.Background()

	t.Run("plural accepts no identity", func(t *testing.T) {
		if _, err := m.GetMachinesStats(ctx, kathara.LabRef{}, "", true); err != nil {
			t.Errorf("GetMachinesStats: %v", err)
		}
		if _, err := m.GetLinksStats(ctx, kathara.LabRef{}, "", true); err != nil {
			t.Errorf("GetLinksStats: %v", err)
		}
	})

	t.Run("plural rejects two identities at the call", func(t *testing.T) {
		ref := kathara.LabRef{Hash: "h", Name: "n"}
		if _, err := m.GetMachinesStats(ctx, ref, "", false); !errors.Is(err, kerrors.ErrInvocation) {
			t.Errorf("error = %v, want an InvocationError", err)
		}
	})

	t.Run("singular defers to the first Next", func(t *testing.T) {
		stream := m.GetMachineStats(ctx, "pc1", kathara.LabRef{}, false)
		if stream == nil {
			t.Fatal("stream = nil")
		}
		if _, err := stream.Next(ctx); !errors.Is(err, kerrors.ErrInvocation) {
			t.Fatalf("error = %v, want an InvocationError at the first Next", err)
		}
	})

	t.Run("singular link stats defer too", func(t *testing.T) {
		stream := m.GetLinkStats(ctx, "A", kathara.LabRef{}, false)
		if _, err := stream.Next(ctx); !errors.Is(err, kerrors.ErrInvocation) {
			t.Fatalf("error = %v, want an InvocationError at the first Next", err)
		}
	})

	t.Run("the object getters check the scenario eagerly", func(t *testing.T) {
		if _, err := m.GetMachineStatsObj(ctx, &model.Machine{Name: "pc1"}, false); !errors.Is(err, kerrors.ErrLabNotFound) {
			t.Errorf("error = %v, want LabNotFound", err)
		}
		if _, err := m.GetLinkStatsObj(ctx, &model.Link{Name: "A"}, false); !errors.Is(err, kerrors.ErrLabNotFound) {
			t.Errorf("error = %v, want LabNotFound", err)
		}
	})
}

// TestCheckImageIsANoOp pins `check_image`: the cluster resolves the image, so
// this validates nothing and never fails.
func TestCheckImageIsANoOp(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)

	if err := m.CheckImage(context.Background(), "nonexistent/image:9.9"); err != nil {
		t.Fatalf("CheckImage: %v", err)
	}
	if len(clientset.Actions()) != 0 {
		t.Errorf("check_image made %d API calls, want 0", len(clientset.Actions()))
	}
}

func mustQuantity(t *testing.T, value string) resource.Quantity {
	t.Helper()
	q, err := resource.ParseQuantity(value)
	if err != nil {
		t.Fatalf("ParseQuantity(%q): %v", value, err)
	}
	return q
}

func watchWithDeletes(names ...string) *watch.FakeWatcher {
	watcher := watch.NewFakeWithChanSize(len(names), false)
	for _, name := range names {
		watcher.Delete(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}})
	}
	return watcher
}
