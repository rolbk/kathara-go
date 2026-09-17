// This file is `KubernetesLink.py`: collision domains as Multus
// NetworkAttachmentDefinitions, and the VXLAN VNI allocator that keeps two of
// them off the same wire.

package kubernetes

import (
	"context"
	"crypto/sha256"
	"log/slog"
	"math/big"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// maxLinkNumber is `MAX_K8S_LINK_NUMBER` (`KubernetesLink.py:24`):
// `(1 << 24) - 10`, the VXLAN VNI space minus ten reserved ids.
const maxLinkNumber = (1 << 24) - 10

// linkService is `KubernetesLink` (`KubernetesLink.py:31`).
type linkService struct {
	dynamic    dynamic.Interface
	namespace  *namespaceService
	dispatcher *event.Dispatcher
	netPrefix  string

	// seed is `self.seed` (`KubernetesLink.py:40`):
	// `KubernetesConfig.get_cluster_user()`, prepended to every collision-domain
	// name before hashing. See [ClusterConfig.User].
	seed string
}

// ---------------------------------------------------------------------------
// Deploy
// ---------------------------------------------------------------------------

// DeployLinks is `deploy_links` (`KubernetesLink.py:42`).
//
// Order, all of it observable:
//
//  1. the both-filters guard (TRUTHINESS here, so two empty sets pass);
//  2. filter the scenario's collision domains, preserving scenario order;
//  3. nothing at all when the result is empty — no VNI listing, no events
//     (EXPECTATIONS-k8s `test_deploy_links_no_link`);
//  4. `links_deploy_started`, seed the reservation map from the VNIs already
//     deployed CLUSTER-WIDE, reserve one VNI per collision domain, fan out,
//     `links_deploy_ended`.
//
// Step 4's seeding is what makes the allocator avoid a collision with another
// user's scenario and not merely within this one: `_get_existing_network_ids`
// lists NADs across every Kathará namespace (`KubernetesLink.py:348`).
//
// # Why the reservation is not inside the worker
//
// Python calls `_get_unique_network_id` from `_deploy_link`, i.e. from the pool
// thread (`KubernetesLink.py:99`), so when two names probe to the same id —
// a sha256 collision modulo `MAX_K8S_LINK_NUMBER`, or a collision with a VNI
// the cluster already had — WHICH collision domain keeps the base id and which
// takes the offset depends on thread arrival. ORDERING.tsv row
// `KubernetesLink.py:77` marks that non-deterministic and rules the port
// "reserve IDs sequentially in link order BEFORE parallel create; then create
// in parallel", which is what the loop below does: the ids are a pure function
// of the scenario order and the cluster's existing VNIs, and only the creates
// race. CONCURRENCY.tsv row `KubernetesLink.py:319` (the check-and-reserve
// critical section, DIVERGENCES.md 75) is a different rule and still holds —
// [vniAllocator] keeps its mutex, because [linkService.Create] is not the only
// caller.
//
// Errors: [kerrors.ErrSelectedOrExcludedLinks], then whatever the listing or
// any worker answers.
func (s *linkService) DeployLinks(ctx context.Context, lab *model.Lab, selected, excluded kathara.NameSet) error {
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectedOrExcludedLinks
	}

	links := filterLinks(lab.Links(), selected, excluded)
	if len(links) == 0 {
		return nil
	}

	if err := event.Dispatch(s.dispatcher, event.LinksDeployStarted{Links: links}); err != nil {
		return err
	}

	existing, err := s.existingNetworkIDs(ctx)
	if err != nil {
		return err
	}
	allocator := newVNIAllocator(s.seed, existing)

	networkIDs := make(map[*model.Link]int, len(links))
	for _, link := range links {
		networkIDs[link] = allocator.Allocate(link.Name)
	}

	if err := runChunked(ctx, links, linkItemName, func(ctx context.Context, link *model.Link) error {
		return s.deployLink(ctx, networkIDs[link], link)
	}); err != nil {
		return err
	}

	return event.Dispatch(s.dispatcher, event.LinksDeployEnded{})
}

// filterLinks is the dict comprehension of `deploy_links`
// (`KubernetesLink.py:59-67`).
//
// Both filters are TRUTHINESS-tested, so an empty set is "no filter" — the
// exact inverse of [linkService.Undeploy], where the test is `is not None`
// (SYNTHESIS §1.7, NILABILITY.tsv:55-57). The comprehension preserves scenario
// order, which is submission order, which is chunk order.
func filterLinks(links []*model.Link, selected, excluded kathara.NameSet) []*model.Link {
	switch {
	case len(selected) > 0:
		out := make([]*model.Link, 0, len(links))
		for _, link := range links {
			if selected.Has(link.Name) {
				out = append(out, link)
			}
		}
		return out
	case len(excluded) > 0:
		out := make([]*model.Link, 0, len(links))
		for _, link := range links {
			if !excluded.Has(link.Name) {
				out = append(out, link)
			}
		}
		return out
	}
	return links
}

// deployLink is `_deploy_link` (`KubernetesLink.py:87`): create and announce —
// the unit of work the pool runs, which dispatches its own completion event
// from the worker goroutine (CONCURRENCY.tsv row 22).
//
// The VNI is passed in rather than allocated here, which is the ORDERING.tsv
// ruling [linkService.DeployLinks] explains.
func (s *linkService) deployLink(ctx context.Context, networkID int, link *model.Link) error {
	if err := s.Create(ctx, link, networkID); err != nil {
		return err
	}
	return event.Dispatch(s.dispatcher, event.LinkDeployed{Link: link})
}

// Create is `create` (`KubernetesLink.py:105`): adopt the existing NAD if there
// is one, otherwise submit a new one.
//
// The adoption is what makes a second `lstart` of the same scenario idempotent,
// and it means the VNI the allocator just computed is DISCARDED for a collision
// domain that already exists — the deployed one keeps whatever VNI it was
// created with. The allocation still happened, so the number is still reserved
// against the rest of this run.
//
// `pop()` takes the LAST match; the selector is `app=kathara,name=<link>`
// inside one namespace, so there is at most one.
//
// The `external` warning fires AFTER the create, not before, so a collision
// domain with host interfaces is deployed and then reported as unsupported
// (`KubernetesLink.py:128-130`). `lab.ext` is deferred in 1.0 (PORT_SPEC §0.3)
// so [model.Link.External] is always empty and the branch is unreachable; it is
// here because the branch is part of the create's observable order.
func (s *linkService) Create(ctx context.Context, link *model.Link, networkID int) error {
	networks, err := s.getByFilters(ctx, link.Lab.Hash, link.Name)
	if err != nil {
		return err
	}
	if len(networks) > 0 {
		link.APIObject = networks[len(networks)-1]
		return nil
	}

	created, err := s.dynamic.Resource(netGVR).Namespace(link.Lab.Hash).Create(
		ctx,
		NetworkDefinition(NetworkName(s.netPrefix, link.Name), link.Name, link.Lab.Hash, networkID),
		metav1.CreateOptions{},
	)
	if err != nil {
		// Uncaught in Python: it travels out of the pool and out of
		// `deploy_links`, where `deploy_lab`'s `except ApiException` may still
		// turn a 403 into [kerrors.ErrLabTerminating] — `translateForbidden`
		// reads through this wrap and does.
		return translateAPI(err)
	}
	link.APIObject = created

	if len(link.External) > 0 {
		slog.Warn("External is not supported on Megalos. It will be ignored.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Undeploy
// ---------------------------------------------------------------------------

// Undeploy is `undeploy` (`KubernetesLink.py:132`).
//
// selected holds KUBERNETES network names — `metadata.name`, the mangled form —
// and not collision-domain names: the manager computes the set from
// `network["metadata"]["name"]` (`KubernetesManager.py:297,329`) and the Python
// test `test_undeploy_selected_links` pins the distinction.
//
// The filter is `is not None`, so a non-nil EMPTY set deletes nothing while nil
// deletes everything the scenario has. `lclean` passes nil and means "all".
func (s *linkService) Undeploy(ctx context.Context, labHash string, selected kathara.NameSet) error {
	networks, err := s.getByFilters(ctx, labHash, "")
	if err != nil {
		return err
	}
	if selected != nil {
		kept := make([]*Network, 0, len(networks))
		for _, network := range networks {
			if selected.Has(NetworkNameOf(network)) {
				kept = append(kept, network)
			}
		}
		networks = kept
	}

	if len(networks) == 0 {
		return nil
	}

	objects := make([]event.APIObject, 0, len(networks))
	for _, network := range networks {
		objects = append(objects, network)
	}
	if err := event.Dispatch(s.dispatcher, event.LinksUndeployStarted{Networks: objects}); err != nil {
		return err
	}

	if err := runChunked(ctx, networks, networkItemName, s.undeployLink); err != nil {
		return err
	}

	return event.Dispatch(s.dispatcher, event.LinksUndeployEnded{})
}

// Wipe is `wipe` (`KubernetesLink.py:158`): every Kathará NAD in the cluster.
//
// Nothing reaches it: `KubernetesManager.wipe` deletes namespaces and lets the
// API server cascade (k8s-backend.md G26). It is ported because it is public
// surface, and it dispatches no events — unlike [linkService.Undeploy] — which
// is Python's asymmetry.
func (s *linkService) Wipe(ctx context.Context) error {
	networks, err := s.getByFilters(ctx, "", "")
	if err != nil {
		return err
	}
	return runChunked(ctx, networks, networkItemName, s.undeployLink)
}

// undeployLink is `_undeploy_link` (`KubernetesLink.py:173`).
//
// The delete is wrapped in `except ApiException: pass`, so a collision domain
// that has already gone — the usual case when the namespace is being torn down
// underneath — is not an error, and the `link_undeployed` event fires EITHER
// WAY (`KubernetesLink.py:193-196`). The progress bar therefore advances for a
// network that was never deleted, which is what keeps it from hanging.
//
// Python passes `grace_period_seconds=0` twice: once as a `V1DeleteOptions`
// body and once as a query parameter. client-go carries it in the body only,
// which is the same request modulo a redundant parameter the API server
// ignores.
func (s *linkService) undeployLink(ctx context.Context, network *Network) error {
	gracePeriod := int64(0)
	err := s.dynamic.Resource(netGVR).Namespace(NetworkNamespace(network)).Delete(
		ctx,
		NetworkNameOf(network),
		metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod},
	)
	if err != nil && !isAPIException(err) {
		return err
	}

	return event.Dispatch(s.dispatcher, event.LinkUndeployed{Network: network})
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// getByFilters is `get_links_api_objects_by_filters`
// (`KubernetesLink.py:198`).
//
// With no labHash it enumerates every Kathará namespace and lists each one,
// concatenating in namespace order (k8s-backend.md O13); with one it queries
// that namespace directly and skips the enumeration entirely. "" is exactly as
// absent as Python's None (the test is `if not lab_hash`).
func (s *linkService) getByFilters(ctx context.Context, labHash, linkName string) ([]*Network, error) {
	namespaces, err := s.targetNamespaces(ctx, labHash)
	if err != nil {
		return nil, err
	}

	var networks []*Network
	for _, namespace := range namespaces {
		list, err := s.dynamic.Resource(netGVR).Namespace(namespace).List(ctx, listOptions(ObjectSelector(linkName)))
		if err != nil {
			return nil, translateAPI(err)
		}
		for i := range list.Items {
			networks = append(networks, &list.Items[i])
		}
	}
	return networks, nil
}

// targetNamespaces is the
// `list(map(lambda x: x.metadata.name, self.kubernetes_namespace.get_all())) if not lab_hash else [lab_hash]`
// line that both listing functions open with (`KubernetesLink.py:214-215`,
// `KubernetesMachine.py:998-999`).
func (s *linkService) targetNamespaces(ctx context.Context, labHash string) ([]string, error) {
	if labHash != "" {
		return []string{labHash}, nil
	}
	all, err := s.namespace.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for _, namespace := range all {
		names = append(names, namespace.Name)
	}
	return names, nil
}

// existingNetworkIDs is `_get_existing_network_ids`
// (`KubernetesLink.py:340`): the VNI of every Kathará NAD in the cluster, read
// out of each one's `spec.config`.
//
// A NAD whose config will not parse is DROPPED rather than failing the deploy.
// Python's `json.loads` would raise and take the whole `lstart` with it, which
// is a listing failing on one foreign object in a namespace someone else
// labelled `app=kathara` — PORT_SPEC §10's "never crash on a Python-reachable
// path" applies, and the only consequence is that its VNI is not reserved.
// Recorded in DIVERGENCES.md.
func (s *linkService) existingNetworkIDs(ctx context.Context) ([]int, error) {
	networks, err := s.getByFilters(ctx, "", "")
	if err != nil {
		return nil, err
	}

	ids := make([]int, 0, len(networks))
	for _, network := range networks {
		if id, ok := NetworkVXLANID(network); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// ---------------------------------------------------------------------------
// VNI allocation
// ---------------------------------------------------------------------------

// vniAllocator is the `network_ids` dict of `deploy_links`
// (`KubernetesLink.py:77`) plus the `_get_unique_network_id` loop that reads and
// writes it (`:305`).
//
// Python hosts the dict in a `multiprocessing.Manager()` — a real child process
// serving a proxy — which is overkill for a `multiprocessing.dummy` (thread)
// pool and buys nothing the GIL did not already give. CONCURRENCY.tsv row
// `KubernetesLink.py:75` rules the port uses "a plain map[int]struct{} +
// sync.Mutex owned by the deploy call", which is what this is.
//
// # The race that does not survive
//
// `_get_unique_network_id` is a membership loop followed by an assignment
// (`while network_id in network_ids: … ; network_ids[network_id] = 1`). Each
// proxy operation is atomic; the PAIR is not, so two workers probing to the
// same free VNI can both pass the test and both reserve it — a silent duplicate
// that puts two collision domains on one VXLAN wire. CONCURRENCY.tsv row
// `KubernetesLink.py:319` rules the port makes the check and the reservation
// one critical section: "fixes latent bug; safe deviation, note in port docs".
// DIVERGENCES.md carries the note.
type vniAllocator struct {
	seed string

	mu       sync.Mutex
	reserved map[int]struct{}
}

// newVNIAllocator seeds the allocator with the VNIs already deployed. Duplicate
// ids in the input collapse, exactly as they do in Python's dict comprehension.
func newVNIAllocator(seed string, existing []int) *vniAllocator {
	reserved := make(map[int]struct{}, len(existing))
	for _, id := range existing {
		reserved[id] = struct{}{}
	}
	return &vniAllocator{seed: seed, reserved: reserved}
}

// Allocate is `_get_unique_network_id` (`KubernetesLink.py:305`): the name's own
// VNI when it is free, otherwise the first free one found by adding 1, 2, 3, …
// to the hash before the modulo.
//
// The probe is deterministic per name given the same reserved set, and
// [linkService.DeployLinks] calls it in scenario order before any worker starts,
// so WHICH collision domain wins a contested id is decided by the scenario and
// not by thread arrival — the ORDERING.tsv row `KubernetesLink.py:77` ruling,
// which overrides the nondeterminism k8s-backend.md O14 observes in Python. The
// mutex stays: it is CONCURRENCY.tsv row `KubernetesLink.py:319` (DIVERGENCES.md
// 75) and it guards the allocator against any future concurrent caller.
//
// The loop cannot spin forever in practice — it would have to exhaust 16.7
// million ids — and it is not bounded here because Python does not bound it.
func (a *vniAllocator) Allocate(name string) int {
	a.mu.Lock()
	defer a.mu.Unlock()

	networkID := NetworkID(a.seed, name, 0)
	for offset := 1; ; offset++ {
		if _, taken := a.reserved[networkID]; !taken {
			break
		}
		networkID = NetworkID(a.seed, name, offset)
	}

	a.reserved[networkID] = struct{}{}
	return networkID
}

// Reserved reports whether a VNI is taken. It exists for the tests, which is
// where Python's shared dict is inspected directly.
func (a *vniAllocator) Reserved(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, taken := a.reserved[id]
	return taken
}

// NetworkID is `_get_network_id` (`KubernetesLink.py:327`):
//
//	(offset + int(sha256(seed + name).hexdigest(), 16)) % MAX_K8S_LINK_NUMBER
//
// Two details decide the answer:
//
//   - the offset is added BEFORE the modulo, not after, so probing does not
//     simply walk the VNI space by one — it does, in practice, because the
//     modulus is far below the hash, but the arithmetic is what the Python
//     tests pin (offset 1 on the `user123`+`A` case gives 1362435);
//   - the hash is the full 256-bit integer, which no fixed-width Go integer
//     holds. `math/big` is the faithful spelling; folding the digest into a
//     uint64 first would give different ids.
//
// The digest is over `seed + name` as UTF-8 bytes, with no separator — so a
// seed ending in `a` and a name starting with `b` hash the same as a seed
// ending in `ab` and a name starting with nothing. Python has that property
// too.
func NetworkID(seed, name string, offset int) int {
	digest := sha256.Sum256([]byte(seed + name))

	value := new(big.Int).SetBytes(digest[:])
	value.Add(value, big.NewInt(int64(offset)))
	value.Mod(value, big.NewInt(maxLinkNumber))

	return int(value.Int64())
}
