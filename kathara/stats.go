package kathara

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// OptionalString is a JSON string with three states, not two: absent from the
// object entirely, present and null, or present with a value.
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

type MachineStats struct {
	// NetworkScenarioID is `network_scenario_id`: the scenario hash, from the
	// `lab_hash` container label on Docker and from the pod's namespace on
	// Kubernetes.
	NetworkScenarioID string `json:"network_scenario_id"`

	// Name is `name`: the device name as the scenario spells it, from the
	// `name` label.
	Name string `json:"name"`

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

	// AssignedNode is `assigned_node`, the cluster node the pod landed on.
	// All three states of [OptionalString] are live. Docker leaves it at the
	// zero value and the key is not emitted, because `DockerMachineStats`
	// has no such key. Kubernetes always sets it — [SomeString] once the pod
	// is scheduled, [NullString] while it is Pending, since the value is
	// `pod.spec.node_name` and nothing filters the listing by phase
	// (`KubernetesMachine.py:983-1007`).
	AssignedNode OptionalString `json:"assigned_node,omitzero"`
}

// Update is `IMachineStats.update()`, the re-sampling that refreshed the
// resource fields in place.
func (s *MachineStats) Update(context.Context) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureStatsSampling)
}

// LinkStats is the collision-domain twin of [MachineStats]
// (`manager/docker/stats/DockerLinkStats.py`,
// `manager/kubernetes/stats/KubernetesLinkStats.py`), reduced to inventory the
// same way.
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
func (s *LinkStats) Update(context.Context) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureStatsSampling)
}

// MachineStatsEntry is one pair of the `Dict[str, IMachineStats]` that
// `get_machines_stats` yields.
type MachineStatsEntry struct {
	// ID is the dict key: the *API object's* identifier, which is the
	// container name on Docker and the pod name on Kubernetes, not the device
	// name (`DockerMachine.py:1045`).
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
type MachinesStatsStream interface {
	// Next is one `next()` on the generator: the current snapshot.
	// An empty result is not the end of the stream.
	// Entries come back sorted by [MachineStatsEntry.ID].
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
type MachineStatsStream interface {
	// Next is one `next()`: the device's inventory, nil when it was not
	// found, io.EOF once the single element has been taken.
	// This is where the whole call fails when it fails. The Python method
	// body holds a `yield`, so nothing in it — not the
	// `check_required_single_not_none_var` at its top, not the privilege
	// check below it — runs before the first `next()`. Expect
	// [ErrInvocation] and [ErrPrivilege] from here.

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
