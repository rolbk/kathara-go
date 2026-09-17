// This file is `DockerLink.py`: collision domains as Docker networks.

package docker

import (
	"context"

	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"

	"github.com/KatharaFramework/kathara-go/event"
	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
	"github.com/KatharaFramework/kathara-go/settings"
)

// linkService is `DockerLink` (`DockerLink.py:25`).
type linkService struct {
	manager *Manager
}

// DeployLinks is `deploy_links` (`DockerLink.py:33`).
//
// Two things happen that are easy to miss:
//
//   - the both-filters guard is TRUTHINESS, like the machine deploy path's and
//     unlike either undeploy path's;
//   - the bridge link is injected into the CALLER'S scenario on every call,
//     after the fan-out, whether or not anything was deployed
//     (`:72-75`, docker-backend.md gotcha 15). `kathara_host_bridge` therefore
//     appears in `lab.links` of any scenario that has ever been deployed, and
//     its api_object is the Docker `bridge` network or nil when there is none.
//
// The `_started`/`_ended` events bracket the fan-out and fire only when the
// filtered set is non-empty, so deploying a scenario with no collision domains
// produces no progress bar and still injects the bridge.
func (s *linkService) DeployLinks(ctx context.Context, lab *model.Lab, selected, excluded kathara.NameSet) error {
	if len(selected) > 0 && len(excluded) > 0 {
		return kerrors.ErrSelectedOrExcludedLinks
	}

	links := filterLinks(lab.Links(), selected, excluded)

	if len(links) > 0 {
		if err := event.Dispatch(s.manager.dispatcher, event.LinksDeployStarted{Links: links}); err != nil {
			return err
		}

		if err := runChunked(ctx, links, linkItemName, s.deployLink); err != nil {
			return err
		}

		if err := event.Dispatch(s.manager.dispatcher, event.LinksDeployEnded{}); err != nil {
			return err
		}
	}

	bridge, err := s.DockerBridge(ctx)
	if err != nil {
		return err
	}
	link := lab.GetOrNewLink(model.BridgeLinkName)
	if bridge == nil {
		// `link.api_object = None` — an explicit nil, not "leave it alone".
		link.APIObject = nil
	} else {
		link.APIObject = bridge
	}
	return nil
}

// deployLink is `_deploy_link` (`DockerLink.py:77`), the pool's unit of work.
// The reserved bridge name is skipped here as well as inside
// [linkService.Create], and skipping it means the `link_deployed` event does
// NOT fire for it.
func (s *linkService) deployLink(ctx context.Context, link *model.Link) error {
	if link.Name == model.BridgeLinkName {
		return nil
	}
	if err := s.Create(ctx, link); err != nil {
		return err
	}
	return event.Dispatch(s.manager.dispatcher, event.LinkDeployed{Link: link})
}

// filterLinks is the dict comprehension of `deploy_links`
// (`DockerLink.py:50-58`), preserving scenario order.
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

// Create is `DockerLink.create` (`DockerLink.py:95`): find or make the Docker
// network for a collision domain.
//
// The reuse lookup and the labels both key off `shared_cds`, and they key off
// it in OPPOSITE directions, which is what makes the three modes work:
//
//	NOT_SHARED  look up by (name, lab_hash, user); label with user + lab_hash
//	LABS        look up by (name, user);           label with user
//	USERS       look up by (name);                 label with neither
//
// So widening the mode widens both the search and the set of networks that can
// match it. `networks.pop()` takes the LAST match (ORDERING.tsv row 10).
//
// External collision domains are DEFERRED (PORT_SPEC §0.3): the label they
// would fill is always "" and [externalLabel] answers `FeatureNotAvailable`
// rather than a wrong value if a caller hand-built one.
func (s *linkService) Create(ctx context.Context, link *model.Link) error {
	if link.Name == model.BridgeLinkName {
		return nil
	}

	shared := s.manager.settings.SharedCds

	// `utils.get_current_user_name()` is called inside the two `!= USERS`
	// branches and inside `get_network_name`'s NOT_SHARED/LABS arms — never on
	// the USERS path (`DockerLink.py:117-118,131`, :410-415). It can fail (the
	// passwd lookup), so resolving it unconditionally would fail a shared-
	// between-users deploy Python completes.
	user := ""
	if shared != settings.SharedBetweenUsers {
		var err error
		if user, err = util.GetCurrentUserName(); err != nil {
			return err
		}
	}

	filterLabHash := ""
	filterUser := ""
	if shared == settings.NotShared {
		filterLabHash = link.Lab.Hash
	}
	if shared != settings.SharedBetweenUsers {
		filterUser = user
	}

	networks, err := s.getByFilters(ctx, filterLabHash, link.Name, filterUser)
	if err != nil {
		return err
	}
	if len(networks) > 0 {
		link.APIObject = networks[len(networks)-1]
		return nil
	}

	external, err := externalLabel(link)
	if err != nil {
		return err
	}

	networkPlugin := pluginNameOf(s.manager.settings)

	architecture, err := util.GetArchitecture()
	if err != nil {
		return err
	}

	created, err := s.manager.api.NetworkCreate(ctx,
		NetworkName(s.manager.settings.NetPrefix, user, link.Name, link.Lab.Hash, shared),
		networkCreateOptions(
			NetworkDriver(networkPlugin, architecture),
			NetworkLabels(link.Name, user, link.Lab.Hash, external, shared),
		),
	)
	if err != nil {
		return err
	}

	inspected, err := s.manager.api.NetworkInspect(ctx, created.ID, network.InspectOptions{})
	if err != nil {
		return err
	}
	link.APIObject = &Network{ID: created.ID, Attrs: inspected}

	// `if link.external:` — unreachable in 1.0, since `externalLabel` above
	// has already answered FeatureNotAvailable for a non-empty list.
	return nil
}

// Undeploy is `DockerLink.undeploy` (`DockerLink.py:154`).
//
// The three-step filter is the whole semantic:
//
//  1. every network of the scenario — note NO user filter, unlike the machine
//     undeploy, so a shared collision domain another user created is a
//     candidate;
//  2. `selected_links` if it is NOT NONE — an empty set therefore selects
//     nothing, the inverse of the deploy path;
//  3. reload each survivor and keep only those with ZERO attached containers,
//     which is what makes a shared collision domain still in use survive a
//     partial teardown.
//
// The reload in step 3 is sequential and happens before the pool, so its cost
// is linear in the scenario's collision domains; that is Python's shape and the
// events bracket only the deletion.
func (s *linkService) Undeploy(ctx context.Context, labHash string, selected kathara.NameSet) error {
	networks, err := s.getByFilters(ctx, labHash, "", "")
	if err != nil {
		return err
	}
	if selected != nil {
		kept := make([]*Network, 0, len(networks))
		for _, n := range networks {
			if selected.Has(n.Label(labelName)) {
				kept = append(kept, n)
			}
		}
		networks = kept
	}

	networks, err = s.reloadAndKeepEmpty(ctx, networks)
	if err != nil {
		return err
	}
	if len(networks) == 0 {
		return nil
	}

	items := make([]event.APIObject, 0, len(networks))
	for _, n := range networks {
		items = append(items, n)
	}
	if err := event.Dispatch(s.manager.dispatcher, event.LinksUndeployStarted{Networks: items}); err != nil {
		return err
	}

	if err := runChunked(ctx, networks, networkItemName, s.undeployLink); err != nil {
		return err
	}

	return event.Dispatch(s.manager.dispatcher, event.LinksUndeployEnded{})
}

// Wipe is `DockerLink.wipe` (`DockerLink.py:184`).
//
// The user filter is dropped entirely in `SharedBetweenUsers` mode
// (`:193`): collision domains carry no `user` label there, so filtering by one
// would match nothing and a wipe would leave every network behind. The
// "keep only the empty ones" rule still applies, and there are no events.
func (s *linkService) Wipe(ctx context.Context, user string) error {
	userLabel := user
	if s.manager.settings.SharedCds == settings.SharedBetweenUsers {
		userLabel = ""
	}

	networks, err := s.getByFilters(ctx, "", "", userLabel)
	if err != nil {
		return err
	}
	networks, err = s.reloadAndKeepEmpty(ctx, networks)
	if err != nil {
		return err
	}
	return runChunked(ctx, networks, networkItemName, s.undeployLink)
}

// reloadAndKeepEmpty is the `for item in networks: item.reload()` loop plus the
// `len(item.containers) <= 0` filter both teardown paths run
// (`DockerLink.py:168-170`, `:195-197`).
func (s *linkService) reloadAndKeepEmpty(ctx context.Context, networks []*Network) ([]*Network, error) {
	for _, n := range networks {
		if err := reloadNetwork(ctx, s.manager.api, n); err != nil {
			return nil, err
		}
	}
	kept := make([]*Network, 0, len(networks))
	for _, n := range networks {
		if n.ContainerCount() <= 0 {
			kept = append(kept, n)
		}
	}
	return kept, nil
}

// undeployLink is `_undeploy_link` (`DockerLink.py:206`).
func (s *linkService) undeployLink(ctx context.Context, n *Network) error {
	if err := s.deleteLink(ctx, n); err != nil {
		return err
	}
	return event.Dispatch(s.manager.dispatcher, event.LinkUndeployed{Network: n})
}

// deleteLink is `_delete_link` (`DockerLink.py:300`): detach any external
// interfaces, then remove the network.
//
// The `external` label is read UNGUARDED in Python and a network without it
// would KeyError; [NetworkLabels] always emits the key, so the only way to see
// one without it is a foreign network the `app=kathara` filter should not have
// matched. Reading it as "" here is that KeyError's benign twin — an empty
// label means no external links either way.
//
// The external teardown itself is DEFERRED (PORT_SPEC §0.3): a network carrying
// a non-empty `external` label was created by a Python Kathará, and removing it
// without detaching the host interfaces would leave them dangling, so this
// answers `FeatureNotAvailable` instead of removing the network.
func (s *linkService) deleteLink(ctx context.Context, n *Network) error {
	if n.Label(labelExternal) != "" {
		return kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
	}
	return s.manager.api.NetworkRemove(ctx, n.ID)
}

// DockerBridge is `get_docker_bridge` (`DockerLink.py:219`):
// `client.networks.list(names="bridge")` then `.pop()`.
//
// The `names` filter is a SUBSTRING match on the daemon side — docker-py turns
// it into `filters={'name': 'bridge'}` and the daemon does not anchor it — so
// a host with a network called `my-bridge-net` can match more than one, and
// `.pop()` then takes the last. Both are reproduced; nil is Python's None,
// which leaves the bridge link's api_object unset.
func (s *linkService) DockerBridge(ctx context.Context) (*Network, error) {
	args := filters.NewArgs(filters.Arg("name", "bridge"))
	summaries, err := s.manager.api.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return nil, err
	}
	if len(summaries) == 0 {
		return nil, nil
	}

	last := summaries[len(summaries)-1]
	// docker-py's `networks.list` is not greedy here — no `greedy=True` — so
	// the object carries only what the listing returned. The one field any
	// caller reads is the id, which `NetworkConnect` needs.
	return &Network{ID: last.ID, Attrs: last}, nil
}

// getByFilters is `get_links_api_objects_by_filters` (`DockerLink.py:228`).
func (s *linkService) getByFilters(ctx context.Context, labHash, linkName, user string) ([]*Network, error) {
	return listNetworks(ctx, s.manager.api, ObjectFilters(user, labHash, linkName))
}
