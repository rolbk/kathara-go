package kubernetes

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// TestNetworkID is EXPECTATIONS-k8s §2 "_get_network_id":
// `(offset + int(sha256(seed + name).hex, 16)) % (2^24 - 10)`, with the offset
// added BEFORE the modulo.
func TestNetworkID(t *testing.T) {
	tests := []struct {
		name   string
		seed   string
		object string
		offset int
		want   int
	}{
		{name: "base", seed: "user123", object: "A", offset: 0, want: 1362434},
		{name: "offset 1", seed: "user123", object: "A", offset: 1, want: 1362435},
		{name: "offset 2", seed: "user123", object: "A", offset: 2, want: 1362436},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := NetworkID(test.seed, test.object, test.offset); got != test.want {
				t.Errorf("NetworkID(%q, %q, %d) = %d, want %d", test.seed, test.object, test.offset, got, test.want)
			}
		})
	}
}

// TestNetworkIDSeedIsLoadBearing pins that the seed really participates: two
// users of one cluster get different VNIs for the same collision domain, which
// is the whole point of `get_cluster_user`.
func TestNetworkIDSeedIsLoadBearing(t *testing.T) {
	if NetworkID("user123", "A", 0) == NetworkID("user456", "A", 0) {
		t.Error("two seeds produced the same VNI")
	}
	// No separator: the concatenation is what is hashed.
	if NetworkID("ab", "c", 0) != NetworkID("a", "bc", 0) {
		t.Error("seed and name are expected to be concatenated without a separator")
	}
}

// TestVNIAllocator is EXPECTATIONS-k8s §2 "_get_unique_network_id": the base id
// when free, and a linear probe by offset when not — with the chosen id
// reserved either way.
func TestVNIAllocator(t *testing.T) {
	tests := []struct {
		name     string
		existing []int
		want     int
	}{
		{name: "empty reservation map", existing: nil, want: 1362434},
		{name: "base id taken", existing: []int{1362434}, want: 1362435},
		{name: "two consecutive ids taken", existing: []int{1362434, 1362435}, want: 1362436},
		{name: "an unrelated id changes nothing", existing: []int{5432}, want: 1362434},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			allocator := newVNIAllocator("user123", test.existing)
			if got := allocator.Allocate("A"); got != test.want {
				t.Fatalf("Allocate = %d, want %d", got, test.want)
			}
			if !allocator.Reserved(test.want) {
				t.Errorf("id %d was not reserved", test.want)
			}
			for _, id := range test.existing {
				if !allocator.Reserved(id) {
					t.Errorf("seeded id %d was dropped", id)
				}
			}
		})
	}
}

// TestVNIAllocatorIsRaceFree pins CONCURRENCY.tsv row
// `KubernetesLink.py:319`: the check and the reservation are ONE critical
// section, so two workers probing to the same free id cannot both take it —
// the latent duplicate-VNI race the register sanctions fixing.
func TestVNIAllocatorIsRaceFree(t *testing.T) {
	const workers = 32
	allocator := newVNIAllocator("user123", nil)

	results := make(chan int, workers)
	start := make(chan struct{})
	for range workers {
		go func() {
			<-start
			results <- allocator.Allocate("A")
		}()
	}
	close(start)

	seen := make(map[int]struct{}, workers)
	for range workers {
		id := <-results
		if _, duplicate := seen[id]; duplicate {
			t.Fatalf("VNI %d was handed out twice", id)
		}
		seen[id] = struct{}{}
	}
}

// TestDeployLinksFilters is EXPECTATIONS-k8s §2 "deploy_links": which collision
// domains are created for each filter shape, and that an empty scenario does
// nothing at all — not even the cluster-wide VNI listing.
func TestDeployLinksFilters(t *testing.T) {
	tests := []struct {
		name     string
		links    []string
		selected kathara.NameSet
		excluded kathara.NameSet
		want     []string
		wantErr  error
	}{
		{name: "no filter deploys every link", links: []string{"A", "B", "C"}, want: []string{"netprefix-a", "netprefix-b", "netprefix-c"}},
		{name: "selected", links: []string{"A", "B", "C"}, selected: kathara.NewNameSet("A"), want: []string{"netprefix-a"}},
		{name: "excluded", links: []string{"A", "B", "C"}, excluded: kathara.NewNameSet("A"), want: []string{"netprefix-b", "netprefix-c"}},
		{
			name:     "both filters",
			links:    []string{"A"},
			selected: kathara.NewNameSet("A"),
			excluded: kathara.NewNameSet("B"),
			wantErr:  kerrors.ErrSelectedOrExcludedLinks,
		},
		{name: "empty scenario does nothing", links: nil, want: nil},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			m, _, dynamicClient, _ := newTestManager(t, s)
			lab := newTestLab(t, s)
			for _, name := range test.links {
				if _, err := lab.NewLink(name); err != nil {
					t.Fatalf("NewLink: %v", err)
				}
			}

			err := m.link.DeployLinks(context.Background(), lab, test.selected, test.excluded)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				if len(createdNetworks(dynamicClient)) != 0 {
					t.Fatal("networks were created despite the refusal")
				}
				return
			}
			if err != nil {
				t.Fatalf("DeployLinks: %v", err)
			}
			if got := createdNetworks(dynamicClient); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("created %v, want %v", got, test.want)
			}
		})
	}
}

// createdNetworks is the name of every NAD the fake dynamic client was asked to
// create, sorted — the fan-out order is scheduler-dependent.
func createdNetworks(client actionRecorder) []string {
	var names []string
	for _, action := range client.Actions() {
		create, ok := action.(k8stesting.CreateAction)
		if !ok || action.GetResource() != netGVR {
			continue
		}
		if object, ok := create.GetObject().(*unstructured.Unstructured); ok {
			names = append(names, object.GetName())
		}
	}
	slices.Sort(names)
	return names
}

// TestDeployLinksSeedsFromCluster is EXPECTATIONS-k8s
// `test_deploy_links_with_loaded_ids_and_collision`: the reservation map starts
// from the VNIs already deployed CLUSTER-WIDE, so a collision domain whose base
// id is taken by ANOTHER scenario is created at base+1.
func TestDeployLinksSeedsFromCluster(t *testing.T) {
	s := testSettings()

	// An existing NAD in a different namespace, carrying the exact VNI that
	// `user123`+`A` hashes to.
	existing := newTestNetwork("othernamespace", "Z", 1362434)
	m, _, dynamicClient, _ := newTestManager(t, s)
	if _, err := dynamicClient.Resource(netGVR).Namespace("othernamespace").
		Create(context.Background(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed network: %v", err)
	}

	// The unfiltered listing enumerates namespaces, so both have to exist.
	clientset := m.clientset
	for _, name := range []string{"othernamespace", defaultScenarioHash} {
		if _, err := clientset.CoreV1().Namespaces().Create(context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
				Name:   name,
				Labels: map[string]string{labelApp: labelAppValue},
			}}, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed namespace: %v", err)
		}
	}

	lab := newTestLab(t, s)
	if _, err := lab.NewLink("A"); err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	if err := m.link.DeployLinks(context.Background(), lab, nil, nil); err != nil {
		t.Fatalf("DeployLinks: %v", err)
	}

	link, err := lab.GetLink("A")
	if err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	network, ok := link.APIObject.(*Network)
	if !ok {
		t.Fatalf("api object = %T, want *Network", link.APIObject)
	}
	id, ok := NetworkVXLANID(network)
	if !ok || id != 1362435 {
		t.Errorf("vxlan id = %d (ok=%v), want 1362435 (base+1)", id, ok)
	}
}

// vniCollisionPair is two collision-domain names whose base VNIs are EQUAL
// under the `user123` seed: `sha256(seed+name)` mod `MAX_K8S_LINK_NUMBER` is
// 1392701 for both, and 1392702 for both at offset 1. Verified against the
// oracle; it is the only shape in which the allocator's probe loop is reachable
// from two names at once, and therefore the only shape in which the assignment
// could depend on who gets there first.
var vniCollisionPair = [2]string{"cd57", "cd6099"}

const (
	vniCollisionBase   = 1392701
	vniCollisionOffset = 1392702
)

// TestDeployLinksReservesIDsInScenarioOrder is ORDERING.tsv row
// `KubernetesLink.py:77`: "reserve IDs sequentially in link order BEFORE
// parallel create; then create in parallel".
//
// Python allocates inside the pool worker, so for two names that probe to the
// same id WHICH one keeps the base and which takes the offset is thread-arrival
// nondeterminism. Here it is the scenario's order, and nothing else: the first
// collision domain of the scenario gets the base id on every run, in both
// orders, every time.
func TestDeployLinksReservesIDsInScenarioOrder(t *testing.T) {
	orders := [][2]string{
		{vniCollisionPair[0], vniCollisionPair[1]},
		{vniCollisionPair[1], vniCollisionPair[0]},
	}

	for _, order := range orders {
		t.Run(order[0]+"_before_"+order[1], func(t *testing.T) {
			// Repeated because the failure mode being excluded is a race: with
			// the allocation back inside the worker this flips on some runs.
			for range 32 {
				s := testSettings()
				m, _, _, _ := newTestManager(t, s)
				lab := newTestLab(t, s)
				for _, name := range order {
					if _, err := lab.NewLink(name); err != nil {
						t.Fatalf("NewLink(%q): %v", name, err)
					}
				}

				if err := m.link.DeployLinks(context.Background(), lab, nil, nil); err != nil {
					t.Fatalf("DeployLinks: %v", err)
				}

				if got := deployedVNI(t, lab, order[0]); got != vniCollisionBase {
					t.Fatalf("%s got VNI %d, want the base %d", order[0], got, vniCollisionBase)
				}
				if got := deployedVNI(t, lab, order[1]); got != vniCollisionOffset {
					t.Fatalf("%s got VNI %d, want the offset %d", order[1], got, vniCollisionOffset)
				}
			}
		})
	}
}

// deployedVNI reads the `vxlanId` out of the NAD `create` left in
// `link.api_object`.
func deployedVNI(t *testing.T, lab *model.Lab, linkName string) int {
	t.Helper()

	link, err := lab.GetLink(linkName)
	if err != nil {
		t.Fatalf("GetLink(%q): %v", linkName, err)
	}
	network, ok := link.APIObject.(*Network)
	if !ok {
		t.Fatalf("api object of %q = %T, want *Network", linkName, link.APIObject)
	}
	id, ok := NetworkVXLANID(network)
	if !ok {
		t.Fatalf("%q carries no VNI", linkName)
	}
	return id
}

// TestCreateLinkAdoptsExisting is `KubernetesLink.create`'s first branch: a
// collision domain that is already deployed is adopted, the freshly computed
// VNI is discarded, and nothing is submitted.
func TestCreateLinkAdoptsExisting(t *testing.T) {
	s := testSettings()
	m, _, dynamicClient, _ := newTestManager(t, s)

	existing := newTestNetwork(defaultScenarioHash, "A", 999)
	if _, err := dynamicClient.Resource(netGVR).Namespace(defaultScenarioHash).
		Create(context.Background(), existing, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed network: %v", err)
	}

	lab := newTestLab(t, s)
	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	before := len(createdNetworks(dynamicClient))
	if err := m.link.Create(context.Background(), link, 1362434); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := len(createdNetworks(dynamicClient)); got != before {
		t.Errorf("%d networks created, want %d — the existing one should be adopted", got, before)
	}
	if network, ok := link.APIObject.(*Network); !ok {
		t.Fatalf("api object = %T, want *Network", link.APIObject)
	} else if id, _ := NetworkVXLANID(network); id != 999 {
		t.Errorf("adopted vxlan id = %d, want 999", id)
	}
}

// TestUndeployLinks is EXPECTATIONS-k8s §2 "undeploy / wipe", including that
// `selected_links` matches the KUBERNETES network name and not the
// collision-domain label.
func TestUndeployLinks(t *testing.T) {
	tests := []struct {
		name     string
		networks []string
		selected kathara.NameSet
		want     []string
	}{
		{name: "no filter deletes every network", networks: []string{"A", "B", "C"}, want: []string{"netprefix-a", "netprefix-b", "netprefix-c"}},
		{name: "empty scenario deletes nothing", networks: nil, want: nil},
		{
			name:     "selection is by Kubernetes name",
			networks: []string{"A", "B"},
			selected: kathara.NewNameSet("netprefix-b"),
			want:     []string{"netprefix-b"},
		},
		{
			// The collision-domain label is NOT what is matched — the Python
			// test `test_undeploy_selected_links` pins this.
			name:     "the collision-domain name matches nothing",
			networks: []string{"A", "B"},
			selected: kathara.NewNameSet("B"),
			want:     nil,
		},
		{
			// `is not None`: a non-nil empty set deletes nothing.
			name:     "empty selection deletes nothing",
			networks: []string{"A"},
			selected: kathara.NewNameSet(),
			want:     nil,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			s := testSettings()
			objects := make([]*Network, 0, len(test.networks))
			for _, name := range test.networks {
				objects = append(objects, newTestNetwork(defaultScenarioHash, name, 1))
			}

			m, _, _, _ := newTestManager(t, s)
			dynamicClient := newFakeDynamic(objects...)
			m.link.dynamic = dynamicClient
			m.dynamic = dynamicClient

			if err := m.link.Undeploy(context.Background(), defaultScenarioHash, test.selected); err != nil {
				t.Fatalf("Undeploy: %v", err)
			}
			if got := deletedNetworks(dynamicClient); strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("deleted %v, want %v", got, test.want)
			}
		})
	}
}

func deletedNetworks(client actionRecorder) []string {
	var names []string
	for _, action := range client.Actions() {
		del, ok := action.(k8stesting.DeleteAction)
		if !ok || action.GetResource() != netGVR {
			continue
		}
		names = append(names, del.GetName())
	}
	slices.Sort(names)
	return names
}

// TestUndeployLinkSwallowsAPIErrors pins `_undeploy_link`'s
// `except ApiException: pass` AND that the `link_undeployed` event fires anyway
// — which is what keeps the progress bar from hanging on a network that has
// already gone.
func TestUndeployLinkSwallowsAPIErrors(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	dynamicClient := newFakeDynamic(newTestNetwork(defaultScenarioHash, "A", 1))
	dynamicClient.PrependReactor("delete", netPlural, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: netGroup, Resource: netPlural}, "netprefix-a")
	})
	m.link.dynamic = dynamicClient

	undeployed := 0
	event.Subscribe(m.dispatcher, func(event.LinkUndeployed) error { undeployed++; return nil })

	if err := m.link.Undeploy(context.Background(), defaultScenarioHash, nil); err != nil {
		t.Fatalf("Undeploy: %v", err)
	}
	if undeployed != 1 {
		t.Errorf("link_undeployed fired %d times, want 1", undeployed)
	}
}

// TestGetLinksByFilters is EXPECTATIONS-k8s §2
// "get_links_api_objects_by_filters": the same selector and namespace
// enumeration as the machine side.
func TestGetLinksByFilters(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1", Labels: map[string]string{labelApp: labelAppValue}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns2", Labels: map[string]string{labelApp: labelAppValue}}},
	)
	_ = clientset
	m.link.dynamic = newFakeDynamic(
		newTestNetwork("ns1", "A", 1),
		newTestNetwork("ns2", "B", 2),
	)

	t.Run("no filter enumerates namespaces", func(t *testing.T) {
		networks, err := m.link.getByFilters(context.Background(), "", "")
		if err != nil {
			t.Fatalf("getByFilters: %v", err)
		}
		if len(networks) != 2 {
			t.Errorf("%d networks, want 2", len(networks))
		}
	})

	t.Run("lab hash skips enumeration", func(t *testing.T) {
		networks, err := m.link.getByFilters(context.Background(), "ns1", "")
		if err != nil {
			t.Fatalf("getByFilters: %v", err)
		}
		if len(networks) != 1 || NetworkNameOf(networks[0]) != "netprefix-a" {
			t.Errorf("networks = %v, want just netprefix-a", networks)
		}
	})

	t.Run("link name filters by label", func(t *testing.T) {
		networks, err := m.link.getByFilters(context.Background(), "ns1", "B")
		if err != nil {
			t.Fatalf("getByFilters: %v", err)
		}
		if len(networks) != 0 {
			t.Errorf("%d networks, want 0 — B is in ns2", len(networks))
		}
	})
}

// TestExistingNetworkIDs is EXPECTATIONS-k8s §2 "_get_existing_network_ids",
// plus the port's own rule that an unparseable NAD is skipped rather than
// failing the whole deploy.
func TestExistingNetworkIDs(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns1", Labels: map[string]string{labelApp: labelAppValue}}},
	)

	broken := newTestNetwork("ns1", "X", 1)
	if err := unstructured.SetNestedField(broken.Object, "not json", "spec", "config"); err != nil {
		t.Fatalf("SetNestedField: %v", err)
	}
	m.link.dynamic = newFakeDynamic(newTestNetwork("ns1", "A", 5432), broken)

	ids, err := m.link.existingNetworkIDs(context.Background())
	if err != nil {
		t.Fatalf("existingNetworkIDs: %v", err)
	}
	slices.Sort(ids)
	if len(ids) != 1 || ids[0] != 5432 {
		t.Errorf("ids = %v, want [5432]", ids)
	}
}

// TestDeployLinksDispatchesEvents pins the three collision-domain events and
// their order around the fan-out.
func TestDeployLinksDispatchesEvents(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	var order []string
	event.Subscribe(m.dispatcher, func(e event.LinksDeployStarted) error {
		order = append(order, "started:"+strings.Join(linkNames(e.Links), ","))
		return nil
	})
	event.Subscribe(m.dispatcher, func(event.LinkDeployed) error { order = append(order, "deployed"); return nil })
	event.Subscribe(m.dispatcher, func(event.LinksDeployEnded) error { order = append(order, "ended"); return nil })

	lab := newTestLab(t, s)
	if _, err := lab.NewLink("A"); err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	if err := m.link.DeployLinks(context.Background(), lab, nil, nil); err != nil {
		t.Fatalf("DeployLinks: %v", err)
	}
	want := "started:A,deployed,ended"
	if got := strings.Join(order, ","); got != want {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func linkNames(links []*model.Link) []string {
	names := make([]string, 0, len(links))
	for _, link := range links {
		names = append(names, link.Name)
	}
	return names
}
