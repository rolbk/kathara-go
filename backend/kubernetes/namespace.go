// This file is `KubernetesNamespace.py`: the scenario's namespace, which IS the
// scenario hash — lowercased, because a Kubernetes name may not carry capitals
// and `generate_urlsafe_hash` produces mixed-case base64 (k8s-backend.md G1).

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
//
// Its `__slots__` also lists a `kubernetes_secret` that is never assigned and
// never read (k8s-backend.md G26); there is nothing to port.
type namespaceService struct {
	clientset kubernetes.Interface
}

// Create is `create` (`KubernetesNamespace.py:19`): create the namespace and
// block until it reports `Active`.
//
// Every API failure is swallowed and answered with (nil, nil) — Python returns
// None — and the 409 of a namespace that already exists is the normal case, not
// an error: `deploy_lab` calls this on every run of the same scenario. The
// callers all ignore the result.
//
// The wait is INSIDE the `try`, so a namespace that already existed is not
// waited for at all: Python raises out of `create_namespace` before reaching
// the watch. That is load-bearing on the second `lstart` of a scenario, where
// the namespace is already Active and a watch would return immediately anyway,
// and on a namespace that is still Terminating — where the create fails with
// 409, the wait is skipped, and the pod creations that follow fail with the 403
// `deploy_lab` turns into [kerrors.ErrLabTerminating].
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
//
// Deleting a namespace deletes everything in it, which is why the manager's
// teardown path deletes the pods and the networks first and then this: the
// explicit deletions are what produce the progress events, and this is what
// collects whatever they missed.
//
// The API failure is swallowed, and so is a failure of the wait — both are
// inside Python's one `try`.
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
//
// The deletes are SEQUENTIAL and unguarded — unlike [namespaceService.Undeploy]
// there is no `try` here, so the first failure aborts the loop and the wait
// never runs (k8s-backend.md O16). This is the whole of `KubernetesManager.wipe`:
// Megalos does not walk pods or networks, it drops the namespaces and lets the
// API server cascade.
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
//
// It is what makes an unfiltered listing possible: with no `lab_hash`, both
// `get_machines_api_objects_by_filters` and its collision-domain twin enumerate
// these and query each one (`KubernetesMachine.py:998-999`).
//
// Note the missing `timeout_seconds`: the pod and network listings carry
// `9999` and this one does not (k8s-backend.md G21). Preserved.
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
//
// It is a LIST with a label selector rather than a get-by-name, which is what
// makes a missing namespace an empty result instead of a 404. `pop()` takes the
// last match, which for this selector is the only one.
//
// Nothing inside this package calls it — it is dead in Python too
// (k8s-backend.md G26) — and it is ported because it is public API surface.
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
//
// The watch is unbounded — there is no timer on this one, unlike the pod
// watcher of [machineService.DeployMachines] — so the context is the only way
// out (PORT_SPEC §0.2 #11). A closed watch channel ends the wait rather than
// reconnecting: Python's `w.stream` would end its `for` loop the same way when
// the server closes the connection, and the caller's next step fails on its own
// if the namespace really is not there.
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
			slog.Debug("Namespace event.", "type", string(event.Type), "namespace", namespace.Name)
			if namespace.Status.Phase == corev1.NamespaceActive {
				return nil
			}
		}
	}
}

// waitDeletion is `_wait_namespaces_deletion` (`KubernetesNamespace.py:102`):
// count how many namespaces the selector matches NOW, then watch until that
// many DELETED events have arrived.
//
// The count is taken before the watch is established, which is a race Python
// has too: a namespace that finishes terminating in between never produces a
// DELETED event on this watch and the wait then blocks until the context ends.
// Reproduced — the alternative is a different termination condition, and the
// count is what `wipe` depends on to wait for all of its deletions at once.
//
// Zero matches returns immediately without opening a watch, which is the
// `if namespaces_to_delete > 0` guard.
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
			slog.Debug("Namespace event.", "type", string(event.Type), "namespace", namespace.Name)
			if event.Type == watch.Deleted {
				deleted++
			}
			if deleted == toDelete {
				return nil
			}
		}
	}
}
