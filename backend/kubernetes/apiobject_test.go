package kubernetes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/KatharaFramework/kathara-go/model"
)

// TestEncodeNetworkAttachments is `json.dumps(network_interfaces)` with
// CPython's defaults — `", "`/`": "` separators and `ensure_ascii=True` — which
// Go's own encoder does not produce and which is stored verbatim in the pod
// annotation.
func TestEncodeNetworkAttachments(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "annotations.json"))
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}

	var vectors struct {
		Cases []struct {
			Entries []struct {
				Name        string `json:"name"`
				Namespace   string `json:"namespace"`
				Interface   string `json:"interface"`
				KatharaLink string `json:"kathara.link"`
				MAC         string `json:"mac"`
			} `json:"entries"`
			Output string `json:"output"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatalf("decode vectors: %v", err)
	}

	for i, vector := range vectors.Cases {
		attachments := make([]podNetworkAttachment, 0, len(vector.Entries))
		for _, entry := range vector.Entries {
			attachments = append(attachments, podNetworkAttachment{
				Name:        entry.Name,
				Namespace:   entry.Namespace,
				Interface:   entry.Interface,
				KatharaLink: entry.KatharaLink,
				MAC:         entry.MAC,
			})
		}
		if got := encodeNetworkAttachments(attachments); got != vector.Output {
			t.Errorf("case %d:\n got %q\nwant %q", i, got, vector.Output)
		}
	}
}

// TestNetworkDefinitionGolden is EXPECTATIONS-k8s §2 "_build_definition →
// NetworkAttachmentDefinition", including the `spec.config` string whose source
// indentation is part of the stored value (k8s-backend.md G24).
func TestNetworkDefinitionGolden(t *testing.T) {
	tests := []struct {
		name      string
		golden    string
		linkName  string
		networkID int
	}{
		{name: "plain collision domain", golden: "nad.json", linkName: "A", networkID: 1},
		{name: "underscore in the name", golden: "nad_underscore.json", linkName: "A_B", networkID: 1362434},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			definition := NetworkDefinition(
				NetworkName("netprefix", test.linkName), test.linkName, defaultScenarioHash, test.networkID)

			raw, err := os.ReadFile(filepath.Join("testdata", test.golden))
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			var expected map[string]any
			if err := json.Unmarshal(raw, &expected); err != nil {
				t.Fatalf("decode golden: %v", err)
			}

			actualRaw, err := json.Marshal(definition.Object)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var actual map[string]any
			if err := json.Unmarshal(actualRaw, &actual); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			if got, want := mustMarshalIndent(t, actual), mustMarshalIndent(t, expected); got != want {
				t.Errorf("NAD does not match testdata/%s\n--- got ---\n%s\n--- want ---\n%s", test.golden, got, want)
			}
		})
	}
}

// TestNetworkConfigAccessors covers the reads `_get_existing_network_ids` and
// `KubernetesLinkStats` perform on the embedded CNI configuration, and what
// happens when it is not there.
func TestNetworkConfigAccessors(t *testing.T) {
	t.Run("well-formed", func(t *testing.T) {
		network := NetworkDefinition("netprefix-a", "A", defaultScenarioHash, 1362434)
		id, ok := NetworkVXLANID(network)
		if !ok || id != 1362434 {
			t.Fatalf("vxlan id = %d (ok=%v), want 1362434", id, ok)
		}
		config, err := NetworkConfig(network)
		if err != nil {
			t.Fatalf("NetworkConfig: %v", err)
		}
		if config["name"] != "a" || config["type"] != "megalos" {
			t.Errorf("config = %v, want name=a type=megalos", config)
		}
		if config["suffix"] != defaultScenarioHash[:6] {
			t.Errorf("suffix = %v, want %q", config["suffix"], defaultScenarioHash[:6])
		}
	})

	t.Run("no spec at all", func(t *testing.T) {
		network := &Network{Object: map[string]any{"metadata": map[string]any{"name": "x"}}}
		if _, err := NetworkConfig(network); !errors.Is(err, model.ErrPyKeyError) {
			t.Fatalf("error = %v, want a KeyError", err)
		}
		if _, ok := NetworkVXLANID(network); ok {
			t.Error("a NAD without a spec should not report a VNI")
		}
	})

	t.Run("malformed config JSON", func(t *testing.T) {
		network := &Network{Object: map[string]any{"spec": map[string]any{"config": "{not json"}}}
		if _, err := NetworkConfig(network); err == nil {
			t.Fatal("want a decode error")
		}
		if _, ok := NetworkVXLANID(network); ok {
			t.Error("a NAD with a malformed config should not report a VNI")
		}
	})

	// `int(network_config['vxlanId'])` (`KubernetesLink.py:350`) parses a string
	// like any other `int()` call, so a foreign NAD that spells its VNI as a
	// string still reserves it — dropping it would let the allocator hand the
	// same id to a collision domain of this scenario.
	t.Run("a numeric string vxlanId is a VNI", func(t *testing.T) {
		network := &Network{Object: map[string]any{"spec": map[string]any{"config": `{"vxlanId": " +5 "}`}}}
		id, ok := NetworkVXLANID(network)
		if !ok || id != 5 {
			t.Errorf("vxlan id = %d (ok=%v), want 5", id, ok)
		}
	})

	t.Run("a string int() would refuse is not a VNI", func(t *testing.T) {
		for _, raw := range []string{`"5.5"`, `"five"`, `""`, `true`, `null`} {
			network := &Network{Object: map[string]any{
				"spec": map[string]any{"config": `{"vxlanId": ` + raw + `}`},
			}}
			if _, ok := NetworkVXLANID(network); ok {
				t.Errorf("vxlanId %s reported a VNI", raw)
			}
		}
	})
}

// TestEnvVarValueFromPod is `get_env_var_value_from_pod`, including the three
// shapes that answer "" — no containers, no env list, and a name that is not
// there — and the one property that deliberately does NOT survive: Python's
// `containers.pop()` empties the pod, so a second call answers None
// (k8s-backend.md G7). Here the second call answers the same value.
func TestEnvVarValueFromPod(t *testing.T) {
	pod := &corev1.Pod{Spec: corev1.PodSpec{Containers: []corev1.Container{{
		Env: []corev1.EnvVar{{Name: megalosShellEnv, Value: "/bin/zsh"}},
	}}}}

	if got := EnvVarValueFromPod(pod, megalosShellEnv); got != "/bin/zsh" {
		t.Errorf("first read = %q, want /bin/zsh", got)
	}
	if got := EnvVarValueFromPod(pod, megalosShellEnv); got != "/bin/zsh" {
		t.Errorf("second read = %q, want /bin/zsh (Python's pop() would answer \"\")", got)
	}
	if got := EnvVarValueFromPod(pod, "OTHER"); got != "" {
		t.Errorf("missing variable = %q, want \"\"", got)
	}
	if got := EnvVarValueFromPod(&corev1.Pod{}, megalosShellEnv); got != "" {
		t.Errorf("no containers = %q, want \"\"", got)
	}
	if got := EnvVarValueFromPod(nil, megalosShellEnv); got != "" {
		t.Errorf("nil pod = %q, want \"\"", got)
	}
}

// TestPodNetworkAttachments is the read side of the annotation: a pod without
// one is Python's KeyError, which the teardown paths must not swallow.
func TestPodNetworkAttachments(t *testing.T) {
	t.Run("round trip", func(t *testing.T) {
		want := []podNetworkAttachment{
			{Name: "netprefix-a", Namespace: "h", Interface: "net0", KatharaLink: "A"},
			{Name: "netprefix-b", Namespace: "h", Interface: "net1", KatharaLink: "B", MAC: "00:11:22:33:44:55"},
		}
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{
			networkAttachmentAnnotation: encodeNetworkAttachments(want),
		}}}

		got, err := podNetworkAttachments(pod)
		if err != nil {
			t.Fatalf("podNetworkAttachments: %v", err)
		}
		if len(got) != 2 || got[0].Name != "netprefix-a" || got[1].MAC != "00:11:22:33:44:55" {
			t.Errorf("got %+v, want %+v", got, want)
		}
	})

	t.Run("no annotation is a KeyError", func(t *testing.T) {
		_, err := podNetworkAttachments(&corev1.Pod{})
		if !errors.Is(err, model.ErrPyKeyError) {
			t.Fatalf("error = %v, want a KeyError", err)
		}
	})
}

// TestListOptions pins the `timeout_seconds=9999` every pod and network listing
// carries (k8s-backend.md G21).
func TestListOptions(t *testing.T) {
	options := listOptions(ObjectSelector("pc1"))
	if options.LabelSelector != "app=kathara,name=pc1" {
		t.Errorf("selector = %q", options.LabelSelector)
	}
	if options.TimeoutSeconds == nil || *options.TimeoutSeconds != 9999 {
		t.Errorf("timeout = %v, want 9999", options.TimeoutSeconds)
	}
}
