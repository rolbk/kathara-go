// This file is `KubernetesSecret.py`: the one Secret Megalos creates, which
// carries a Docker `config.json` so that pods can pull from a private registry.

package kubernetes

import (
	"context"
	"encoding/base64"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/KatharaFramework/kathara-go/model"
)

// dockerConfigJSONKey is the key a `kubernetes.io/dockerconfigjson` Secret must
// carry (`KubernetesSecret.py:34`).
const dockerConfigJSONKey = ".dockerconfigjson"

// secretService is `KubernetesSecret` (`KubernetesSecret.py:12`).
type secretService struct {
	clientset kubernetes.Interface
	// dockerConfigJSON is `Setting.docker_config_json`: the **base64 of a
	// config.json's contents**, not a path (settings.Settings.DockerConfigJSON).
	// nil and "" both mean "no private-registry secret" (NILABILITY.tsv:52).
	dockerConfigJSON *string
}

// Create is `create` (`KubernetesSecret.py:19`): the Secrets for one scenario,
// which is at most one.
//
// The list shape is Python's and the loop it implies never runs more than once;
// it exists because `_create_secret` is written to be reusable and nothing has
// reused it. A creation failure is not an error — [secretService.createSecret]
// answers nil and the entry is simply absent from the list (k8s-backend.md
// G25), so a misconfigured registry credential is silent here and surfaces as
// an ImagePullBackOff on every pod.
func (s *secretService) Create(ctx context.Context, lab *model.Lab) ([]*corev1.Secret, error) {
	dockerConfig := stringOrEmpty(s.dockerConfigJSON)
	if dockerConfig == "" {
		return nil, nil
	}

	secret, err := s.createSecret(ctx, lab.Hash, privateRegistrySecretName,
		corev1.SecretTypeDockerConfigJson, map[string]string{dockerConfigJSONKey: dockerConfig})
	if err != nil {
		return nil, err
	}
	if secret == nil {
		return nil, nil
	}
	return []*corev1.Secret{secret}, nil
}

// createSecret is `_create_secret` (`KubernetesSecret.py:42`): submit the
// Secret and block until the API server reports it.
//
// # The base64 round trip
//
// Python's `data` dict holds STRINGS and the client serializes them verbatim,
// so the wire carries `{".dockerconfigjson": "<the setting value>"}` — and the
// setting value is already the base64 of a config.json, which is why nothing
// encodes it here. client-go's `Secret.Data` is `map[string][]byte`, and its
// JSON codec base64-encodes on the way out; handing it the setting string would
// double-encode. So the value is DECODED here and re-encoded by the codec,
// which reproduces the wire bytes exactly for every input the API server
// accepts.
//
// An input that is not valid base64 cannot round-trip, and it does not have to:
// Python sends it, the API server answers 400, the `except ApiException` below
// swallows it and `create` returns an empty list. The observable outcome —
// no Secret, no error, no wait — is the same, so the decode failure takes the
// same exit. DIVERGENCES.md records it.
//
// Every API failure answers nil AND skips the wait, because the wait is inside
// the `try` and below the create.
func (s *secretService) createSecret(ctx context.Context, labHash, name string, secretType corev1.SecretType, data map[string]string) (*corev1.Secret, error) {
	decoded := make(map[string][]byte, len(data))
	for key, value := range data {
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return nil, nil
		}
		decoded[key] = raw
	}

	definition := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: labHash,
			Labels:    map[string]string{labelApp: labelAppValue},
		},
		Type: secretType,
		Data: decoded,
	}

	if _, err := s.clientset.CoreV1().Secrets(labHash).Create(ctx, definition, metav1.CreateOptions{}); err != nil {
		if isAPIException(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := s.waitCreation(ctx, labHash, name); err != nil {
		if isAPIException(err) {
			return nil, nil
		}
		return nil, err
	}

	// As with the namespace, Python returns the object it BUILT rather than the
	// one the API server answered with.
	return definition, nil
}

// waitCreation is `_wait_secret_creation` (`KubernetesSecret.py:68`): watch the
// namespace's Secrets, filtered by name, until an ADDED event arrives.
//
// The filter is a FIELD selector (`metadata.name=…`), not a label one — the
// Secret carries `app=kathara` but this wait does not use it.
//
// Python leaves the watch generator open (`break`, not `w.stop()`), which leaks
// the connection until the object is collected (k8s-backend.md C11); the
// deferred Stop here closes it, which is the only difference and is not
// observable.
func (s *secretService) waitCreation(ctx context.Context, labHash, name string) error {
	watcher, err := s.clientset.CoreV1().Secrets(labHash).Watch(ctx, metav1.ListOptions{
		FieldSelector: fields.OneTermEqualSelector("metadata.name", name).String(),
	})
	if err != nil {
		return err
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-watcher.ResultChan():
			if !open {
				return nil
			}
			secret, ok := event.Object.(*corev1.Secret)
			if !ok {
				continue
			}
			slog.Debug("Secret event.", "type", string(event.Type), "secret", secret.Name)
			if event.Type == watch.Added {
				return nil
			}
		}
	}
}
