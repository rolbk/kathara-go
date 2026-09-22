package kubernetes

import (
	"context"
	"log/slog"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/KatharaFramework/kathara-go/model"
)

// namespaceService is `KubernetesNamespace` (`KubernetesNamespace.py:11`).
type namespaceService struct {
	clientset kubernetes.Interface
}

// Create is `create` (`KubernetesNamespace.py:19`): create the namespace and
// block until it reports `Active`.
func (s *namespaceService) Create(ctx context.Context, lab *model.Lab) (*corev1.Namespace, error) {
	definition := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   lab.Hash,
			Labels: map[string]string{labelApp: labelAppValue},
		},
	}

	if _, err := s.clientset.CoreV1().Namespaces().Create(ctx, definition, metav1.CreateOptions{}); err != nil {
		if isAPIException(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := s.waitCreation(ctx, lab.Hash); err != nil {
		if isAPIException(err) {
			return nil, nil
		}
		return nil, err
	}

	// Python returns the definition it BUILT, not the object the API server
	// answered with — so the returned value carries no uid, no resourceVersion
	// and no status. Nothing reads it; the shape is preserved anyway.
	return definition, nil
}

// Undeploy is `undeploy` (`KubernetesNamespace.py:40`): delete the namespace
// and block until it is gone.
func (s *namespaceService) Undeploy(ctx context.Context, labHash string) error {
	if err := s.clientset.CoreV1().Namespaces().Delete(ctx, labHash, metav1.DeleteOptions{}); err != nil {
		if isAPIException(err) {
			return nil
		}
		return err
	}
	if err := s.waitDeletion(ctx, namespaceSelector(labHash)); err != nil {
		if isAPIException(err) {
			return nil
		}
		return err
	}
	return nil
}

// Wipe is `wipe` (`KubernetesNamespace.py:55`): delete every Kathará namespace,
// then wait for all of them together.
func (s *namespaceService) Wipe(ctx context.Context) error {
	namespaces, err := s.GetAll(ctx)
	if err != nil {
		return err
	}

	for _, namespace := range namespaces {
		if err := s.clientset.CoreV1().Namespaces().Delete(ctx, namespace.Name, metav1.DeleteOptions{}); err != nil {
			return translateAPI(err)
		}
	}

	return s.waitDeletion(ctx, ObjectSelector(""))
}

// GetAll is `get_all` (`KubernetesNamespace.py:68`): every namespace labelled
// `app=kathara`.
func (s *namespaceService) GetAll(ctx context.Context) ([]corev1.Namespace, error) {
	list, err := s.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: ObjectSelector(""),
	})
	if err != nil {
		return nil, translateAPI(err)
	}
	return list.Items, nil
}

// GetNamespace is `get_namespace` (`KubernetesNamespace.py:76`): the namespace
// whose `kubernetes.io/metadata.name` label is labHash, or nil.
func (s *namespaceService) GetNamespace(ctx context.Context, labHash string) (*corev1.Namespace, error) {
	list, err := s.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{
		LabelSelector: namespaceSelector(labHash),
	})
	if err != nil {
		return nil, translateAPI(err)
	}
	if len(list.Items) == 0 {
		return nil, nil
	}
	return &list.Items[len(list.Items)-1], nil
}

// waitCreation is `_wait_namespace_creation` (`KubernetesNamespace.py:85`):
// watch the namespace until its phase is `Active`.
func (s *namespaceService) waitCreation(ctx context.Context, labHash string) error {
	watcher, err := s.clientset.CoreV1().Namespaces().Watch(ctx, metav1.ListOptions{
		LabelSelector: namespaceSelector(labHash),
	})
	if err != nil {
		return translateAPI(err)
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
			namespace, ok := event.Object.(*corev1.Namespace)
			if !ok {
				continue
			}
			// `f"Event: {event['type']} - Namespace: {event['object'].metadata.name}"`
			// (`KubernetesNamespace.py:97`).
			slog.Debug("Event: " + string(event.Type) + " - Namespace: " + namespace.Name)
			if namespace.Status.Phase == corev1.NamespaceActive {
				return nil
			}
		}
	}
}

// waitDeletion is `_wait_namespaces_deletion` (`KubernetesNamespace.py:102`):
// count how many namespaces the selector matches NOW, then watch until that
// many DELETED events have arrived.
func (s *namespaceService) waitDeletion(ctx context.Context, selector string) error {
	list, err := s.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return translateAPI(err)
	}
	toDelete := len(list.Items)
	if toDelete == 0 {
		return nil
	}

	watcher, err := s.clientset.CoreV1().Namespaces().Watch(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return translateAPI(err)
	}
	defer watcher.Stop()

	deleted := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, open := <-watcher.ResultChan():
			if !open {
				return nil
			}
			namespace, ok := event.Object.(*corev1.Namespace)
			if !ok {
				continue
			}
			// `f"Event: {event['type']} - Namespace: {event['object'].metadata.name}"`
			// (`KubernetesNamespace.py:117`).
			slog.Debug("Event: " + string(event.Type) + " - Namespace: " + namespace.Name)
			if event.Type == watch.Deleted {
				deleted++
			}
			if deleted == toDelete {
				return nil
			}
		}
	}
}
