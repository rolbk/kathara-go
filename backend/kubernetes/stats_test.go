package kubernetes

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/KatharaFramework/kathara-go/kathara"
)

// TestMachineStatsFor is the inventory `KubernetesMachineStats` carries, in the
// shape JSON_CLI_CONTRACT.md §3.0.2 pins — including the three fields that
// differ from Docker's: `container_name` is the POD name, `user` is always
// null, and `assigned_node` is present-and-possibly-null rather than absent.
func TestMachineStatsFor(t *testing.T) {
	hash := strings.ToLower(defaultScenarioHash)

	pod := newTestPod(hash, "pc1")
	pod.Status.ContainerStatuses = []corev1.ContainerStatus{{Image: "kathara/test:latest", Ready: true}}

	stats := machineStatsFor(pod)
	if stats.NetworkScenarioID != hash {
		t.Errorf("network_scenario_id = %q, want %q", stats.NetworkScenarioID, hash)
	}
	if stats.Name != "pc1" {
		t.Errorf("name = %q, want pc1", stats.Name)
	}
	if stats.ContainerName != pod.Name {
		t.Errorf("container_name = %q, want the pod name %q", stats.ContainerName, pod.Name)
	}
	if stats.User != nil {
		t.Errorf("user = %v, want nil — Kubernetes records no deploying user", stats.User)
	}
	if stats.Image != "kathara/test:latest" {
		t.Errorf("image = %q", stats.Image)
	}
	if value, ok := stats.AssignedNode.Value(); !ok || value != "node-1" {
		t.Errorf("assigned_node = %v, want node-1", value)
	}

	// The wire shape: the key order is the struct's, and `assigned_node` is the
	// one additive backend key.
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `{"network_scenario_id":"` + hash + `","name":"pc1","container_name":"` + pod.Name +
		`","user":null,"status":"Running","image":"kathara/test:latest","assigned_node":"node-1"}`
	if string(raw) != want {
		t.Errorf("json =\n %s\nwant\n %s", raw, want)
	}
}

// TestMachineStatsUnscheduledPod pins the NULL half of `assigned_node`: a pod
// that has not been scheduled has no `spec.node_name`, and the contract says the
// key is present and "(string|null)" there.
func TestMachineStatsUnscheduledPod(t *testing.T) {
	pod := newTestPod(defaultScenarioHash, "pc1")
	pod.Spec.NodeName = ""
	pod.Status.Phase = corev1.PodPending

	stats := machineStatsFor(pod)
	if !stats.AssignedNode.Present() {
		t.Error("assigned_node is absent, want present-and-null")
	}
	if _, ok := stats.AssignedNode.Value(); ok {
		t.Error("assigned_node carries a value, want null")
	}

	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"assigned_node":null`) {
		t.Errorf("json = %s, want assigned_node:null", raw)
	}
}

// TestMachineStatsImageFallback is `container_statuses[0].image if
// container_statuses else "N/A"` — the literal string, not an empty one.
func TestMachineStatsImageFallback(t *testing.T) {
	pod := newTestPod(defaultScenarioHash, "pc1")
	if got := machineStatsFor(pod).Image; got != "N/A" {
		t.Errorf("image = %q, want N/A", got)
	}
}

// TestDetailedMachineStatus is `_get_detailed_machine_status`
// (`stats/KubernetesMachineStats.py:56`), whose ladder ends by truncating a
// reason at its first `": "`.
func TestDetailedMachineStatus(t *testing.T) {
	tests := []struct {
		name  string
		phase corev1.PodPhase
		state *corev1.ContainerState
		want  string
	}{
		{
			name:  "no container statuses is the phase",
			phase: corev1.PodPending,
			want:  "Pending",
		},
		{
			name:  "terminated with a reason",
			phase: corev1.PodFailed,
			state: &corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error"}},
			want:  "Error",
		},
		{
			name:  "terminated without a reason is the literal Terminating",
			phase: corev1.PodFailed,
			state: &corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{}},
			want:  "Terminating",
		},
		{
			name:  "waiting with a reason",
			phase: corev1.PodPending,
			state: &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			want:  "CrashLoopBackOff",
		},
		{
			name:  "waiting without a reason falls back to the phase",
			phase: corev1.PodPending,
			state: &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{}},
			want:  "Pending",
		},
		{
			name:  "an error message is truncated at the first colon-space",
			phase: corev1.PodPending,
			state: &corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "CreateContainerConfigError: secret private-registry not found",
			}},
			want: "CreateContainerConfigError",
		},
		{
			name:  "a running container falls back to the phase",
			phase: corev1.PodRunning,
			state: &corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			want:  "Running",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pod := &corev1.Pod{Status: corev1.PodStatus{Phase: test.phase}}
			if test.state != nil {
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{{State: *test.state}}
			}
			if got := detailedMachineStatus(pod); got != test.want {
				t.Errorf("status = %q, want %q", got, test.want)
			}
		})
	}
}

// TestLinkStatsFor is `KubernetesLinkStats`: the VNI parsed out of the embedded
// CNI config, and the `user` key that is always null here (DIVERGENCES.md #53).
func TestLinkStatsFor(t *testing.T) {
	network := newTestNetwork(defaultScenarioHash, "A", 1362434)

	stats := linkStatsFor(network)
	if stats.NetworkScenarioID != defaultScenarioHash {
		t.Errorf("network_scenario_id = %q", stats.NetworkScenarioID)
	}
	if stats.Name != "A" {
		t.Errorf("name = %q, want the collision-domain label", stats.Name)
	}
	if stats.NetworkName != "netprefix-a" {
		t.Errorf("network_name = %q", stats.NetworkName)
	}
	if stats.User != nil {
		t.Errorf("user = %v, want nil", stats.User)
	}
	if stats.VXLANID == nil || *stats.VXLANID != 1362434 {
		t.Errorf("vxlan_id = %v, want 1362434", stats.VXLANID)
	}
	if stats.IPv6Enabled != nil || stats.Containers != nil {
		t.Error("Docker-only fields should stay nil on Megalos")
	}
}

// TestMachinesStatsStream is the inventory stream: sorted by pod name
// (ORDERING.tsv row 79, where Python's dict is in thread-completion order), and
// an EMPTY result is an empty batch rather than the end of the stream
// (NILABILITY.tsv:64).
func TestMachinesStatsStream(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, _ := newTestManager(t, s,
		newTestPod(hash, "pc2"),
		newTestPod(hash, "pc1"),
	)

	stream, err := m.GetMachinesStats(context.Background(), kathara.LabRef{Hash: hash}, "", false)
	if err != nil {
		t.Fatalf("GetMachinesStats: %v", err)
	}
	defer func() { _ = stream.Close() }()

	entries, err := stream.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	if entries[0].ID >= entries[1].ID {
		t.Errorf("entries are not sorted by id: %q, %q", entries[0].ID, entries[1].ID)
	}
	// The dict key is the POD name (`machines_stats[pod.metadata.name]`).
	if entries[0].ID != entries[0].Stats.ContainerName {
		t.Errorf("entry id %q does not match container_name %q", entries[0].ID, entries[0].Stats.ContainerName)
	}

	// A second step re-queries and is not the end.
	if _, err := stream.Next(context.Background()); err != nil {
		t.Fatalf("second Next: %v", err)
	}
}

// TestMachinesStatsStreamEmpty is EXPECTATIONS-k8s §1
// `test_get_machines_stats_lab_hash_device_not_found`: no match yields an empty
// batch, NOT an error and NOT the end.
func TestMachinesStatsStreamEmpty(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)

	stream, err := m.GetMachinesStats(context.Background(), kathara.LabRef{Hash: defaultScenarioHash}, "", false)
	if err != nil {
		t.Fatalf("GetMachinesStats: %v", err)
	}
	entries, err := stream.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("%d entries, want 0", len(entries))
	}
}

// TestMachineStatsStreamSingle is the singular generator: one element, then
// io.EOF — and a nil element with a nil error when the device is not found,
// which is Python's `yield None`.
func TestMachineStatsStreamSingle(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)

	t.Run("found", func(t *testing.T) {
		m, _, _, _ := newTestManager(t, s, newTestPod(hash, "pc1"))
		stream := m.GetMachineStats(context.Background(), "pc1", kathara.LabRef{Hash: hash}, false)

		stats, err := stream.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if stats == nil || stats.Name != "pc1" {
			t.Fatalf("stats = %v, want pc1", stats)
		}
		if _, err := stream.Next(context.Background()); !errors.Is(err, io.EOF) {
			t.Errorf("second Next = %v, want io.EOF", err)
		}
	})

	t.Run("not found yields nil", func(t *testing.T) {
		m, _, _, _ := newTestManager(t, s)
		stream := m.GetMachineStats(context.Background(), "pc9", kathara.LabRef{Hash: hash}, false)

		stats, err := stream.Next(context.Background())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if stats != nil {
			t.Errorf("stats = %v, want nil", stats)
		}
		if _, err := stream.Next(context.Background()); !errors.Is(err, io.EOF) {
			t.Errorf("second Next = %v, want io.EOF", err)
		}
	})
}

// TestLinksStatsStream is the collision-domain twin, keyed and sorted by the
// Kubernetes network name.
func TestLinksStatsStream(t *testing.T) {
	s := testSettings()
	hash := strings.ToLower(defaultScenarioHash)
	m, _, _, _ := newTestManager(t, s)
	m.link.dynamic = newFakeDynamic(newTestNetwork(hash, "B", 2), newTestNetwork(hash, "A", 1))

	stream, err := m.GetLinksStats(context.Background(), kathara.LabRef{Hash: hash}, "", false)
	if err != nil {
		t.Fatalf("GetLinksStats: %v", err)
	}
	defer func() { _ = stream.Close() }()

	entries, err := stream.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d entries, want 2", len(entries))
	}
	if entries[0].ID != "netprefix-a" || entries[1].ID != "netprefix-b" {
		t.Errorf("entries = %q, %q; want sorted network names", entries[0].ID, entries[1].ID)
	}

	single := m.GetLinkStats(context.Background(), "A", kathara.LabRef{Hash: hash}, false)
	stats, err := single.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if stats == nil || stats.NetworkName != "netprefix-a" {
		t.Fatalf("stats = %v", stats)
	}
	if _, err := single.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Errorf("second Next = %v, want io.EOF", err)
	}
}

// TestStatsUpdateIsDeferred pins PORT_SPEC §0.3/§0.4: re-sampling is post-1.0
// and says so rather than quietly doing nothing.
func TestStatsUpdateIsDeferred(t *testing.T) {
	machine := machineStatsFor(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{}}})
	if err := machine.Update(context.Background()); err == nil {
		t.Error("MachineStats.Update should report the deferral")
	}

	link := linkStatsFor(newTestNetwork(defaultScenarioHash, "A", 1))
	if err := link.Update(context.Background()); err == nil {
		t.Error("LinkStats.Update should report the deferral")
	}
}
