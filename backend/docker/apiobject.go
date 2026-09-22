// This file has no single Python original: it is docker-py's `Container` and
// `Network` model objects, which the Go SDK does not have.
// docker-py's `client.containers.list()` returns *inspected* objects — it lists
// ids and then calls `inspect` on each one — and every read Kathará does
// (`container.attrs["HostConfig"]`, `container.labels`, `container.status`,
// `network.attrs["Labels"]`, `network.containers`) assumes that. The Go SDK's
// `ContainerList` returns summaries instead, so the inspect has to be issued
// explicitly. These two types are where that happens, once, so that no caller
// has to remember.

package docker

import (
	"context"
	"slices"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

// Container is docker-py's `docker.models.containers.Container`: the handle
// [Manager.GetMachineAPIObject] hands back and the value
// [model.Machine.APIObject] holds.
type Container struct {
	// ID is `container.id`.
	ID string

	// Attrs is `container.attrs`: the full inspect response.
	Attrs container.InspectResponse
}

// Name is `container.name`: `attrs['Name'].lstrip('/')`.
func (c *Container) Name() string {
	if c == nil || c.Attrs.ContainerJSONBase == nil {
		return ""
	}
	return strings.TrimLeft(c.Attrs.Name, "/")
}

// Labels is `container.labels`: `attrs['Config']['Labels']`, never nil for a
// container this package created.
func (c *Container) Labels() map[string]string {
	if c == nil || c.Attrs.Config == nil {
		return nil
	}
	return c.Attrs.Config.Labels
}

// Label reads one label, empty when absent. Python indexes the dict directly
// and KeyErrors on a foreign container; every site here is reached through an
// `app=kathara` filter, so the key is present by construction.
func (c *Container) Label(key string) string { return c.Labels()[key] }

// Status is `container.status`: `attrs['State']['Status']`, the string
// `connect_machine_to_link` compares against "running"
// (`DockerManager.py:202`) and `_delete_machine` against the same (:1101).
func (c *Container) Status() string {
	if c == nil || c.Attrs.ContainerJSONBase == nil || c.Attrs.State == nil {
		return ""
	}
	return string(c.Attrs.State.Status)
}

// HostConfig is `attrs['HostConfig']`, which `get_lab_from_api` reads five
// fields out of (`DockerManager.py:717-740`). Nil for a zero-value Attrs.
func (c *Container) HostConfig() *container.HostConfig {
	if c == nil || c.Attrs.ContainerJSONBase == nil {
		return nil
	}
	return c.Attrs.HostConfig
}

// Networks is `attrs["NetworkSettings"]["Networks"]`, the endpoint map keyed by
// Docker network name.
func (c *Container) Networks() map[string]*network.EndpointSettings {
	if c == nil || c.Attrs.NetworkSettings == nil {
		return nil
	}
	return c.Attrs.NetworkSettings.Networks
}

// Network is docker-py's `docker.models.networks.Network`, the handle
// [Manager.GetLinkAPIObject] returns and [model.Link.APIObject] holds.
type Network struct {
	// ID is `network.id`.
	ID string
	// Attrs is `network.attrs`, the inspect response. `client.networks.list`
	// is called with `greedy=True` everywhere here, which is docker-py for
	// "inspect each one", so this is populated from the first listing on.
	Attrs network.Inspect
}

// Name is `network.name`.
func (n *Network) Name() string {
	if n == nil {
		return ""
	}
	return n.Attrs.Name
}

// Labels is `network.attrs['Labels']`.
func (n *Network) Labels() map[string]string {
	if n == nil {
		return nil
	}
	return n.Attrs.Labels
}

// Label reads one label, empty when absent.
func (n *Network) Label(key string) string { return n.Labels()[key] }

// ContainerCount is `len(network.containers)`, the only thing the undeploy and
// wipe paths read off the attached-container map (`DockerLink.py:170,197`):
// a collision domain is deleted when and only when the count is zero, which is
// what makes a shared collision domain survive a partial teardown.
func (n *Network) ContainerCount() int {
	if n == nil {
		return 0
	}
	return len(n.Attrs.Containers)
}

// AttachedNames is the DEVICE name of every container attached to this network,
// sorted.
func (n *Network) AttachedNames(ctx context.Context, api *client.Client) ([]string, error) {
	if n == nil {
		return nil, nil
	}
	names := make([]string, 0, len(n.Attrs.Containers))
	for _, id := range sortedKeys(n.Attrs.Containers) {
		inspected, err := api.ContainerInspect(ctx, id)
		if err != nil {
			return nil, err
		}
		attached := &Container{ID: id, Attrs: inspected}
		names = append(names, attached.Label(labelName))
	}
	slices.Sort(names)
	return names, nil
}

// ---------------------------------------------------------------------------
// Listing
// ---------------------------------------------------------------------------

// labelArgs turns the ordered filter terms of [ObjectFilters] into the SDK's
// filter set. The ordering the slice carries is lost here — `filters.Args` is a
// set — and that is fine: it is the *construction* order the goldens see, in
// the query string the SDK builds, and `filters.Args` marshals its values
// sorted, so two runs of the same command produce the same request.
func labelArgs(terms []LabelFilter) filters.Args {
	args := filters.NewArgs()
	for _, term := range terms {
		args.Add("label", term.String())
	}
	return args
}

// listContainers is `client.containers.list(all=True, filters=…,
// ignore_removed=True)` (`DockerMachine.py:1018`).
func listContainers(ctx context.Context, api *client.Client, terms []LabelFilter) ([]*Container, error) {
	summaries, err := api.ContainerList(ctx, container.ListOptions{
		All:     true,
		Filters: labelArgs(terms),
	})
	if err != nil {
		return nil, err
	}

	containers := make([]*Container, 0, len(summaries))
	for _, summary := range summaries {
		inspected, err := api.ContainerInspect(ctx, summary.ID)
		if err != nil {
			if isNotFound(err) {
				continue // ignore_removed=True
			}
			return nil, err
		}
		containers = append(containers, &Container{ID: summary.ID, Attrs: inspected})
	}
	return containers, nil
}

// listNetworks is `client.networks.list(filters=…, greedy=True)`
// (`DockerLink.py:248`).
func listNetworks(ctx context.Context, api *client.Client, terms []LabelFilter) ([]*Network, error) {
	summaries, err := api.NetworkList(ctx, network.ListOptions{Filters: labelArgs(terms)})
	if err != nil {
		return nil, err
	}

	networks := make([]*Network, 0, len(summaries))
	for _, summary := range summaries {
		inspected, err := api.NetworkInspect(ctx, summary.ID, network.InspectOptions{})
		if err != nil {
			return nil, err
		}
		networks = append(networks, &Network{ID: summary.ID, Attrs: inspected})
	}
	return networks, nil
}

// reloadContainer is `container.reload()`.
func reloadContainer(ctx context.Context, api *client.Client, c *Container) error {
	inspected, err := api.ContainerInspect(ctx, c.ID)
	if err != nil {
		return err
	}
	c.Attrs = inspected
	return nil
}

// reloadNetwork is `network.reload()`. `DockerLink.undeploy` and `wipe` call it
// on every candidate before the "no attached containers" filter, because the
// container list on a stale inspect is what decides whether a shared collision
// domain survives the teardown (`DockerLink.py:168-170,195-197`).
func reloadNetwork(ctx context.Context, api *client.Client, n *Network) error {
	inspected, err := api.NetworkInspect(ctx, n.ID, network.InspectOptions{})
	if err != nil {
		return err
	}
	n.Attrs = inspected
	return nil
}

// containerOf is the type assertion behind `machine.api_object`, which is
// `Any` in the model and a `docker.models.containers.Container` in practice.
// The second result is false for a device that was never deployed (nil) and
// for one deployed by the other backend.
func containerOf(v any) (*Container, bool) {
	c, ok := v.(*Container)
	return c, ok && c != nil
}

// networkOf is [containerOf] for `link.api_object`.
func networkOf(v any) (*Network, bool) {
	n, ok := v.(*Network)
	return n, ok && n != nil
}
