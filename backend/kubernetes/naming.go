// This file is the frozen half of the backend (PORT_SPEC §0.4, SYNTHESIS §1.2):
// how a device and a collision domain get their Kubernetes names, which labels
// go on them, and which label selector finds them again. Nothing here talks to
// a cluster.
//
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

// listTimeoutSeconds is the `timeout_seconds=9999` every pod and network
// listing carries (`KubernetesMachine.py:1005`, `KubernetesLink.py:225`;
// k8s-backend.md G21). It is a SERVER-side timeout, unrelated to the context,
// and it is part of the request the goldens see.
const listTimeoutSeconds int64 = 9999

// networkAttachmentAnnotation is Multus' `k8s.v1.cni.cncf.io/networks` pod
// annotation (`KubernetesMachine.py:497`): the JSON array that wires a pod to
// its collision domains, in interface order (ORDERING.tsv, k8s-backend.md O1).
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
//
//	suffix = ''
//	if '_' in name:
//	    suffix = '-%s' % md5(name.encode('utf-8', errors='ignore')).hexdigest()[:8]
//	    name = name.replace('_', '-')
//	full = "%s-%s%s" % (prefix, name, suffix)
//	return re.sub(r'[^0-9a-z\-.]+', '', full.lower())
//
// Four details are load-bearing and each of them is a way to get a different
// name than Python (k8s-backend.md G12):
//
//   - the md5 is over the ORIGINAL name, before the `_`→`-` replacement, so
//     `a_b` and `a-b` do not collide;
//   - the suffix is triggered by an underscore in the NAME only. An underscore
//     in the *prefix* is deleted by the character filter without triggering a
//     hash, which is why `dev_prefix` + `device_name` is
//     `devprefix-device-name-3b92d741` and not `dev-prefix-…`;
//   - the lowercasing happens before the filter, so an uppercase letter is
//     folded rather than deleted (`Device05#A` → `device05a`);
//   - the filter deletes RUNS of disallowed characters and keeps `.` and `-`,
//     because a Kubernetes object name is an RFC 1123 subdomain.
//
// The `errors='ignore'` on the md5 input is CPython dropping unencodable
// surrogates. Go strings hold arbitrary bytes and a Go source string is already
// UTF-8, so the only inputs that differ are ones carrying an invalid encoding,
// which `sanitizeUTF8` drops for the same reason.
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
//
// A Go string literal and everything the parsers produce is already valid
// UTF-8, in which case this is the identity. It exists so that a name carrying
// a stray byte — which `model` does not forbid — hashes to what Python would
// hash it to instead of to the replacement character.
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
//
// Unlike the Docker backend's, this one does not vary with `shared_cds`: on
// Megalos a collision domain is namespaced by the scenario, so the sharing
// modes have nothing to widen.
func NetworkName(netPrefix, linkName string) string {
	return ResourceName(netPrefix, linkName)
}

// ConfigMapName is `KubernetesConfigMap.build_name_for_machine`
// (`KubernetesConfigMap.py:55`): `"%s-%s-files"`.
//
// Its first argument is the DEPLOYMENT name and not the device name — `create`
// passes `machine.meta['real_name']` (`KubernetesConfigMap.py:96`) and
// `_delete_machine` recomputes it with `get_deployment_name`
// (`KubernetesMachine.py:692-693`) — so a device called `test_device` in
// namespace `abc` gets `devprefix-test-device-ec84ad3b-abc-files`. Passing the
// bare device name here would build a ConfigMap the delete path can never find.
func ConfigMapName(deploymentName, namespace string) string {
	return deploymentName + "-" + namespace + "-files"
}

// ObjectSelector is the `",".join(filters)` label selector of
// `get_machines_api_objects_by_filters` (`KubernetesMachine.py:993-996`) and
// `get_links_api_objects_by_filters` (`KubernetesLink.py:209-212`), which are
// the same four lines twice.
//
// The order is fixed and the Python tests pin the exact string: `app=kathara`,
// then `name=<name>` when one was given. "" is exactly as absent as Python's
// None (the test is truthiness).
//
// name labels the object's *Kathará* name — the device name, the
// collision-domain name — never its Kubernetes one.
func ObjectSelector(name string) string {
	if name == "" {
		return labelApp + "=" + labelAppValue
	}
	return labelApp + "=" + labelAppValue + "," + labelName + "=" + name
}

// ObjectLabels is the `{"name": …, "app": "kathara"}` dict that goes on every
// device and every collision domain (`KubernetesMachine.py:500-502`,
// `KubernetesLink.py:289-292`).
//
// The same map is the Deployment's metadata labels, the pod template's labels
// AND the Deployment selector's `matchLabels`, which is why it is built once:
// a selector that does not match its own template is a Deployment that never
// produces a pod.
func ObjectLabels(name string) map[string]string {
	return map[string]string{labelName: name, labelApp: labelAppValue}
}

// namespaceSelector is the `kubernetes.io/metadata.name={hash}` selector
// `KubernetesNamespace` uses to look one namespace up
// (`KubernetesNamespace.py:51,82,96`).
//
// It is a label selector over a LIST rather than a get-by-name, which is what
// makes a missing namespace an empty list instead of a 404 — and what makes
// `get_namespace` return None rather than raise.
func namespaceSelector(labHash string) string {
	return metadataNameLabel + "=" + labHash
}
