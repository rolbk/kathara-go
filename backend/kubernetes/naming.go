// `KubernetesMachine.get_deployment_name` (:1054),
// `KubernetesLink.get_network_name` (:354),
// `KubernetesConfigMap.build_name_for_machine` (:55), and the two
// `*_by_filters` selector builders (`KubernetesMachine.py:993-996`,
// `KubernetesLink.py:209-212`).

package kubernetes

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
	"unicode/utf8"
)

// The label keys of the frozen schema, and the constant every Kathará object
// carries. They are spelled once so that the builders and the selectors cannot
// drift apart.
const (
	labelApp  = "app"
	labelName = "name"

	// labelAppValue is what every listing filters on first.
	labelAppValue = "kathara"

	// metadataNameLabel is the label the API server puts on every namespace
	// (`kubernetes.io/metadata.name`), which `KubernetesNamespace` selects a
	// single namespace by rather than getting it by name
	// (`KubernetesNamespace.py:51,82,96`).
	metadataNameLabel = "kubernetes.io/metadata.name"
)

const listTimeoutSeconds int64 = 9999

const networkAttachmentAnnotation = "k8s.v1.cni.cncf.io/networks"

// megalosShellEnv is `_MEGALOS_SHELL` (`KubernetesMachine.py:458`), the
// environment variable that records which shell the device was deployed with —
// Megalos' equivalent of the Docker backend's `shell` label. `connect` and
// `_delete_machine` read it back off the pod.
const megalosShellEnv = "_MEGALOS_SHELL"

// privateRegistrySecretName is the name of the `kubernetes.io/dockerconfigjson`
// Secret (`KubernetesSecret.py:33`) and of the `imagePullSecrets` entry that
// references it (`KubernetesMachine.py:545`). The two must agree or every pod
// of a private-registry scenario fails to pull.
const privateRegistrySecretName = "private-registry"

// ResourceName is the shared body of `get_deployment_name`
// (`KubernetesMachine.py:1054`) and `get_network_name` (`KubernetesLink.py:354`),
// which are the same eight lines twice:
func ResourceName(prefix, name string) string {
	suffix := ""
	if strings.Contains(name, "_") {
		sum := md5.Sum(sanitizeUTF8(name))
		suffix = "-" + hex.EncodeToString(sum[:])[:8]
		name = strings.ReplaceAll(name, "_", "-")
	}

	full := strings.ToLower(prefix + "-" + name + suffix)

	var b strings.Builder
	b.Grow(len(full))
	for _, r := range full {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || r == '-' || r == '.' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sanitizeUTF8 is `s.encode('utf-8', errors='ignore')`: the bytes of s with
// every invalid encoding dropped.
func sanitizeUTF8(s string) []byte {
	if utf8.ValidString(s) {
		return []byte(s)
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			i++
			continue
		}
		out = append(out, s[i:i+size]...)
		i += size
	}
	return out
}

// DeploymentName is `KubernetesMachine.get_deployment_name`
// (`KubernetesMachine.py:1054`): the name of the Deployment, of its single
// container and of the pod's hostname, all three the same string.
func DeploymentName(devicePrefix, machineName string) string {
	return ResourceName(devicePrefix, machineName)
}

// NetworkName is `KubernetesLink.get_network_name` (`KubernetesLink.py:354`):
// the name of the NetworkAttachmentDefinition.
func NetworkName(netPrefix, linkName string) string {
	return ResourceName(netPrefix, linkName)
}

// ConfigMapName is `KubernetesConfigMap.build_name_for_machine`
// (`KubernetesConfigMap.py:55`): `"%s-%s-files"`.
func ConfigMapName(deploymentName, namespace string) string {
	return deploymentName + "-" + namespace + "-files"
}

// ObjectSelector is the `",".join(filters)` label selector of
// `get_machines_api_objects_by_filters` (`KubernetesMachine.py:993-996`) and
// `get_links_api_objects_by_filters` (`KubernetesLink.py:209-212`), which are
// the same four lines twice.
func ObjectSelector(name string) string {
	if name == "" {
		return labelApp + "=" + labelAppValue
	}
	return labelApp + "=" + labelAppValue + "," + labelName + "=" + name
}

// ObjectLabels is the `{"name": …, "app": "kathara"}` dict that goes on every
// device and every collision domain (`KubernetesMachine.py:500-502`,
// `KubernetesLink.py:289-292`).
func ObjectLabels(name string) map[string]string {
	return map[string]string{labelName: name, labelApp: labelAppValue}
}

// namespaceSelector is the `kubernetes.io/metadata.name={hash}` selector
// `KubernetesNamespace` uses to look one namespace up
// (`KubernetesNamespace.py:51,82,96`).
func namespaceSelector(labHash string) string {
	return metadataNameLabel + "=" + labHash
}
