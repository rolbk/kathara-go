// This file is `stats/DockerMachineStats.py` and `stats/DockerLinkStats.py`,
// reduced to inventory by PORT_SPEC §0.3.
//
// # What is deferred and what is not
//
// Resource sampling — pids, cpu, memory, network throughput, the interface
// summary — is post-1.0 and absent. `IMachineStats.update()` is where it lived
// and [kathara.MachineStats.Update] answers `FeatureNotAvailable` for it. What
// survives is the six fields `kathara list` renders, which come out of the
// container listing that command already performs, plus the official API
// tutorial's `next(get_machines_stats(...))` liveness check that PORT_SPEC §11
// makes a release gate.
//
// # The generator quirks that do not survive
//
// Python's generators are infinite, re-query on every step, YIELD THE SAME DICT
// OBJECT each round, and on an empty result `yield dict()` and then FALL
// THROUGH — no `continue` — so the following step returns stale accumulated
// entries without re-querying (docker-backend.md gotcha 19). All three are
// artefacts of the accumulate-and-resample machinery that is being deferred:
// there is nothing to accumulate when every step rebuilds the inventory from
// the listing. Each step here re-queries and returns what is running now, which
// is what the surviving observable behaviours require — an empty result is an
// empty batch and NOT the end of the stream (NILABILITY.tsv:64), and the stream
// never ends on its own.
//
// # Ordering
//
// Python fills the dict from pool threads, so its iteration order is completion
// order and `kathara list`'s rows differ run to run. ORDERING.tsv rows 44 and
// 51 rule the port sorts instead, and rows 59 and 88 rule the singular getters
// pick deterministically where Python's `popitem()` takes whichever thread
// finished last.

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

	// `machine_api_object.image.tags[0]`, which IndexErrors on an untagged
	// image. The image reference stands in for the missing tag, because a
	// listing must not be able to fail on one container (docker-backend.md
	// gotcha 21, PORT_SPEC §10).
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
		// AssignedNode stays at its zero value: `DockerMachineStats.to_dict()`
		// has no such key, and the zero [kathara.OptionalString] is the absent
		// state, so the key is not emitted (JSON_CLI_CONTRACT.md §3.0.2).
	}
}

// linkStatsFor is `DockerLinkStats.__init__` (`stats/DockerLinkStats.py:26-34`)
// reduced to inventory.
//
// # The reproduced-as-benign bug
//
// Python indexes `attrs['Labels']['lab_hash']` and `['user']` directly, and
// [NetworkLabels] deliberately omits both in the shared modes — so
// constructing a `DockerLinkStats` for a shared collision domain KeyErrors
// (SYNTHESIS §1.2). The label reads here answer "" instead. The crash is
// recorded in DIVERGENCES.md rather than reproduced: it is a latent bug on a
// path 1.0 does not render (no CLI command reads `get_links_stats`), and
// reproducing it would make an API call fail for a configuration the same
// backend produced two functions earlier.
// `containers` is what `self.update()` leaves behind, i.e.
// [Network.AttachedNames]; it is passed in rather than fetched because it costs
// one inspect per attached container and this function is otherwise pure.
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
//
// A failure is swallowed into an empty tag list rather than returned. Python
// reaches the same place by a different route — `client.images.get` raising
// would propagate — but the two callers are a stats listing and a warning line,
// and neither is allowed to fail because an image was removed underneath it.
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
//
// The privilege check fires here and not from the call that built the stream,
// because the Python body holds `yield` and nothing in it runs until now
// ([kathara.MachinesStatsStream.Next]). `user == ""` is Python's `user is None`,
// i.e. "every user", which needs root.
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
		// OQ-22 keeps Python's keying: the dict key is the CONTAINER name, not
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
//
// Python's `machines_stats_next.popitem()` takes the LAST-INSERTED entry, i.e.
// whichever pool thread finished last, which only matters when more than one
// container matches the name — possible with `all_users` or across scenarios.
// ORDERING.tsv row 59 rules the port picks deterministically: the first entry
// by sorted ID, which is what the sorted batch below yields.
//
// A `None` batch is Python's `yield None`, and the generator stays alive for
// one more `next()` that then raises StopIteration; nil with a nil error here
// is that None, and the io.EOF comes on the following call.
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
