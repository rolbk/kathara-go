// This file is `foundation/manager/stats/IMachineStats.py` and
// `stats/ILinkStats.py`, reduced to inventory by PORT_SPEC §0.3 and reshaped
// from Python generators into streams whose eagerness matches, method by
// method, whether the Python body holds a `yield`.

package kathara

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// OptionalString is a JSON string with three states, not two: absent from the
// object entirely, present and null, or present with a value.
//
// One field needs all three. `assigned_node` is *absent* on Docker, whose
// `to_dict()` has no such key, and *present* on Kubernetes, where it is
// `pod.spec.node_name` and therefore null for a pod that has not been scheduled
// yet (`KubernetesMachineStats.py:44,56`) — JSON_CLI_CONTRACT.md §3.0.2 pins it
// as "(string|null)" and appendix A7 repeats it. A `*string` with `omitempty`
// can spell only the first of the two, and a `*string` without it only the
// second; neither can carry the difference, and the wire shape is fixed at this
// end (see [MachineStats]).
//
// The zero value is the absent one, which is what a Docker backend gets by
// leaving the field alone. [NullString] and [SomeString] are the other two.
type OptionalString struct {
	value   *string
	present bool
}

// SomeString is the present-with-a-value state: the key is emitted carrying s.
func SomeString(s string) OptionalString {
	return OptionalString{value: &s, present: true}
}

// NullString is the present-but-null state: the key is emitted carrying JSON
// null. On Kubernetes that is an unscheduled pod's `assigned_node`.
func NullString() OptionalString {
	return OptionalString{present: true}
}

// Present reports whether the key is emitted at all — true for both
// [SomeString] and [NullString], false for the zero value.
func (o OptionalString) Present() bool { return o.present }

// Value is the string and whether there is one. It is false for the zero value
// and for [NullString], which a caller that only wants to render a name can
// treat alike; [OptionalString.Present] is how the two are told apart.
func (o OptionalString) Value() (string, bool) {
	if o.value == nil {
		return "", false
	}
	return *o.value, true
}

// IsZero is what `omitzero` consults: the absent state, and nothing else, drops
// the key.
func (o OptionalString) IsZero() bool { return !o.present }

// MarshalJSON emits null for the present-but-null state and the string
// otherwise. The absent state never reaches here — `omitzero` removes the field
// first — but it encodes as null if some other caller marshals the value on its
// own, which is the safer of the two readings.
func (o OptionalString) MarshalJSON() ([]byte, error) {
	if o.value == nil {
		return []byte("null"), nil
	}
	return json.Marshal(*o.value)
}

// UnmarshalJSON is the inverse: a JSON null decodes to [NullString], a string
// to [SomeString]. An absent key never calls this, so it leaves the zero value
// in place, which is the absent state.
func (o *OptionalString) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*o = NullString()
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*o = SomeString(s)
	return nil
}

// MachineStats is what `kathara list` shows about one running device
// (`manager/docker/stats/DockerMachineStats.py`,
// `manager/kubernetes/stats/KubernetesMachineStats.py`), reduced to the
// inventory fields PORT_SPEC §0.3 keeps.
//
// The deferral is a partial one. Resource sampling — `pids`, `cpu_usage`,
// `mem_usage`, `mem_percent`, `net_usage`, `interfaces` — is post-1.0 and
// absent here; the six fields below are not, because they come out of the
// container listing `kathara list` already needs and because the official
// `getting-started` API tutorial ends with `print(next(get_machines_stats(…)))`
// as a liveness check, which PORT_SPEC §11 makes a release gate.
//
// The field order and the json tags are the machine inventory object of
// JSON_CLI_CONTRACT.md §3.0.2, whose key order is pinned to Python's
// `to_dict()` source order restricted to these fields. encoding/json emits
// struct fields in declaration order, so reordering them here changes the wire
// format; `internal/cliout` renders, but the shape is fixed at this end.
type MachineStats struct {
	// NetworkScenarioID is `network_scenario_id`: the scenario hash, from the
	// `lab_hash` container label on Docker and from the pod's namespace on
	// Kubernetes.
	NetworkScenarioID string `json:"network_scenario_id"`

	// Name is `name`: the device name as the scenario spells it, from the
	// `name` label.
	Name string `json:"name"`

	// ContainerName is `container_name`: the backend's own name for the
	// object — the container name on Docker, the *pod* name on Kubernetes,
	// whose Python `to_dict()` calls the key `pod_name`. The canonical key
	// wins (JSON_CLI_CONTRACT.md §3.0.2).
	ContainerName string `json:"container_name"`

	// User is `user`: the host user that deployed the device, from the `user`
	// label. Nil is Python's None — always so on Kubernetes, whose stats
	// object has no user at all.
	User *string `json:"user"`

	// Status is `status`: the container's state on Docker, the computed
	// phase-and-reason string on Kubernetes. Nil is Python's None, which is
	// the value the object carries before its first `update()`.
	Status *string `json:"status"`

	// Image is `image`: `image.tags[0]` on Docker, the first container
	// status's image on Kubernetes, where it is the literal "N/A" when the
	// pod has no container statuses yet.
	Image string `json:"image"`

	// AssignedNode is `assigned_node`, the cluster node the pod landed on. It
	// is the one additive backend key of JSON_CLI_CONTRACT.md §3.0.2 and
	// follows `image`.
	//
	// All three states of [OptionalString] are live. Docker leaves it at the
	// zero value and the key is not emitted, because `DockerMachineStats`
	// has no such key. Kubernetes always sets it — [SomeString] once the pod
	// is scheduled, [NullString] while it is Pending, since the value is
	// `pod.spec.node_name` and nothing filters the listing by phase
	// (`KubernetesMachine.py:983-1007`) — because the contract says the key
	// is present and "(string|null)" there.
	AssignedNode OptionalString `json:"assigned_node,omitzero"`
}

// Update is `IMachineStats.update()`, the re-sampling that refreshed the
// resource fields in place.
//
// It always fails. Live resource statistics are deferred to post-1.0
// (PORT_SPEC §0.3) and a deferred feature must say so rather than quietly do
// nothing (PORT_SPEC §0.4), so this returns `FeatureNotAvailable` with the
// `stats-sampling` token — ERROR_CODES.md §5 spells the message. The inventory
// fields above are not affected and are not an error: they are refreshed by
// taking another step on the stream that produced this value.
func (s *MachineStats) Update(context.Context) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureStatsSampling)
}

// LinkStats is the collision-domain twin of [MachineStats]
// (`manager/docker/stats/DockerLinkStats.py`,
// `manager/kubernetes/stats/KubernetesLinkStats.py`), reduced to inventory the
// same way.
//
// Nothing in 1.0 renders it — no CLI command reads `get_links_stats`, so
// JSON_CLI_CONTRACT.md pins no shape for it — and it exists for the API. The
// json tags are Python's `to_dict()` keys in `to_dict()` order all the same, so
// that a shape, if one is ever pinned, starts from the Python one. The two
// backends fill disjoint tails: `enable_ipv6`, `external` and `containers` are
// Docker's, `vxlan_id` is Kubernetes'.
//
// One key is the port's and not Python's, and it is here because the struct is
// one type where Python has two classes. `KubernetesLinkStats.to_dict()` is
// `{network_scenario_id, name, network_name, vxlan_id}` and has no `user` at
// all (`KubernetesLinkStats.py:40-45`), while this type always emits `user` —
// null on Kubernetes — before `vxlan_id`, exactly as [MachineStats] does for
// the machine record, whose §3.0.2 contract *requires* the nulled `user`.
// Recorded as DIVERGENCES.md #53; nothing renders it in 1.0, and whoever pins
// the shape later decides whether to keep it or split the type.
type LinkStats struct {
	// NetworkScenarioID is `network_scenario_id`: the scenario hash, from the
	// network's `lab_hash` label on Docker and the namespace on Kubernetes.
	NetworkScenarioID string `json:"network_scenario_id"`

	// Name is `name`: the collision-domain name as the scenario spells it.
	Name string `json:"name"`

	// NetworkName is `network_name`: the backend's own name for the network.
	NetworkName string `json:"network_name"`

	// User is `user`: the host user that deployed the network. Nil on
	// Kubernetes, which does not record one.
	User *string `json:"user"`

	// IPv6Enabled is `enable_ipv6`, read off the Docker network's
	// `EnableIPv6` attribute. Nil on Kubernetes.
	IPv6Enabled *bool `json:"enable_ipv6,omitempty"`

	// External is `external`: the host interfaces a lab.ext file attached,
	// which Python reads from a `;`-joined label. The feature is deferred
	// (PORT_SPEC §0.3), so nothing in 1.0 fills it.
	External []string `json:"external,omitempty"`

	// Containers is `containers`: the devices attached to this collision
	// domain. Python's value is a list of API objects and its `__str__` prints
	// each one's `name` label; the inventory reduction keeps the names.
	Containers []string `json:"containers,omitempty"`

	// VXLANID is `vxlan_id`, the VNI parsed out of the Kubernetes
	// network-attachment definition's config. Nil on Docker.
	VXLANID *int `json:"vxlan_id,omitempty"`
}

// Update is `ILinkStats.update()`. It always fails, for the reason
// [MachineStats.Update] does.
//
// Python's Docker implementation used it to re-list the attached containers
// and its Kubernetes one did nothing at all; both are re-sampling, and both
// are behind the same deferral.
func (s *LinkStats) Update(context.Context) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureStatsSampling)
}

// MachineStatsEntry is one pair of the `Dict[str, IMachineStats]` that
// `get_machines_stats` yields.
type MachineStatsEntry struct {
	// ID is the dict key: the *API object's* identifier, which is the
	// container name on Docker and the pod name on Kubernetes, not the device
	// name (`DockerMachine.py:1045`, OQ-22 keeps the keying).
	ID string

	// Stats is the value. Never nil inside a slice an implementation returns.
	Stats *MachineStats
}

// LinkStatsEntry is one pair of the `Dict[str, ILinkStats]` that
// `get_links_stats` yields.
type LinkStatsEntry struct {
	// ID is the dict key: the backend network's own name.
	ID string

	// Stats is the value. Never nil inside a slice an implementation returns.
	Stats *LinkStats
}

// MachinesStatsStream is the generator `get_machines_stats` returns: one
// snapshot of every matching device per step, for as long as the caller keeps
// asking.
//
// Python's generator is infinite — `while True:` around a fresh listing — and
// `kathara list --watch` consumes it that way while the one-shot `kathara list`
// and the API tutorial take a single `next()`. Both stay possible here.
type MachinesStatsStream interface {
	// Next is one `next()` on the generator: the current snapshot.
	//
	// An empty result is not the end of the stream. Python yields `dict()`
	// when nothing matches and keeps going, and `kathara list` renders that as
	// "No Devices Found" (NILABILITY.tsv:64); the end of the stream is io.EOF
	// and nothing else.
	//
	// Entries come back sorted by [MachineStatsEntry.ID]. Python's dict is
	// filled by a thread pool and yielded in completion order, which makes
	// the row order of `kathara list` differ run to run; ORDERING.tsv rows 44
	// and 79 rule that the port sorts instead, and the ordered slice is what
	// makes that rule expressible — a Go map could not carry it.
	//
	// Errors an implementation may surface here rather than from the call
	// that built the stream: [ErrPrivilege] when allUsers was set without
	// root, because the Python generator that raises it has not started
	// running until now (`DockerMachine.py:1037`).
	Next(ctx context.Context) ([]MachineStatsEntry, error)

	// Close releases whatever the stream holds open. Safe to call more than
	// once.
	Close() error
}

// MachineStatsStream is the generator `get_machine_stats` returns: exactly one
// element and then the end.
//
// The element is nil when no device matched — Python yields `None` and the
// generator stays alive for one more `next()`, which then raises StopIteration
// (`DockerManager.py:908,910`).
type MachineStatsStream interface {
	// Next is one `next()`: the device's inventory, nil when it was not
	// found, io.EOF once the single element has been taken.
	//
	// This is where the whole call fails when it fails. The Python method
	// body holds a `yield`, so nothing in it — not the
	// `check_required_single_not_none_var` at its top, not the privilege
	// check below it — runs before the first `next()`. Expect
	// [ErrInvocation] and [ErrPrivilege] from here.
	//
	// When more than one device matches the name (possible with allUsers, or
	// across scenarios), Python picks with `dict.popitem()`, i.e. the
	// last-inserted, i.e. whichever pool thread finished last. ORDERING.tsv
	// rows 59 and 88 rule that the port picks deterministically: the first
	// entry by sorted ID.
	Next(ctx context.Context) (*MachineStats, error)

	// Close releases whatever the stream holds open. Safe to call more than
	// once.
	Close() error
}

// LinksStatsStream is the collision-domain twin of [MachinesStatsStream], with
// the same empty-versus-ended distinction and the same sort.
type LinksStatsStream interface {
	// Next is one `next()` on the generator: the current snapshot, sorted by
	// [LinkStatsEntry.ID], empty rather than ended when nothing matches.
	Next(ctx context.Context) ([]LinkStatsEntry, error)

	// Close releases whatever the stream holds open. Safe to call more than
	// once.
	Close() error
}

// LinkStatsStream is the collision-domain twin of [MachineStatsStream], lazy
// for the same reason: its Python body holds a `yield`
// (`DockerManager.py:1001,1003`).
type LinkStatsStream interface {
	// Next is one `next()`: the collision domain's inventory, nil when it was
	// not found, io.EOF once the single element has been taken. The
	// deferred [ErrInvocation] and [ErrPrivilege] arrive here.
	Next(ctx context.Context) (*LinkStats, error)

	// Close releases whatever the stream holds open. Safe to call more than
	// once.
	Close() error
}
