package kathara

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestStatsSamplingIsDeferred(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		update func(context.Context) error
	}{
		{name: "MachineStats", update: (&MachineStats{}).Update},
		{name: "LinkStats", update: (&LinkStats{}).Update},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.update(context.Background())
			if err == nil {
				t.Fatal("Update() = nil, want FeatureNotAvailable")
			}
			if got, want := Code(err), CodeFeatureNotAvailable; got != want {
				t.Errorf("code = %q, want %q", got, want)
			}

			var feature *kerrors.FeatureNotAvailableError
			if !errors.As(err, &feature) {
				t.Fatalf("Update() = %v, want a FeatureNotAvailableError", err)
			}
			if feature.Feature != FeatureStatsSampling {
				t.Errorf("feature = %q, want %q", feature.Feature, FeatureStatsSampling)
			}

			if !errors.Is(err, kerrors.ErrNotSupported) {
				t.Errorf("Update() = %v, want it to wrap ErrNotSupported", err)
			}
			if got, want := err.Error(), "Resource statistics sampling is not supported in this release. Use Kathará 3.8.x."; got != want {
				t.Errorf("message = %q, want %q", got, want)
			}
		})
	}
}

func TestMachineStatsJSONShape(t *testing.T) {
	t.Parallel()

	user, status := "user-abcdefgh", "running"
	docker := MachineStats{
		NetworkScenarioID: "9pe3y6IDMwx4PfOPu5mbNg",
		Name:              "pc1",
		ContainerName:     "kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg",
		User:              &user,
		Status:            &status,
		Image:             "kathara/base",
	}

	got, err := json.Marshal(docker)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"network_scenario_id":"9pe3y6IDMwx4PfOPu5mbNg","name":"pc1",` +
		`"container_name":"kathara_user_pc1_9pe3y6IDMwx4PfOPu5mbNg",` +
		`"user":"user-abcdefgh","status":"running","image":"kathara/base"}`
	if string(got) != want {
		t.Errorf("Docker inventory =\n%s\nwant\n%s", got, want)
	}

	// Docker omits `assigned_node` entirely: `DockerMachineStats.to_dict()`
	// has no such key, and the record above must not grow one.
	if bytes.Contains(got, []byte("assigned_node")) {
		t.Errorf("Docker inventory carries assigned_node:\n%s", got)
	}

	// The Kubernetes shape: no user (its `to_dict()` has none), the pod name
	// in container_name, and assigned_node appended after image.
	k8s := MachineStats{
		NetworkScenarioID: "9pe3y6idmwx4pfopu5mbng",
		Name:              "pc1",
		ContainerName:     "pc1",
		Status:            &status,
		Image:             "kathara/base",
		AssignedNode:      SomeString("worker-1"),
	}
	got, err = json.Marshal(k8s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want = `{"network_scenario_id":"9pe3y6idmwx4pfopu5mbng","name":"pc1","container_name":"pc1",` +
		`"user":null,"status":"running","image":"kathara/base","assigned_node":"worker-1"}`
	if string(got) != want {
		t.Errorf("Kubernetes inventory =\n%s\nwant\n%s", got, want)
	}

	pending := k8s
	pending.Status = nil
	pending.Image = "N/A"
	pending.AssignedNode = NullString()
	got, err = json.Marshal(pending)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want = `{"network_scenario_id":"9pe3y6idmwx4pfopu5mbng","name":"pc1","container_name":"pc1",` +
		`"user":null,"status":null,"image":"N/A","assigned_node":null}`
	if string(got) != want {
		t.Errorf("Pending pod inventory =\n%s\nwant\n%s", got, want)
	}
}

// TestOptionalString is the three states the `assigned_node` key needs — absent
// on Docker, null on an unscheduled Kubernetes pod, a value once it is
// scheduled — and the round trip through JSON that keeps them apart.
func TestOptionalString(t *testing.T) {
	t.Parallel()

	t.Run("states", func(t *testing.T) {
		t.Parallel()

		var absent OptionalString
		if absent.Present() || !absent.IsZero() {
			t.Errorf("the zero OptionalString = %+v, want the absent state", absent)
		}
		if _, ok := absent.Value(); ok {
			t.Error("the absent state produced a value")
		}

		null := NullString()
		if !null.Present() || null.IsZero() {
			t.Error("NullString() is not present")
		}
		if _, ok := null.Value(); ok {
			t.Error("NullString() produced a value")
		}

		some := SomeString("worker-1")
		if !some.Present() || some.IsZero() {
			t.Error("SomeString() is not present")
		}
		if got, ok := some.Value(); !ok || got != "worker-1" {
			t.Errorf("Value() = %q, %v; want \"worker-1\", true", got, ok)
		}
	})

	t.Run("round trip", func(t *testing.T) {
		t.Parallel()

		type holder struct {
			Node OptionalString `json:"assigned_node,omitzero"`
		}

		for _, tt := range []struct {
			name string
			in   OptionalString
			wire string
		}{
			{name: "absent", in: OptionalString{}, wire: `{}`},
			{name: "null", in: NullString(), wire: `{"assigned_node":null}`},
			{name: "value", in: SomeString("worker-1"), wire: `{"assigned_node":"worker-1"}`},
		} {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				encoded, err := json.Marshal(holder{Node: tt.in})
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				if string(encoded) != tt.wire {
					t.Errorf("Marshal = %s, want %s", encoded, tt.wire)
				}

				// The value holds a pointer, so identity is the pair
				// (present, value) rather than ==.
				var decoded holder
				if err := json.Unmarshal([]byte(tt.wire), &decoded); err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				gotValue, gotOK := decoded.Node.Value()
				wantValue, wantOK := tt.in.Value()
				if decoded.Node.Present() != tt.in.Present() || gotOK != wantOK || gotValue != wantValue {
					t.Errorf("round trip = present=%v %q/%v, want present=%v %q/%v",
						decoded.Node.Present(), gotValue, gotOK, tt.in.Present(), wantValue, wantOK)
				}
			})
		}
	})
}

func TestMachineStatsOmitsDeferredFields(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(MachineStats{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"pids", "cpu_usage", "mem_usage", "mem_percent", "net_usage", "interfaces"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("deferred key %q is present", key)
		}
	}
	// The six canonical keys are always there, on both backends.
	for _, key := range []string{"network_scenario_id", "name", "container_name", "user", "status", "image"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("canonical key %q is missing", key)
		}
	}
}

// TestLinkStatsJSONShape keeps the collision-domain record on Python's own
// `to_dict()` keys in `to_dict()` order, with the two backends' disjoint tails
// omitted rather than nulled. Nothing in 1.0 renders it — no CLI command reads
// `get_links_stats` — so this is the shape the API hands an embedder.
func TestLinkStatsJSONShape(t *testing.T) {
	t.Parallel()

	user := "user-abcdefgh"
	ipv6 := false
	docker := LinkStats{
		NetworkScenarioID: "9pe3y6IDMwx4PfOPu5mbNg",
		Name:              "A",
		NetworkName:       "kathara_user_A_9pe3y6IDMwx4PfOPu5mbNg",
		User:              &user,
		IPv6Enabled:       &ipv6,
		Containers:        []string{"pc1", "pc2"},
	}
	got, err := json.Marshal(docker)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `{"network_scenario_id":"9pe3y6IDMwx4PfOPu5mbNg","name":"A",` +
		`"network_name":"kathara_user_A_9pe3y6IDMwx4PfOPu5mbNg","user":"user-abcdefgh",` +
		`"enable_ipv6":false,"containers":["pc1","pc2"]}`
	if string(got) != want {
		t.Errorf("Docker link stats =\n%s\nwant\n%s", got, want)
	}

	vni := 100
	k8s := LinkStats{
		NetworkScenarioID: "9pe3y6idmwx4pfopu5mbng",
		Name:              "A",
		NetworkName:       "a",
		VXLANID:           &vni,
	}
	got, err = json.Marshal(k8s)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want = `{"network_scenario_id":"9pe3y6idmwx4pfopu5mbng","name":"A","network_name":"a",` +
		`"user":null,"vxlan_id":100}`
	if string(got) != want {
		t.Errorf("Kubernetes link stats =\n%s\nwant\n%s", got, want)
	}
}
