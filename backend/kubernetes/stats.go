package kubernetes

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// machineStatsFor is `KubernetesMachineStats.__init__`
// (`stats/KubernetesMachineStats.py:24`) plus the two fields `update()` sets,
// both of which are inventory rather than sampling (`:53-54`).
func machineStatsFor(pod *corev1.Pod) *kathara.MachineStats {
	// `container_statuses[0].image if container_statuses else "N/A"` — the
	// literal string, not an empty one.
	image := "N/A"
	if len(pod.Status.ContainerStatuses) > 0 {
		image = pod.Status.ContainerStatuses[0].Image
	}

	status := detailedMachineStatus(pod)

	assignedNode := kathara.NullString()
	if pod.Spec.NodeName != "" {
		assignedNode = kathara.SomeString(pod.Spec.NodeName)
	}

	return &kathara.MachineStats{
		NetworkScenarioID: pod.Namespace,
		Name:              pod.Labels[labelName],
		ContainerName:     pod.Name,
		User:              nil,
		Status:            &status,
		Image:             image,
		AssignedNode:      assignedNode,
	}
}

// detailedMachineStatus is `_get_detailed_machine_status`
// (`stats/KubernetesMachineStats.py:56`).
func detailedMachineStatus(pod *corev1.Pod) string {
	if len(pod.Status.ContainerStatuses) == 0 {
		return string(pod.Status.Phase)
	}

	state := pod.Status.ContainerStatuses[0].State

	stringStatus := ""
	switch {
	case state.Terminated != nil:
		stringStatus = state.Terminated.Reason
		if stringStatus == "" {
			stringStatus = "Terminating"
		}
	case state.Waiting != nil:
		stringStatus = state.Waiting.Reason
	}

	if stringStatus == "" {
		return string(pod.Status.Phase)
	}
	return strings.SplitN(stringStatus, ": ", 2)[0]
}

// linkStatsFor is `KubernetesLinkStats.__init__`
// (`stats/KubernetesLinkStats.py:19`).
func linkStatsFor(network *Network) *kathara.LinkStats {
	stats := &kathara.LinkStats{
		NetworkScenarioID: NetworkNamespace(network),
		Name:              NetworkLinkName(network),
		NetworkName:       NetworkNameOf(network),
		User:              nil,
	}
	if id, ok := NetworkVXLANID(network); ok {
		stats.VXLANID = &id
	}
	return stats
}

// ---------------------------------------------------------------------------
// Machines
// ---------------------------------------------------------------------------

// machinesStatsStream is the generator `KubernetesMachine.get_machines_stats`
// returns (`KubernetesMachine.py:1011`).
type machinesStatsStream struct {
	machine     *machineService
	labHash     string
	machineName string
}

// Next is one `next()` on the generator: the current inventory of every
// matching device, sorted by pod name.
func (s *machinesStatsStream) Next(ctx context.Context) ([]kathara.MachineStatsEntry, error) {
	pods, err := s.machine.getByFilters(ctx, s.labHash, s.machineName)
	if err != nil {
		return nil, err
	}

	entries := make([]kathara.MachineStatsEntry, 0, len(pods))
	for _, pod := range pods {
		entries = append(entries, kathara.MachineStatsEntry{ID: pod.Name, Stats: machineStatsFor(pod)})
	}
	slices.SortFunc(entries, func(a, b kathara.MachineStatsEntry) int { return strings.Compare(a.ID, b.ID) })
	return entries, nil
}

// Close releases nothing: the stream holds no connection, only its filters.
func (s *machinesStatsStream) Close() error { return nil }

// machineStatsStream is the generator `KubernetesManager.get_machine_stats`
// returns (`KubernetesManager.py:781`): one element, then the end.
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

// linksStatsStream is the generator `KubernetesLink.get_links_stats` returns
// (`KubernetesLink.py:231`).
type linksStatsStream struct {
	link     *linkService
	labHash  string
	linkName string
}

// Next is one `next()`, sorted by the Kubernetes network name — which is the
// dict key Python uses (`networks_stats[network['metadata']['name']]`).
func (s *linksStatsStream) Next(ctx context.Context) ([]kathara.LinkStatsEntry, error) {
	networks, err := s.link.getByFilters(ctx, s.labHash, s.linkName)
	if err != nil {
		return nil, err
	}

	entries := make([]kathara.LinkStatsEntry, 0, len(networks))
	for _, network := range networks {
		entries = append(entries, kathara.LinkStatsEntry{ID: NetworkNameOf(network), Stats: linkStatsFor(network)})
	}
	slices.SortFunc(entries, func(a, b kathara.LinkStatsEntry) int { return strings.Compare(a.ID, b.ID) })
	return entries, nil
}

func (s *linksStatsStream) Close() error { return nil }

// linkStatsStream is the generator `KubernetesManager.get_link_stats` returns
// (`KubernetesManager.py:872`), lazy for the same reason [machineStatsStream]
// is.
type linkStatsStream struct {
	inner *linksStatsStream
	check error
	// warnAllUsers defers `if all_users: logging.warning(...)` to the first
	// step, because `get_link_stats` forwards the flag to the plural getter
	// from inside its generator body ([Manager.GetLinkStats]).
	warnAllUsers bool
	done         bool
}

func (s *linkStatsStream) Next(ctx context.Context) (*kathara.LinkStats, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true

	if s.check != nil {
		return nil, s.check
	}
	if s.warnAllUsers {
		slog.Warn("User-specific options have no effect on Megalos.")
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
