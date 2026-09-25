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
	if len(link.External) > 0 {
		if err := s.preflightExternalInterfaces(ctx, link.External); err != nil {
			return err
		}
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

	if len(link.External) > 0 {
		if err := s.attachExternalInterfaces(ctx, link.External, link.APIObject.(*Network)); err != nil {
			return err
		}
	}
	return nil
}

// Undeploy is `DockerLink.undeploy` (`DockerLink.py:154`).
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
func (s *linkService) deleteLink(ctx context.Context, n *Network) error {
	if external := n.Label(labelExternal); external != "" {
		if err := s.deleteExternalInterfaces(ctx, external, n); err != nil {
			return err
		}
	}
	return s.manager.api.NetworkRemove(ctx, n.ID)
}

// DockerBridge is `get_docker_bridge` (`DockerLink.py:219`):
// `client.networks.list(names="bridge")` then `.pop()`.
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
