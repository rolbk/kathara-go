// This file is `KubernetesConfig.py`: where the cluster credentials come from,
// and the one string derived from them that reaches an object the cluster
// stores — the VNI seed.

package kubernetes

import (
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/settings"
)

// ClusterConfig is what `KubernetesConfig.load_kube_config` leaves behind: the
// connection, plus the `get_cluster_user` value that seeds every VXLAN VNI.
type ClusterConfig struct {
	// Rest is the connection: host, credentials, TLS.
	Rest *rest.Config

	// User is `KubernetesConfig.get_cluster_user()`
	// (`KubernetesConfig.py:9`), which `KubernetesLink.__init__` stores as
	// `self.seed` and prepends to every collision-domain name before hashing
	// (`KubernetesLink.py:337`). It therefore decides which VNI a collision
	// domain gets, and two users of the same cluster deliberately get
	// different ones.
	User string
}

// LoadKubeConfig is `KubernetesConfig.load_kube_config`
// (`KubernetesConfig.py:24`) fused with `get_cluster_user`
// (`KubernetesConfig.py:8`), because the two questions have one answer and
// Python only separates them by routing through a global.
func LoadKubeConfig(s *settings.Settings) (*ClusterConfig, error) {
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	)

	if restConfig, err := loader.ClientConfig(); err == nil {
		contextName := ""
		if raw, rawErr := loader.RawConfig(); rawErr == nil {
			contextName = raw.CurrentContext
		}
		return &ClusterConfig{Rest: restConfig, User: contextName}, nil
	}

	apiURL, token := stringOrEmpty(s.APIServerURL), stringOrEmpty(s.APIToken)
	if apiURL == "" || token == "" {
		return nil, kerrors.ErrKubeConfigUnreadable
	}

	return &ClusterConfig{
		Rest: &rest.Config{Host: apiURL, BearerToken: token},
		User: token,
	}, nil
}

func stringOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
