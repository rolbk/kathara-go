// This file is `stats/KubernetesMachineStats.py` and
// `stats/KubernetesLinkStats.py`, reduced to inventory by PORT_SPEC §0.3.
//
// # What is deferred and what is not
//
// Nothing in these two classes is resource sampling: the Kubernetes stats
// objects never carried cpu, memory or throughput, so they fall entirely under
// the §0.3 partial exception (EXPECTATIONS-k8s, "Notes for the porting
// agents"). What is dropped is the `interfaces` string — the
// `"{idx}:{link}"` summary the Docker side calls a sampled field — because
// JSON_CLI_CONTRACT.md §3.0.2 does not carry it and
// [kathara.MachineStats] has no field for it.
//
// # The generator quirks that do not survive
//
// Python's generators are infinite, re-query on every step, YIELD THE SAME DICT
// OBJECT each round, and on an empty result `yield dict()` and then FALL
// THROUGH — no `continue` — so the following step returns stale accumulated
// entries without re-querying (k8s-backend.md G16). All three are artefacts of
// the accumulate-and-resample machinery that is being deferred. Each step here
// re-queries and returns what is running now, which is what the surviving
// observable behaviours require: an empty result is an empty batch and NOT the
// end of the stream (NILABILITY.tsv:64), and the stream never ends on its own.
//
// # Ordering
//
// Python fills the dict from pool threads, so its iteration order is completion
// order and `kathara list`'s rows differ run to run. ORDERING.tsv rows 79 and
// 88 rule the port sorts instead, and the singular getters pick the first entry
// by sorted id where Python's `popitem()` takes whichever thread finished last.

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
//
// Three fields differ from the Docker backend's and each is pinned by the
// contract (JSON_CLI_CONTRACT.md §3.0.2 and its §7 ruling):
//
//   - `container_name` carries the POD name. Python's `to_dict()` calls the key
//     `pod_name`; the canonical key wins.
//   - `user` is always null. Kubernetes records no deploying user, and the
//     contract requires the key to be present anyway.
//   - `assigned_node` is the one additive backend key. It is `spec.node_name`,
//     so it is a string once the pod is scheduled and NULL while it is Pending
//     — never absent, which is what Docker's is. All three states of
//     [kathara.OptionalString] are live across the two backends.
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
//
// The ladder, in Python's order:
//
//  1. no container statuses at all → the pod PHASE;
//  2. the first container's state is terminated → its reason, or the literal
//     "Terminating" when the reason is absent;
//  3. waiting → its reason, which can itself be absent;
//  4. anything else, and the absent reasons of 2 and 3 → the pod phase.
//
// Then `string_status.split(': ')[0]`: a reason carrying an error message —
// `"CreateContainerConfigError: secret not found"` — is truncated at the first
// `": "`, so the rendered status stays one word.
//
// An empty reason string is Python's `None` here: the API's Go types have no
// null string, and a reason that is genuinely empty renders the same way under
// either reading.
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
//
// `vxlan_id` is parsed out of the NAD's `spec.config` JSON string, and it is the
// only field this backend fills that the Docker one leaves nil. `user` is
// always null: `KubernetesLinkStats.to_dict()` has no such key at all, and
// [kathara.LinkStats] emits it anyway (DIVERGENCES.md #53).
//
// A NAD whose config will not parse leaves the VNI nil rather than failing the
// listing, for the reason [linkService.existingNetworkIDs] gives.
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
//
// There is no privilege check on this backend — the Docker one gates
// `all_users` on root because it filters by a `user` label, and Megalos has no
// such label. `all_users` only produces a warning
// (`KubernetesManager.py:776-777`).
type machinesStatsStream struct {
	machine     *machineService
	labHash     string
	machineName string
}

// Next is one `next()` on the generator: the current inventory of every
// matching device, sorted by pod name.
//
// The entry key is the POD name (`machines_stats[pod.metadata.name]`), which is
// also what [kathara.MachineStats.ContainerName] carries — so the two agree
// here where on Docker they are the container name and the same.
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
//
// Python's `machines_stats_next.popitem()` takes the LAST-INSERTED entry, i.e.
// whichever pool thread finished last, which only matters when more than one
// pod matches the name — possible across scenarios. ORDERING.tsv row 88 rules
// the port picks deterministically: the first entry by sorted id.
//
// A nil result with a nil error is Python's `yield None`; the io.EOF comes on
// the following call.
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
