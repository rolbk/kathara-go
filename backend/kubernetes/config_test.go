package kubernetes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// kubeconfigYAML is a minimal readable kubeconfig, whose CURRENT CONTEXT name
// is what `get_cluster_user` falls back to and therefore what seeds every VNI.
const kubeconfigYAML = `apiVersion: v1
kind: Config
current-context: kathara-context
clusters:
- name: kathara-cluster
  cluster:
    server: https://cluster.example:6443
    insecure-skip-tls-verify: true
contexts:
- name: kathara-context
  context:
    cluster: kathara-cluster
    user: kathara-user
users:
- name: kathara-user
  user:
    token: kubeconfig-token
`

// TestLoadKubeConfigFromKubeconfig is `KubernetesConfig.load_kube_config`'s
// first branch fused with `get_cluster_user`'s fallback.
func TestLoadKubeConfigFromKubeconfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(kubeconfigYAML), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", path)

	// The settings name a remote cluster too; the kubeconfig must still win.
	s := testSettings()
	s.APIServerURL = ptr("https://settings.example:6443")
	s.APIToken = ptr("settings-token")

	cluster, err := LoadKubeConfig(s)
	if err != nil {
		t.Fatalf("LoadKubeConfig: %v", err)
	}
	if cluster.Rest.Host != "https://cluster.example:6443" {
		t.Errorf("host = %q, want the kubeconfig's", cluster.Rest.Host)
	}
	if cluster.User != "kathara-context" {
		t.Errorf("seed = %q, want the current context name", cluster.User)
	}
}

// TestLoadKubeConfigFromSettings is the `except Exception` branch: no readable
// kubeconfig, so the Megalos settings are used and the seed is the TOKEN —
// because that branch assigns `api_key['authorization']` itself.
func TestLoadKubeConfigFromSettings(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))

	s := testSettings()
	s.APIServerURL = ptr("https://settings.example:6443")
	s.APIToken = ptr("settings-token")

	cluster, err := LoadKubeConfig(s)
	if err != nil {
		t.Fatalf("LoadKubeConfig: %v", err)
	}
	if cluster.Rest.Host != "https://settings.example:6443" {
		t.Errorf("host = %q", cluster.Rest.Host)
	}
	if cluster.Rest.BearerToken != "settings-token" {
		t.Errorf("token = %q", cluster.Rest.BearerToken)
	}
	if cluster.User != "settings-token" {
		t.Errorf("seed = %q, want the token", cluster.User)
	}
}

func TestLoadKubeConfigUnreadable(t *testing.T) {
	tests := []struct {
		name  string
		url   *string
		token *string
	}{
		{name: "neither"},
		{name: "url only", url: ptr("https://settings.example:6443")},
		{name: "token only", token: ptr("settings-token")},
		{name: "empty url", url: ptr(""), token: ptr("settings-token")},
		{name: "empty token", url: ptr("https://settings.example:6443"), token: ptr("")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))

			s := testSettings()
			s.APIServerURL = test.url
			s.APIToken = test.token

			_, err := LoadKubeConfig(s)
			if !errors.Is(err, kerrors.ErrConnection) {
				t.Fatalf("error = %v, want a ConnectionError", err)
			}
			if err.Error() != "Cannot read Kubernetes configuration." {
				t.Errorf("message = %q", err.Error())
			}
		})
	}
}

// TestNewReportsAnUnreadableConfiguration pins the one thing the constructor
// checks: unlike the Docker backend there is no ping and no version read, so a
// missing configuration is all [New] can fail on.
func TestNewReportsAnUnreadableConfiguration(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))

	s := &settings.Settings{}
	_, err := New(t.Context(), testConfig(s))
	if !errors.Is(err, kerrors.ErrConnection) {
		t.Fatalf("error = %v, want a ConnectionError", err)
	}
}

// TestStringOrEmpty is the nullable-settings reading `if not api_url:` does.
func TestStringOrEmpty(t *testing.T) {
	if got := stringOrEmpty(nil); got != "" {
		t.Errorf("nil = %q, want \"\"", got)
	}
	if got := stringOrEmpty(ptr("x")); got != "x" {
		t.Errorf("value = %q, want x", got)
	}
}
