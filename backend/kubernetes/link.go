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

func (s *linkService) deployLink(ctx context.Context, networkID int, link *model.Link) error {
	if err := s.Create(ctx, link, networkID); err != nil {
		return err
	}
	return event.Dispatch(s.dispatcher, event.LinkDeployed{Link: link})
}

// Create is `create` (`KubernetesLink.py:105`): adopt the existing NAD if there
// is one, otherwise submit a new one.
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
func (s *linkService) Wipe(ctx context.Context) error {
	networks, err := s.getByFilters(ctx, "", "")
	if err != nil {
		return err
	}
	return runChunked(ctx, networks, networkItemName, s.undeployLink)
}

// undeployLink is `_undeploy_link` (`KubernetesLink.py:173`).
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
func NetworkID(seed, name string, offset int) int {
	digest := sha256.Sum256([]byte(seed + name))

	value := new(big.Int).SetBytes(digest[:])
	value.Add(value, big.NewInt(int64(offset)))
	value.Mod(value, big.NewInt(maxLinkNumber))

	return int(value.Int64())
}
