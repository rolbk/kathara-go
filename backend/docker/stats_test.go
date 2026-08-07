package docker

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/network"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// TestMachineStatsFor is the inventory reduction of `DockerMachineStats`: the
// six fields JSON_CLI_CONTRACT.md §3.0.2 keeps, read off the container's labels
// and state.
func TestMachineStatsFor(t *testing.T) {
	c := newTestContainer("pc1", nil)

	got := machineStatsFor(c, []string{"kathara/base:latest", "kathara/base:1.0"})

	want := &kathara.MachineStats{
		NetworkScenarioID: fixtureHash,
		Name:              "pc1",
		ContainerName:     "kathara_user_pc1_" + fixtureHash,
		Image:             "kathara/base:latest",
	}
	user, status := "user", "running"
	want.User, want.Status = &user, &status

	if !reflect.DeepEqual(got, want) {
		t.Errorf("machineStatsFor =\n %+v\nwant\n %+v", got, want)
	}
}

// TestMachineStatsAssignedNodeIsAbsent is the one additive backend key of
// JSON_CLI_CONTRACT.md §3.0.2: `DockerMachineStats.to_dict()` has no
// `assigned_node`, so the Docker backend leaves the field at its zero value and
// the key is not emitted at all — which is not the same as emitting null.
func TestMachineStatsAssignedNodeIsAbsent(t *testing.T) {
	got := machineStatsFor(newTestContainer("pc1", nil), nil)
	if got.AssignedNode.Present() {
		t.Error("Docker set assigned_node; only Kubernetes emits that key")
	}
}

// TestMachineStatsUntaggedImage is the total form of
// `machine_api_object.image.tags[0]`, which IndexErrors in Python on an
// untagged image: the image reference stands in, because a listing must not be
// able to fail on one container (PORT_SPEC §10).
func TestMachineStatsUntaggedImage(t *testing.T) {
	got := machineStatsFor(newTestContainer("pc1", nil), nil)
	if got.Image != "sha256:deadbeef" {
		t.Errorf("Image = %q, want the image reference when there is no tag", got.Image)
	}
}

// TestLinkStatsFor is the collision-domain twin, including the `;`-joined
// `external` label (SYNTHESIS C-8 — a semicolon, not a comma) and the attached
// device names.
//
// `Containers` holds DEVICE names — `container.labels['name']`, which is what
// `DockerLinkStats.__str__` prints — not the `{prefix}_{user}_{device}_{hash}`
// container names the endpoint map carries. [Network.AttachedNames] resolves
// them with the same per-container inspect docker-py's `Network.containers`
// property performs.
func TestLinkStatsFor(t *testing.T) {
	n := newTestNetwork("kathara_user_A_h", "A")
	n.Attrs.Labels["lab_hash"] = fixtureHash
	n.Attrs.Labels["user"] = "user"
	n.Attrs.Labels["external"] = "eth0;eth1.20"
	n.Attrs.EnableIPv6 = true
	n.Attrs.Containers = map[string]network.EndpointResource{
		"cid2": {Name: "kathara_user_pc2_h"},
		"cid1": {Name: "kathara_user_pc1_h"},
	}

	got := linkStatsFor(n, []string{"pc1", "pc2"})

	if got.NetworkScenarioID != fixtureHash || got.Name != "A" || got.NetworkName != "kathara_user_A_h" {
		t.Errorf("identity fields = %+v", got)
	}
	if got.User == nil || *got.User != "user" {
		t.Errorf("User = %v", got.User)
	}
	if got.IPv6Enabled == nil || !*got.IPv6Enabled {
		t.Errorf("IPv6Enabled = %v", got.IPv6Enabled)
	}
	if !reflect.DeepEqual(got.External, []string{"eth0", "eth1.20"}) {
		t.Errorf("External = %q, want the semicolon-split label", got.External)
	}
	// ORDERING.tsv:51: Python's dict order is thread-completion order; the
	// port sorts.
	if !reflect.DeepEqual(got.Containers, []string{"pc1", "pc2"}) {
		t.Errorf("Containers = %q, want the sorted device names", got.Containers)
	}
}

// TestLinkStatsSharedModeLabelsAreBenign is the recorded divergence: Python
// indexes `attrs['Labels']['lab_hash']` and `['user']` directly, and
// [NetworkLabels] omits both in the shared modes — so `DockerLinkStats.__init__`
// KeyErrors there. The port answers "" instead, because the crash is latent on
// a path 1.0 does not render and reproducing it would fail an API call for a
// configuration this same backend produced.
func TestLinkStatsSharedModeLabelsAreBenign(t *testing.T) {
	n := newTestNetwork("kathara_A", "A") // no user, no lab_hash: SharedBetweenUsers

	got := linkStatsFor(n, nil)
	if got.NetworkScenarioID != "" {
		t.Errorf("NetworkScenarioID = %q, want the empty string for a shared network", got.NetworkScenarioID)
	}
	if got.User == nil || *got.User != "" {
		t.Errorf("User = %v, want a pointer to the empty string", got.User)
	}
}

// TestLinkStatsEmptyExternalIsNil: the label is always present and always ""
// in 1.0, and an empty label must not become a one-element list holding "".
func TestLinkStatsEmptyExternalIsNil(t *testing.T) {
	if got := linkStatsFor(newTestNetwork("kathara_user_A_h", "A"), nil); got.External != nil {
		t.Errorf("External = %q, want nil for an empty label", got.External)
	}
}

// TestStatsUpdateIsDeferred: live resource sampling is post-1.0 (PORT_SPEC
// §0.3) and a deferred feature must say so rather than quietly do nothing
// (§0.4).
func TestStatsUpdateIsDeferred(t *testing.T) {
	machine := machineStatsFor(newTestContainer("pc1", nil), nil)
	if err := machine.Update(t.Context()); err == nil {
		t.Error("MachineStats.Update succeeded; sampling is deferred")
	}

	link := linkStatsFor(newTestNetwork("kathara_user_A_h", "A"), nil)
	if err := link.Update(t.Context()); err == nil {
		t.Error("LinkStats.Update succeeded; sampling is deferred")
	}
}

// TestNetworkContainerCount is the whole of what the teardown paths read off
// the attached-container map: a collision domain is deleted when and only when
// the count is zero, which is what makes a shared one survive.
func TestNetworkContainerCount(t *testing.T) {
	n := newTestNetwork("kathara_A", "A")
	if n.ContainerCount() != 0 {
		t.Errorf("ContainerCount = %d on a fresh network", n.ContainerCount())
	}
	n.Attrs.Containers = map[string]network.EndpointResource{"cid": {Name: "x"}}
	if n.ContainerCount() != 1 {
		t.Errorf("ContainerCount = %d, want 1", n.ContainerCount())
	}
	if (*Network)(nil).ContainerCount() != 0 {
		t.Error("a nil Network should count zero rather than fault")
	}
}

// TestContainerAccessorsAreNilSafe: the SDK's InspectResponse embeds a POINTER,
// so a zero-value Attrs faults on a naive field read. Every accessor has to
// answer the empty value instead — a listing that raced a removal must not
// crash a fan-out.
func TestContainerAccessorsAreNilSafe(t *testing.T) {
	var empty Container
	if empty.Name() != "" || empty.Status() != "" || empty.HostConfig() != nil {
		t.Error("a zero-value Container did not answer empty values")
	}
	if empty.Label("name") != "" || empty.Networks() != nil {
		t.Error("a zero-value Container did not answer empty labels/networks")
	}

	var nilContainer *Container
	if nilContainer.Name() != "" || nilContainer.Status() != "" || nilContainer.Label("x") != "" {
		t.Error("a nil Container did not answer empty values")
	}
}

// TestContainerNameStripsEveryLeadingSlash is docker-py's
// `attrs['Name'].lstrip('/')`, which strips a RUN of slashes and not one.
func TestContainerNameStripsEveryLeadingSlash(t *testing.T) {
	c := newTestContainer("pc1", nil)
	c.Attrs.Name = "///kathara_user_pc1_h"
	if got := c.Name(); got != "kathara_user_pc1_h" {
		t.Errorf("Name = %q", got)
	}
}

// TestAPIObjectTypeAssertions guard the `Any` on the model's api_object slots:
// nil is "not deployed" and a foreign value is not this backend's.
func TestAPIObjectTypeAssertions(t *testing.T) {
	if _, ok := containerOf(nil); ok {
		t.Error("containerOf(nil) reported a container")
	}
	if _, ok := containerOf((*Container)(nil)); ok {
		t.Error("containerOf of a typed nil reported a container")
	}
	if _, ok := containerOf("not a container"); ok {
		t.Error("containerOf accepted a foreign value")
	}
	if _, ok := containerOf(newTestContainer("pc1", nil)); !ok {
		t.Error("containerOf rejected a real container")
	}

	if _, ok := networkOf(nil); ok {
		t.Error("networkOf(nil) reported a network")
	}
	if _, ok := networkOf(newTestNetwork("n", "A")); !ok {
		t.Error("networkOf rejected a real network")
	}
}
