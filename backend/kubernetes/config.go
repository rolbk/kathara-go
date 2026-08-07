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
//
// Python keeps both in the SDK's process-global default `Configuration`
// (k8s-backend.md G20) and reads them back out of it later. PORT_SPEC §0.2 #10
// forbids that here, so they are a value the [Manager] holds — which also means
// two Managers over two kubeconfigs can coexist, and a test can hand in a
// config without touching anything global.
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
//
// The Python control flow is a bare `except Exception` around
// `config.load_kube_config()`:
//
//  1. a readable kubeconfig wins, whatever it says;
//  2. otherwise `api_server_url` and `api_token` from the settings, both
//     required — either one missing is
//     `ConnectionError("Cannot read Kubernetes configuration.")`
//     ([kerrors.ErrKubeConfigUnreadable]).
//
// Note what step 1 does NOT do, despite its docstring: it never tries the
// in-cluster service-account configuration. `load_kube_config` reads
// `$KUBECONFIG` or `~/.kube/config` and nothing else, so a pod running Megalos
// with no kubeconfig mounted falls through to step 2 exactly like a laptop
// would. Reproduced: `rest.InClusterConfig` is deliberately not consulted.
//
// # Which string is the seed
//
// `get_cluster_user` reads `Configuration.api_key['authorization']` and falls
// back to the current context name on `KeyError`. In the kubeconfig case that
// key is never set — the Python client stores a kubeconfig bearer token under
// `api_key['BearerToken']`, not `['authorization']` (verified against the
// installed client) — so the kubeconfig path ALWAYS seeds with the context
// name. Only step 2, which assigns `api_key['authorization'] = token` itself,
// seeds with the token. Both branches are spelled out below.
//
// A kubeconfig that loads but names no current context yields the empty string,
// which is what Python's `active_context['name']` would be for the same file;
// the VNIs are then the ones an empty seed produces, and nothing errors.
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

// stringOrEmpty renders a nullable setting the way Python's `if not api_url:`
// reads it: nil and "" are the same thing (NILABILITY.tsv:50-51).
func stringOrEmpty(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
