// This file preserves Python exception behaviour and the `except ApiException`
// discrimination every Kubernetes call site performs.

package kubernetes

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// newPyKeyError is `d['k']` on a dict without that key: the repr of a str,
// which carries single quotes.
func newPyKeyError(key string) error {
	return &model.PyRuntimeError{Class: "KeyError", Msg: "'" + key + "'"}
}

func newPyAttributeError(attr string) error {
	return &model.PyRuntimeError{
		Class: "AttributeError",
		Msg:   "'NoneType' object has no attribute '" + attr + "'",
	}
}

func newPyTypeError(msg string) error {
	return &model.PyRuntimeError{Class: "TypeError", Msg: msg}
}

// newPySubscriptError is the exception from evaluating `x['k']` where x is
// None, as in `link.api_object["metadata"]["name"]` for a collision domain
// whose `api_object` is still None (`KubernetesMachine.py:492`).
func newPySubscriptError() error {
	return &model.PyRuntimeError{
		Class: "TypeError",
		Msg:   "'NoneType' object is not subscriptable",
	}
}

// ---------------------------------------------------------------------------
// ApiException discrimination
// ---------------------------------------------------------------------------

// apiStatusCode is `e.status` on a `kubernetes.client.rest.ApiException`: the
// HTTP status of the response the API server sent.
func apiStatusCode(err error) (int32, bool) {
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		return 0, false
	}
	return status.Status().Code, true
}

// isAPIException reports whether err is what Python's `except ApiException`
// would catch.
func isAPIException(err error) bool {
	_, ok := apiStatusCode(err)
	return ok
}

// isConflict is `e.status == 409 and 'Conflict' in e.reason`
// (`KubernetesMachine.py:367`), the test that turns a duplicate Deployment into
// `MachineAlreadyExistsError`.
func isConflict(err error) bool {
	code, ok := apiStatusCode(err)
	return ok && code == 409
}

// isForbidden is `e.status == 403 and 'Forbidden' in e.reason`
// (`KubernetesManager.py:142`), the test that turns the API server's refusal to
// create objects in a Terminating namespace into `LabAlreadyExistsError`. Same
// reasoning as [isConflict].
func isForbidden(err error) bool {
	code, ok := apiStatusCode(err)
	return ok && code == 403
}

func translateAPI(err error) error {
	switch {
	case err == nil:
		return nil
	case !isAPIException(err):
		return err
	case errors.Is(err, kerrors.ErrKubernetesAPI):
		return err
	}
	return kerrors.NewKubernetesAPI(err)
}
