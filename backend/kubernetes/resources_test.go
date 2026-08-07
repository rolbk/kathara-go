package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8stesting "k8s.io/client-go/testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// ---------------------------------------------------------------------------
// ConfigMap
// ---------------------------------------------------------------------------

// TestConfigMapForDeviceWithNoFiles is `_build_for_machine`'s None branch: a
// device that ships nothing gets no ConfigMap, which is what makes the postStart
// hook's `if [ -f "/tmp/kathara/hostlab.b64" ]` false.
func TestConfigMapForDeviceWithNoFiles(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	configMap, err := m.configMap.DeployForMachine(context.Background(), device)
	if err != nil {
		t.Fatalf("DeployForMachine: %v", err)
	}
	if configMap != nil {
		t.Errorf("config map = %v, want nil", configMap)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "configmaps" {
			t.Fatal("a ConfigMap was submitted for a device with no files")
		}
	}
}

// TestConfigMapGolden is `_build_for_machine`'s object: the name, the
// `deletionGracePeriodSeconds: 0` that has no effect on a ConfigMap but is in
// the submitted object, and the single `hostlab.b64` key.
//
// The tar bytes themselves are not compared — Python's archive carries
// `time.time()` mtimes and a gzip header that differ run to run (SYNTHESIS
// §1.4) — so the golden masks them and [TestPackDataRoundTrip] checks the
// content instead.
func TestConfigMapGolden(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	if err := device.CreateFileFromString("hello\n", "/etc/motd"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}

	configMap, err := m.configMap.buildForMachine(device)
	if err != nil {
		t.Fatalf("buildForMachine: %v", err)
	}
	if configMap == nil {
		t.Fatal("config map = nil, want one")
	}

	if _, ok := configMap.Data[hostlabKey]; !ok {
		t.Fatalf("data keys = %v, want %q", configMap.Data, hostlabKey)
	}
	configMap.Data = map[string]string{hostlabKey: "<TARBALL>"}
	assertGolden(t, "configmap.json", configMap)
}

// TestConfigMapTooLarge is `KubernetesConfigMap.py:87-93`: the 3 MiB ceiling,
// measured on the TAR bytes, with both sizes rendered by
// `utils.human_readable_bytes`.
func TestConfigMapTooLarge(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)
	device := newBaseDevice(t, lab)

	// Incompressible bytes, so the gzipped archive really does exceed the
	// ceiling. `.bin` also makes the binary sniff short-circuit, which keeps
	// the newline normalisation out of the way.
	big := make([]byte, 5*1024*1024)
	if _, err := rand.Read(big); err != nil {
		t.Fatalf("rand: %v", err)
	}
	if err := device.CreateFileFromString(string(big), "/big.bin"); err != nil {
		t.Fatalf("CreateFileFromString: %v", err)
	}

	_, err := m.configMap.buildForMachine(device)
	if !errors.Is(err, kerrors.ErrKubernetesConfigMap) {
		t.Fatalf("error = %v, want a KubernetesConfigMap error", err)
	}
	// `utils.human_readable_bytes(3145728)` is "3.0 MB" (oracle-probed), and
	// ERROR_CODES.md freezes the sentence around it.
	if !strings.Contains(err.Error(), "Maximum supported size: 3.0 MB.") {
		t.Errorf("message = %q, want it to name the 3.0 MB ceiling", err.Error())
	}
}

// TestConfigMapDeleteSwallowsErrors is `delete_for_machine`'s
// `except ApiException: return`: a device that shipped no files never had a
// ConfigMap, so a 404 here is the normal case.
func TestConfigMapDeleteSwallowsErrors(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	clientset.PrependReactor("delete", "configmaps", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, "x")
	})

	// No panic, no return value to check: the signature has nowhere to put a
	// failure, which is the point.
	m.configMap.DeleteForMachine(context.Background(), "devprefix-pc1", defaultScenarioHash)
}

// ---------------------------------------------------------------------------
// Secret
// ---------------------------------------------------------------------------

// TestSecretNoDockerConfig is EXPECTATIONS-k8s §3
// `test_create_no_docker_config`: with the setting unset, `create` returns an
// empty list and makes NO API call.
func TestSecretNoDockerConfig(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)

	secrets, err := m.secret.Create(context.Background(), lab)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("%d secrets, want 0", len(secrets))
	}
	for _, action := range clientset.Actions() {
		if action.GetResource().Resource == "secrets" {
			t.Fatal("a Secret call was made with no docker_config_json")
		}
	}
}

// TestSecretGolden is EXPECTATIONS-k8s §3 `test_create_with_docker_config`: the
// exact Secret submitted, in namespace `9pe3y6IDMwx4PfOPu5mbNg` — the hash of
// "Default scenario".
//
// The `data` value is the setting VERBATIM: the settings screen stores the
// base64 of a config.json and nothing re-encodes it. client-go's `Data` is
// `map[string][]byte` and its codec base64s on the way out, so the value is
// decoded here and re-encoded there; the golden proves the round trip is the
// identity.
func TestSecretGolden(t *testing.T) {
	s := testSettings()
	s.DockerConfigJSON = ptr("eyJhdXRocyI6IHt9fQ==")
	m, clientset, _, _ := newTestManager(t, s)

	lab := model.NewLab("Default scenario", testDefaults(s))
	if lab.Hash != "9pe3y6IDMwx4PfOPu5mbNg" {
		t.Fatalf("lab hash = %q", lab.Hash)
	}

	// The wait watches for an ADDED event, which the fake tracker only produces
	// for a watcher registered first — so the event is injected.
	injectSecretAdded(clientset, lab.Hash, privateRegistrySecretName)

	secrets, err := m.secret.Create(context.Background(), lab)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(secrets) != 1 {
		t.Fatalf("%d secrets, want 1", len(secrets))
	}
	assertGolden(t, "secret.json", secrets[0])
}

// injectSecretAdded registers a watch reactor that immediately reports the
// Secret as ADDED, which is what a real API server does for a `watch=true`
// request with no resourceVersion.
func injectSecretAdded(clientset *fakeClientset, namespace, name string) {
	watcher := watch.NewFakeWithChanSize(1, false)
	watcher.Add(&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}})
	clientset.PrependWatchReactor("secrets", k8stesting.DefaultWatchReactor(watcher, nil))
}

// TestSecretSwallowsAPIErrors is EXPECTATIONS-k8s §3 `test_create_exception`
// and `test_create_secret_exception`: a creation failure answers an EMPTY list
// and skips the wait — a misconfigured registry credential is silent here and
// surfaces as an ImagePullBackOff later (k8s-backend.md G25).
func TestSecretSwallowsAPIErrors(t *testing.T) {
	s := testSettings()
	s.DockerConfigJSON = ptr("eyJhdXRocyI6IHt9fQ==")
	m, clientset, _, _ := newTestManager(t, s)
	clientset.PrependReactor("create", "secrets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, "private-registry", errors.New("nope"))
	})

	secrets, err := m.secret.Create(context.Background(), newTestLab(t, s))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("%d secrets, want 0", len(secrets))
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "watch" && action.GetResource().Resource == "secrets" {
			t.Error("the wait ran despite the creation failure")
		}
	}
}

// TestSecretUndecodableConfigIsSilent pins the port's own exit for a
// `docker_config_json` that is not valid base64: Python sends it, the API
// server answers 400, and the exception is swallowed. The observable outcome —
// no Secret, no error — is the same, so the decode failure takes the same exit
// (DIVERGENCES.md).
func TestSecretUndecodableConfigIsSilent(t *testing.T) {
	s := testSettings()
	s.DockerConfigJSON = ptr("not base64!!")
	m, clientset, _, _ := newTestManager(t, s)

	secrets, err := m.secret.Create(context.Background(), newTestLab(t, s))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(secrets) != 0 {
		t.Errorf("%d secrets, want 0", len(secrets))
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "secrets" {
			t.Error("an undecodable credential should not reach the API server")
		}
	}
}

// TestSecretDataRoundTrip proves the base64 identity the golden depends on.
func TestSecretDataRoundTrip(t *testing.T) {
	const value = "eyJhdXRocyI6IHt9fQ=="
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := base64.StdEncoding.EncodeToString(raw); got != value {
		t.Errorf("round trip = %q, want %q", got, value)
	}
}

// ---------------------------------------------------------------------------
// Namespace
// ---------------------------------------------------------------------------

// TestNamespaceCreate is `KubernetesNamespace.create`: the object submitted,
// and the `app=kathara` label that makes the unfiltered listings work.
func TestNamespaceCreate(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	lab := newTestLab(t, s)

	injectNamespaceEvent(clientset, watch.Modified, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: lab.Hash},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})

	namespace, err := m.namespace.Create(context.Background(), lab)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if namespace == nil {
		t.Fatal("namespace = nil, want the definition")
	}
	if namespace.Name != defaultScenarioHash {
		t.Errorf("name = %q, want %q", namespace.Name, defaultScenarioHash)
	}
	if namespace.Labels[labelApp] != labelAppValue {
		t.Errorf("labels = %v, want app=kathara", namespace.Labels)
	}
}

// TestNamespaceCreateSwallowsAPIErrors is the `except ApiException: return None`
// that makes a second `lstart` of the same scenario a no-op — and that SKIPS
// the wait, because the wait is below the create inside the same `try`.
func TestNamespaceCreateSwallowsAPIErrors(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)
	clientset.PrependReactor("create", "namespaces", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, defaultScenarioHash)
	})

	namespace, err := m.namespace.Create(context.Background(), newTestLab(t, s))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if namespace != nil {
		t.Errorf("namespace = %v, want nil", namespace)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "watch" {
			t.Error("the wait ran despite the creation failure")
		}
	}
}

// TestNamespaceGetAll is `get_all`: every namespace labelled `app=kathara`, and
// nothing else — this is what an unfiltered pod or network listing enumerates.
func TestNamespaceGetAll(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara1", Labels: map[string]string{labelApp: labelAppValue}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara2", Labels: map[string]string{labelApp: labelAppValue}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "default"}},
	)

	namespaces, err := m.namespace.GetAll(context.Background())
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(namespaces) != 2 {
		t.Fatalf("%d namespaces, want 2", len(namespaces))
	}
}

// TestNamespaceGetNamespace is `get_namespace`: a LIST with the
// `kubernetes.io/metadata.name` selector, so a missing namespace is nil rather
// than a 404. It is dead code in Python too and ported as public surface.
func TestNamespaceGetNamespace(t *testing.T) {
	s := testSettings()
	m, _, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{
			Name:   defaultScenarioHash,
			Labels: map[string]string{metadataNameLabel: defaultScenarioHash},
		}},
	)

	got, err := m.namespace.GetNamespace(context.Background(), defaultScenarioHash)
	if err != nil {
		t.Fatalf("GetNamespace: %v", err)
	}
	if got == nil || got.Name != defaultScenarioHash {
		t.Fatalf("namespace = %v, want %q", got, defaultScenarioHash)
	}

	missing, err := m.namespace.GetNamespace(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetNamespace: %v", err)
	}
	if missing != nil {
		t.Errorf("namespace = %v, want nil", missing)
	}
}

// TestNamespaceWipe is `wipe`: one delete per Kathará namespace, sequential and
// unguarded, followed by a single collective wait.
func TestNamespaceWipe(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara1", Labels: map[string]string{labelApp: labelAppValue}}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara2", Labels: map[string]string{labelApp: labelAppValue}}},
	)

	// Two DELETED events, so the collective wait terminates.
	watcher := watch.NewFakeWithChanSize(2, false)
	watcher.Delete(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara1"}})
	watcher.Delete(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "kathara2"}})
	clientset.PrependWatchReactor("namespaces", k8stesting.DefaultWatchReactor(watcher, nil))

	if err := m.namespace.Wipe(context.Background()); err != nil {
		t.Fatalf("Wipe: %v", err)
	}

	deleted := 0
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "delete" && action.GetResource().Resource == "namespaces" {
			deleted++
		}
	}
	if deleted != 2 {
		t.Errorf("%d namespace deletes, want 2", deleted)
	}
}

// TestNamespaceUndeployWaitsForZero is the `if namespaces_to_delete > 0` guard:
// with nothing matching, the wait returns without opening a watch at all.
func TestNamespaceUndeployWaitsForZero(t *testing.T) {
	s := testSettings()
	m, clientset, _, _ := newTestManager(t, s)

	if err := m.namespace.Undeploy(context.Background(), defaultScenarioHash); err != nil {
		t.Fatalf("Undeploy: %v", err)
	}
	for _, action := range clientset.Actions() {
		if action.GetVerb() == "watch" {
			t.Error("a watch was opened for a namespace that does not exist")
		}
	}
}

// injectNamespaceActive is the Active-phase event a real API server sends for a
// `watch=true` request with no resourceVersion — it replays the current state
// first. The fake tracker reports only changes made AFTER a watcher is
// registered, and `KubernetesNamespace.create` registers its watcher after the
// create, so without this the wait would never see the namespace it just made.
func injectNamespaceActive(clientset *fakeClientset, labHash string) {
	injectNamespaceEvent(clientset, watch.Modified, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: labHash},
		Status:     corev1.NamespaceStatus{Phase: corev1.NamespaceActive},
	})
}

// injectNamespaceEvent registers a watch reactor carrying one event, which is
// what a real API server sends for a `watch=true` request with no
// resourceVersion.
func injectNamespaceEvent(clientset *fakeClientset, eventType watch.EventType, namespace *corev1.Namespace) {
	watcher := watch.NewFakeWithChanSize(1, false)
	switch eventType {
	case watch.Deleted:
		watcher.Delete(namespace)
	default:
		watcher.Modify(namespace)
	}
	clientset.PrependWatchReactor("namespaces", k8stesting.DefaultWatchReactor(watcher, nil))
}
