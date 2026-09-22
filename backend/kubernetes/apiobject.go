//   - `link.api_object` is a plain dict from `create_namespaced_custom_object`,
//     indexed `["metadata"]["name"]`. Here it is an
//     [unstructured.Unstructured], which is the same thing with a type.
//   - `machine.api_object` is a `V1Deployment` right after `create` and a
//     `V1Pod` when it comes out of a getter. Every consumer reads only
//     `metadata.labels["name"]` and `metadata.namespace`, which both carry, so
//     the accessors below take the narrowest interface that has them.
//   - the pod's `spec.containers[0].env` is where the device's shell is
//     recorded, including the established accessor edge case.

package kubernetes

import (
	"encoding/json"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// The custom-resource coordinates of a Multus NetworkAttachmentDefinition
// (`KubernetesLink.py:26-28`): `K8S_NET_GROUP`, `K8S_NET_VERSION`,
// `K8S_NET_PLURAL`.
const (
	netGroup   = "k8s.cni.cncf.io"
	netVersion = "v1"
	netPlural  = "network-attachment-definitions"
	netKind    = "NetworkAttachmentDefinition"
)

// netGVR is the group/version/resource the dynamic client addresses NADs by. It
// is the typed form of the three keyword arguments every `*_namespaced_custom_object`
// call passes.
var netGVR = schema.GroupVersionResource{Group: netGroup, Version: netVersion, Resource: netPlural}

// Network is `link.api_object`: the NetworkAttachmentDefinition, as the
// untyped object Python's `CustomObjectsApi` hands back.
type Network = unstructured.Unstructured

// NetworkDefinition is `KubernetesLink._build_definition`
// (`KubernetesLink.py:274`): the object submitted to create a collision domain.
func NetworkDefinition(networkName, linkName, labHash string, networkID int) *Network {
	return &Network{Object: map[string]any{
		"apiVersion": netGroup + "/" + netVersion,
		"kind":       netKind,
		"metadata": map[string]any{
			"name": networkName,
			"labels": map[string]any{
				labelName: linkName,
				labelApp:  labelAppValue,
			},
		},
		"spec": map[string]any{
			"config": networkConfigJSON(linkName, labHash, networkID),
		},
	}}
}

// networkConfigJSON is the `spec.config` string of `_build_definition`
// (`KubernetesLink.py:295-302`).
func networkConfigJSON(linkName, labHash string, networkID int) string {
	suffix := labHash
	if len(suffix) > 6 {
		suffix = suffix[:6]
	}

	const indent = "                            "
	const closing = "                        "

	return "{\n" +
		indent + `"cniVersion": "0.3.0",` + "\n" +
		indent + `"name": "` + strings.ToLower(linkName) + `",` + "\n" +
		indent + `"type": "megalos",` + "\n" +
		indent + `"suffix": "` + suffix + `",` + "\n" +
		indent + `"vxlanId": ` + strconv.Itoa(networkID) + "\n" +
		closing + "}"
}

func NetworkNameOf(n *Network) string {
	if n == nil {
		return ""
	}
	return n.GetName()
}

// NetworkNamespace reads `network["metadata"]["namespace"]`, which
// `_undeploy_link` deletes in (`KubernetesLink.py:182`). It is set by the API
// server on create and is absent from a definition this package built but has
// not submitted.
func NetworkNamespace(n *Network) string {
	if n == nil {
		return ""
	}
	return n.GetNamespace()
}

// NetworkLinkName reads `network['metadata']['labels']['name']`: the collision
// domain's Kathará name, which `undeploy_lab(selected_links=…)` matches
// (`KubernetesManager.py:330`) and which the reconstruction names the
// [model.Link] after (`:727`).
func NetworkLinkName(n *Network) string {
	if n == nil {
		return ""
	}
	return n.GetLabels()[labelName]
}

// NetworkConfig is `json.loads(network['spec']['config'])`, the parsed CNI
// configuration `_get_existing_network_ids` and `KubernetesLinkStats` read the
// VNI out of (`KubernetesLink.py:349`, `stats/KubernetesLinkStats.py:24`).
func NetworkConfig(n *Network) (map[string]any, error) {
	if n == nil {
		return nil, newPySubscriptError()
	}
	raw, found, err := unstructured.NestedString(n.Object, "spec", "config")
	if err != nil || !found {
		return nil, newPyKeyError("config")
	}

	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		return nil, err
	}
	return config, nil
}

// NetworkVXLANID is `int(json.loads(network['spec']['config'])['vxlanId'])`
// (`KubernetesLink.py:350`).
func NetworkVXLANID(n *Network) (int, bool) {
	config, err := NetworkConfig(n)
	if err != nil {
		return 0, false
	}
	switch v := config["vxlanId"].(type) {
	case float64:
		return int(v), true
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, false
		}
		return int(f), true
	case string:
		id, err := util.PyInt(v)
		if err != nil {
			return 0, false
		}
		return id, true
	default:
		return 0, false
	}
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// machineObject is the narrow view of `machine.api_object` every consumer
// actually uses: `metadata.labels["name"]` and `metadata.namespace`
// (`KubernetesMachine.py:933-934,959-960`).
type machineObject interface {
	GetName() string
	GetNamespace() string
	GetLabels() map[string]string
}

// machineObjectOf is the type assertion behind `machine.api_object`. The second
// result is false for a device that was never deployed (nil) and for one
// deployed by the other backend.
func machineObjectOf(v any) (machineObject, bool) {
	obj, ok := v.(machineObject)
	return obj, ok && obj != nil
}

// EnvVarValueFromPod is `KubernetesMachine.get_env_var_value_from_pod`
// (`KubernetesMachine.py:761`): the value of one environment variable of the
// pod's single container, or "" when there is none.
func EnvVarValueFromPod(pod *corev1.Pod, name string) string {
	if pod == nil || len(pod.Spec.Containers) == 0 {
		return ""
	}
	container := pod.Spec.Containers[len(pod.Spec.Containers)-1]
	for _, env := range container.Env {
		if env.Name == name {
			return env.Value
		}
	}
	return ""
}

// podNetworkAttachment is one entry of the `k8s.v1.cni.cncf.io/networks`
// annotation array (`KubernetesMachine.py:491-496`).
type podNetworkAttachment struct {
	Name string `json:"name"`
	// Namespace is the scenario hash.
	Namespace string `json:"namespace"`
	// Interface is `"net%d" % idx`: the guest NIC name, numbered by the
	// interface's own number and not by its position in the array.
	Interface string `json:"interface"`
	// KatharaLink is the collision domain's Kathará name, which the inventory
	// renders and the reconstruction does not read.
	KatharaLink string `json:"kathara.link"`

	MAC string `json:"mac,omitempty"`
}

// encodeNetworkAttachments is `json.dumps(network_interfaces)`
// (`KubernetesMachine.py:497`), with CPython's defaults rather than Go's.
func encodeNetworkAttachments(attachments []podNetworkAttachment) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, attachment := range attachments {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteByte('{')
		pyJSONField(&b, "name", attachment.Name, true)
		pyJSONField(&b, "namespace", attachment.Namespace, false)
		pyJSONField(&b, "interface", attachment.Interface, false)
		pyJSONField(&b, "kathara.link", attachment.KatharaLink, false)
		if attachment.MAC != "" {
			pyJSONField(&b, "mac", attachment.MAC, false)
		}
		b.WriteByte('}')
	}
	b.WriteByte(']')
	return b.String()
}

// pyJSONField writes one `"key": "value"` pair, prefixed by `, ` unless it is
// the first of its object.
func pyJSONField(b *strings.Builder, key, value string, first bool) {
	if !first {
		b.WriteString(", ")
	}
	pyJSONString(b, key)
	b.WriteString(": ")
	pyJSONString(b, value)
}

// pyJSONString is CPython's `encode_basestring_ascii`, whose escape set is
// `ESCAPE_ASCII = re.compile(r'([\\"]|[^\ -~])')`: the backslash, the double
// quote, and everything outside printable ASCII — DEL (0x7f) included, since it
// is above `~`.
func pyJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '"':
			b.WriteString(`\"`)
		case r == '\b':
			b.WriteString(`\b`)
		case r == '\f':
			b.WriteString(`\f`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r >= 0x20 && r <= 0x7e:
			b.WriteRune(r)
		case r > 0xFFFF:
			r -= 0x10000
			pyJSONEscape(b, 0xD800+(r>>10))
			pyJSONEscape(b, 0xDC00+(r&0x3FF))
		default:
			pyJSONEscape(b, r)
		}
	}
	b.WriteByte('"')
}

// pyJSONEscape writes one `\uXXXX`, lower-case hex, as CPython's `'\\u%04x'`
// does.
func pyJSONEscape(b *strings.Builder, r rune) {
	const hex = "0123456789abcdef"
	b.WriteString(`\u`)
	b.WriteByte(hex[(r>>12)&0xF])
	b.WriteByte(hex[(r>>8)&0xF])
	b.WriteByte(hex[(r>>4)&0xF])
	b.WriteByte(hex[r&0xF])
}

// podNetworkAttachments parses the annotation back out of a pod.
func podNetworkAttachments(pod *corev1.Pod) ([]podNetworkAttachment, error) {
	raw, ok := pod.Annotations[networkAttachmentAnnotation]
	if !ok {
		return nil, newPyKeyError(networkAttachmentAnnotation)
	}

	var attachments []podNetworkAttachment
	if err := json.Unmarshal([]byte(raw), &attachments); err != nil {
		return nil, err
	}
	return attachments, nil
}

func listOptions(selector string) metav1.ListOptions {
	timeout := listTimeoutSeconds
	return metav1.ListOptions{LabelSelector: selector, TimeoutSeconds: &timeout}
}
