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

	dockerConfigJSON *string
}

// Create is `create` (`KubernetesSecret.py:19`): the Secrets for one scenario,
// which is at most one.
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
			// `f"Event: {event['type']} - Secret: {event['object'].metadata.name}"`
			// (`KubernetesSecret.py:81`).
			slog.Debug("Event: " + string(event.Type) + " - Secret: " + secret.Name)
			if event.Type == watch.Added {
				return nil
			}
		}
	}
}
