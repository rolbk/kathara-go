// This file has no single Python original: it is the three shapes that hide
// behind `Any` in the model (k8s-backend.md G2/G23), given names.
//
//   - `link.api_object` is a plain dict from `create_namespaced_custom_object`,
//     indexed `["metadata"]["name"]`. Here it is an
//     [unstructured.Unstructured], which is the same thing with a type.
//   - `machine.api_object` is a `V1Deployment` right after `create` and a
//     `V1Pod` when it comes out of a getter. Every consumer reads only
//     `metadata.labels["name"]` and `metadata.namespace`, which both carry, so
//     the accessors below take the narrowest interface that has them.
//   - the pod's `spec.containers[0].env` is where the device's shell is
//     recorded, and reading it is the one accessor with a bug worth keeping.

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
//
// It is a named alias rather than a wrapper struct because every read Kathará
// performs is a dict lookup — `["metadata"]["name"]`,
// `["metadata"]["namespace"]`, `["metadata"]["labels"]["name"]`,
// `["spec"]["config"]` — and an [unstructured.Unstructured] answers all four
// without a conversion.
type Network = unstructured.Unstructured

// NetworkDefinition is `KubernetesLink._build_definition`
// (`KubernetesLink.py:274`): the object submitted to create a collision domain.
//
// The `spec.config` value is a STRING containing JSON, not nested JSON — the
// CNI spec is passed to Multus verbatim — and Python builds it from a
// triple-quoted literal whose source indentation is part of the value
// (k8s-backend.md G24). [networkConfigJSON] reproduces the indentation byte for
// byte, because the string is what the cluster stores and a golden reads back.
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
//
// Python's literal is a triple-quoted string indented to its position in the
// method body, so the value carries a newline after `{`, twenty-eight spaces
// before each key and twenty-four before the closing brace. None of that is
// meaningful to Multus, and all of it is in the object the cluster stores.
//
// Three interpolations: `link.name.lower()` — the collision-domain name
// lowercased, which is NOT the mangled Kubernetes name and can therefore still
// contain characters a resource name may not; `link.lab.hash[0:6]`, the first
// six characters of the scenario hash, which is what the `megalos` CNI plugin
// uses to keep two scenarios' VXLAN interfaces apart on a shared node; and the
// VNI itself.
//
// The hash slice is by bytes, as Python's is. A scenario hash is urlsafe-base64
// and therefore ASCII, so the two agree; a shorter hash is taken whole rather
// than panicking, which is also what Python's slice does.
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

// NetworkName reads `network["metadata"]["name"]` — the mangled Kubernetes
// name, which is what `undeploy(selected_links=…)` matches against
// (`KubernetesManager.py:329`, EXPECTATIONS-k8s "matches against the Kubernetes
// network name, not the CD label").
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
//
// Python indexes `network['spec']['config']` unguarded and hands the result to
// `json.loads`, so a NAD without a spec is a KeyError and one with a malformed
// config is a JSONDecodeError. Both are returned here rather than raised: the
// callers are a listing (`_get_existing_network_ids`, which runs before every
// deploy) and an inventory, and PORT_SPEC §10 does not allow either to take the
// process down over one foreign object in a namespace.
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
//
// JSON has one number type, so a numeric value arrives as a float64 and is
// truncated the way Python's `int()` truncates a float.
//
// A STRING `vxlanId` is read too, because `int("123")` is 123: nothing this
// backend writes produces one, but a foreign NAD may, and Python reserves its id
// like any other. Dropping it would let the allocator hand the same VNI to a
// collision domain of this scenario and put two of them on one VXLAN wire. The
// spelling is [util.PyInt], so the accepted syntax is CPython's — `" 12 "` and
// `"٣"` parse, `"12.5"` does not.
//
// Anything else, and any string `int()` would reject, is a `ValueError` or a
// `TypeError` in Python and a not-found here; the caller drops it rather than
// failing the listing, which is the same reasoning as [NetworkConfig].
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
//
// Both the `V1Deployment` `create` assigns and the `V1Pod` the getters return
// satisfy it, which is why `copy_files(machine.api_object, …)` works with
// either and why the port does not need to know which one it is holding
// (k8s-backend.md G23).
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
//
// # The mutation that does not survive
//
// Python's implementation is `container_definition = containers.pop()`, which
// REMOVES the container from the pod object it was handed (k8s-backend.md G7).
// After one call the pod has zero containers and a second call answers None —
// so a device whose shell is read twice silently falls back to
// `Setting.device_shell` the second time, and `get_lab_from_api` only works
// because it reads `pod.spec.containers[0]` BEFORE calling this
// (`KubernetesManager.py:706-707`).
//
// This does not mutate. Reproducing it would mean handing out pod objects that
// decay as they are read, and the observable consequence — the second read
// answering the settings default — is reachable from no 1.0 call path: the two
// callers (`connect`, `_delete_machine`) each fetch their own pod and read it
// once. DIVERGENCES.md records it.
//
// `pop()` also takes the LAST container rather than the first, which matters
// only for a pod this backend did not create; Megalos pods have exactly one.
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
//
// The field order is the JSON key order Python's `json.dumps` emits, which is
// the dict literal's: `name`, `namespace`, `interface`, then the two keys of
// `additional_data` — `kathara.link` always, `mac` only when the interface
// declares one. encoding/json emits struct fields in declaration order, so this
// struct IS the wire format.
type podNetworkAttachment struct {
	// Name is the Kubernetes network name — the NAD's `metadata.name`, read
	// off `interface.link.api_object`, which is why links must be deployed
	// first (k8s-backend.md G2).
	Name string `json:"name"`
	// Namespace is the scenario hash.
	Namespace string `json:"namespace"`
	// Interface is `"net%d" % idx`: the guest NIC name, numbered by the
	// interface's own number and not by its position in the array.
	Interface string `json:"interface"`
	// KatharaLink is the collision domain's Kathará name, which the inventory
	// renders and the reconstruction does not read.
	KatharaLink string `json:"kathara.link"`
	// MAC is the interface's hardware address, omitted when empty —
	// `additional_data` only carries the key when `interface.mac_address` is
	// truthy (`KubernetesMachine.py:489-490`), and an empty MAC in the model is
	// the same as none (NILABILITY.tsv:20).
	MAC string `json:"mac,omitempty"`
}

// encodeNetworkAttachments is `json.dumps(network_interfaces)`
// (`KubernetesMachine.py:497`), with CPython's defaults rather than Go's.
//
// The annotation value is a STRING stored in the pod template, so its bytes are
// part of the object the cluster keeps and a golden reads back. Two CPython
// defaults differ from `encoding/json` and both are reproduced:
//
//   - the separators are `", "` and `": "`, where Go emits `,` and `:`;
//   - `ensure_ascii=True`, so every non-ASCII character becomes a `\uXXXX`
//     escape (a surrogate pair above the BMP), where Go emits UTF-8 and escapes
//     `<`, `>` and `&` instead.
//
// A collision-domain name can be non-ASCII — lab.conf allows Unicode `\w`
// (model.Link.Name) — so the second one is reachable, not theoretical.
//
// An empty list is `"[]"`, which is what a device with no interfaces gets.
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
//
// The five short escapes are `ESCAPE_DCT`'s (`\b \f \n \r \t`); every other
// escaped character becomes `\uXXXX`, and a rune above the BMP becomes the
// UTF-16 surrogate pair CPython emits.
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
//
// Python does `json.loads(pod.metadata.annotations["k8s.v1.cni.cncf.io/networks"])`
// at four sites — `undeploy_machine`, `undeploy_link`, `undeploy_lab` and
// `get_lab_from_api` — every one of them unguarded, so a pod without the
// annotation is a KeyError that fails the whole operation. That is reproduced:
// the annotation is what decides which collision domains survive a partial
// teardown, and treating a pod without one as "attached to nothing" would
// delete networks that are still in use.
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

// listOptions is the `metav1.ListOptions` every pod and network listing uses:
// the label selector of [ObjectSelector] and the `timeout_seconds=9999` of
// k8s-backend.md G21.
func listOptions(selector string) metav1.ListOptions {
	timeout := listTimeoutSeconds
	return metav1.ListOptions{LabelSelector: selector, TimeoutSeconds: &timeout}
}
