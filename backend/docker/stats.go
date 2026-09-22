package docker

import (
	"context"
	"io"
	"slices"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kathara"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// machineStatsFor is `DockerMachineStats.__init__` reduced to its static half
// (`stats/DockerMachineStats.py:38-42`) plus the one field `update()` sets that
// is inventory rather than sampling (`status`, :68).
func machineStatsFor(c *Container, imageTags []string) *kathara.MachineStats {
	user := c.Label(labelUser)
	status := c.Status()

	image := ""
	if len(imageTags) > 0 {
		image = imageTags[0]
	} else if c.Attrs.ContainerJSONBase != nil {
		image = c.Attrs.Image
	}

	return &kathara.MachineStats{
		NetworkScenarioID: c.Label(labelLabHash),
		Name:              c.Label(labelName),
		ContainerName:     c.Name(),
		User:              &user,
		Status:            &status,
		Image:             image,
	}
}

// linkStatsFor is `DockerLinkStats.__init__` (`stats/DockerLinkStats.py:26-34`)
// reduced to inventory.
func linkStatsFor(n *Network, containers []string) *kathara.LinkStats {
	user := n.Label(labelUser)
	ipv6 := n.Attrs.EnableIPv6

	var external []string
	if label := n.Label(labelExternal); label != "" {
		external = strings.Split(label, ";")
	}

	return &kathara.LinkStats{
		NetworkScenarioID: n.Label(labelLabHash),
		Name:              n.Label(labelName),
		NetworkName:       n.Name(),
		User:              &user,
		IPv6Enabled:       &ipv6,
		External:          external,
		Containers:        containers,
	}
}

// imageTags is docker-py's `container.image.tags`: a second API call, which is
// why it is only made where a tag is actually rendered.
func (m *Manager) imageTags(ctx context.Context, imageID string) []string {
	if imageID == "" {
		return nil
	}
	inspected, err := m.api.ImageInspect(ctx, imageID)
	if err != nil {
		return nil
	}
	return inspected.RepoTags
}

// imageLabel is `container.image.tags[0]` made total: the first tag when the
// image has one, the image reference otherwise. It is what the shutdown
// warning interpolates.
func (m *Manager) imageLabel(ctx context.Context, c *Container) string {
	if c.Attrs.ContainerJSONBase == nil {
		return ""
	}
	if tags := m.imageTags(ctx, c.Attrs.Image); len(tags) > 0 {
		return tags[0]
	}
	return c.Attrs.Image
}

// ---------------------------------------------------------------------------
// Machines
// ---------------------------------------------------------------------------

// machinesStatsStream is the generator `DockerMachine.get_machines_stats`
// returns (`DockerMachine.py:1020`).
type machinesStatsStream struct {
	manager     *Manager
	labHash     string
	machineName string
	user        string
	// checked guards the privilege test, which Python performs at the top of
	// the generator body and therefore ONCE, at the first `next()`
	// (`:1037-1038`).
	checked bool
}

// Next is one `next()` on the generator.
func (s *machinesStatsStream) Next(ctx context.Context) ([]kathara.MachineStatsEntry, error) {
	if !s.checked {
		admin, err := util.IsAdmin()
		if err != nil {
			return nil, err
		}
		if s.user == "" && !admin {
			return nil, kerrors.ErrPrivilegeMachineStats
		}
		s.checked = true
	}

	containers, err := s.manager.machine.getByFilters(ctx, s.labHash, s.machineName, s.user)
	if err != nil {
		return nil, err
	}

	entries := make([]kathara.MachineStatsEntry, 0, len(containers))
	for _, c := range containers {
		// Keep Python's keying: the dict key is the container name, not
		// the device name, despite the docstring saying otherwise
		// (`DockerMachine.py:1044`).
		tags := []string(nil)
		if c.Attrs.ContainerJSONBase != nil {
			tags = s.manager.imageTags(ctx, c.Attrs.Image)
		}
		entries = append(entries, kathara.MachineStatsEntry{
			ID:    c.Name(),
			Stats: machineStatsFor(c, tags),
		})
	}
	slices.SortFunc(entries, func(a, b kathara.MachineStatsEntry) int { return strings.Compare(a.ID, b.ID) })
	return entries, nil
}

// Close releases nothing: the stream holds no connection, only its filters.
func (s *machinesStatsStream) Close() error { return nil }

// machineStatsStream is the generator `DockerManager.get_machine_stats` returns
// (`DockerManager.py:877`): one element, then the end.
type machineStatsStream struct {
	inner *machinesStatsStream
	// check is the deferred `check_required_single_not_none_var`. The Python
	// method body holds `yield`, so its guard does not run until the first
	// `next()` — moving it to the call would make an observable error appear
	// earlier ([kathara.Manager.GetMachineStats]).
	check error
	done  bool
}

// Next is the single `next()`.
func (s *machineStatsStream) Next(ctx context.Context) (*kathara.MachineStats, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true

	if s.check != nil {
		return nil, s.check
	}

	entries, err := s.inner.Next(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0].Stats, nil
}

func (s *machineStatsStream) Close() error { return nil }

// ---------------------------------------------------------------------------
// Links
// ---------------------------------------------------------------------------

// linksStatsStream is the generator `DockerLink.get_links_stats` returns
// (`DockerLink.py:250`).
type linksStatsStream struct {
	manager  *Manager
	labHash  string
	linkName string
	user     string
	checked  bool
}

// Next is one `next()`. The privilege check is the collision-domain twin of
// [machinesStatsStream.Next]'s and carries its own message
// (`DockerLink.py:267-268`).
func (s *linksStatsStream) Next(ctx context.Context) ([]kathara.LinkStatsEntry, error) {
	if !s.checked {
		admin, err := util.IsAdmin()
		if err != nil {
			return nil, err
		}
		if s.user == "" && !admin {
			return nil, kerrors.ErrPrivilegeLinkStats
		}
		s.checked = true
	}

	networks, err := s.manager.link.getByFilters(ctx, s.labHash, s.linkName, s.user)
	if err != nil {
		return nil, err
	}

	entries := make([]kathara.LinkStatsEntry, 0, len(networks))
	for _, n := range networks {
		containers, err := n.AttachedNames(ctx, s.manager.api)
		if err != nil {
			return nil, err
		}
		entries = append(entries, kathara.LinkStatsEntry{ID: n.Name(), Stats: linkStatsFor(n, containers)})
	}
	slices.SortFunc(entries, func(a, b kathara.LinkStatsEntry) int { return strings.Compare(a.ID, b.ID) })
	return entries, nil
}

func (s *linksStatsStream) Close() error { return nil }

// linkStatsStream is the generator `DockerManager.get_link_stats` returns
// (`DockerManager.py:967`), lazy for the same reason
// [machineStatsStream] is.
type linkStatsStream struct {
	inner *linksStatsStream
	check error
	done  bool
}

func (s *linkStatsStream) Next(ctx context.Context) (*kathara.LinkStats, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true

	if s.check != nil {
		return nil, s.check
	}

	entries, err := s.inner.Next(ctx)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	return entries[0].Stats, nil
}

func (s *linkStatsStream) Close() error { return nil }
