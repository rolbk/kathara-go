// This file holds two things: the Python crashes this backend reproduces, and
// the `except ApiException` discrimination every Kubernetes call site performs.
//
// The crashes are RETURNED, never panicked — PORT_SPEC §10 forbids a panic on
// any Python-reachable path, and several of these sit inside the deploy
// fan-out. At the CLI boundary they carry no taxonomy code and fall into
// `InternalError`, the exhaustive fallback of ERROR_CODES.md §1.4;
// [model.PyRuntimeError] is the shared carrier the `model` package already
// defined for exactly this, so a Layer B runner can compare against Python's
// `type(e).__name__`.

package kubernetes

import (
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// newPyKeyError is `d['k']` on a dict without that key: the repr of a str,
// which carries single quotes.
//
// One site can reach it: `get_lab_from_api` indexes
// `pod.metadata.annotations["k8s.v1.cni.cncf.io/networks"]`
// (`KubernetesManager.py:725`) and `lab_networks[network_conf['name']]`
// (`:726`) without a guard, so a pod this backend did not create — or one whose
// collision domain has already been deleted — crashes the reconstruction.
func newPyKeyError(key string) error {
	return &model.PyRuntimeError{Class: "KeyError", Msg: "'" + key + "'"}
}

// newPyAttributeError is `None.attr`. Its reachable site is
// `interface.link.api_object` on a tombstoned interface slot
// (`KubernetesMachine.py:487`, the annotation loop) and on a collision domain
// that was never deployed — the second of which is why links are deployed
// before machines (k8s-backend.md G2/O9).
func newPyAttributeError(attr string) error {
	return &model.PyRuntimeError{
		Class: "AttributeError",
		Msg:   "'NoneType' object has no attribute '" + attr + "'",
	}
}

// newPyTypeError is a CPython TypeError with its own message. The reachable
// site is `"sysctl -w -q %s=%d" % item` on a string-valued sysctl
// ([SysctlCommands], k8s-backend.md G9).
func newPyTypeError(msg string) error {
	return &model.PyRuntimeError{Class: "TypeError", Msg: msg}
}

// newPySubscriptError is `x['k']` where x is None: the crash
// `link.api_object["metadata"]["name"]` produces for a collision domain whose
// `api_object` is still None (`KubernetesMachine.py:492`).
//
// It is a different message from [newPyAttributeError] because Python reaches
// it by a different operator, and the two are told apart in a Layer B run.
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
//
// The second result is `isinstance(e, ApiException)`. client-go models the same
// thing as an error implementing [apierrors.APIStatus], which it builds for
// every non-2xx response and for nothing else — a dial failure or a context
// cancellation is a plain error and answers false, exactly as those are not
// `ApiException` in Python either.
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
//
// Python's `e.reason` is the HTTP *reason phrase* off the urllib3 response —
// "Conflict" for 409 — and NOT the `Status.reason` field of the API server's
// body, which for a duplicate create is "AlreadyExists". So the conjunction is
// really one condition tested twice, and the status code is the whole of it.
// Testing client-go's `apierrors.IsConflict` instead would be a DIFFERENT
// question: that one reads `Status.reason` and answers false for the
// "AlreadyExists" a duplicate create actually returns.
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

// translateAPI gives an `ApiException` that no rule matched the taxonomy code
// ERROR_CODES.md §1.3 assigns it: `KubernetesAPI`, human label `ApiException`.
//
// Python has no such call. It does not need one — every uncaught exception
// reaches `kathara.py:104`, which prints `({type(e).__name__}) {e}`, so an
// `ApiException` that nobody caught is ALREADY labelled `(ApiException)` on the
// way out. Go has to say it: an untranslated client-go error carries no code
// and would land in the `InternalError` fallback of §1.4, and the CLI cannot
// classify it itself because `isolation_test.go` forbids `k8s.io/…` outside this
// package. So the passthrough is applied here, at every call whose error Python
// lets escape uncaught — which is a superset of the three literal `raise e`
// sites §1.3 names in parentheses, because in Python a `raise e` and a call
// nobody wrapped in a `try` produce the same line.
//
// It does NOT apply to the calls Python swallows (`KubernetesConfigMap.py:52`,
// `KubernetesLink.py:193`, `KubernetesMachine.py:687`, `KubernetesNamespace.py:37,52`,
// `KubernetesSecret.py:65`): those never reach a user and are left alone.
//
// Three properties make it safe to apply anywhere:
//
//   - a non-`ApiException` — a dial failure, a cancelled context — is returned
//     untouched, exactly as Python's `except ApiException` would not catch it;
//   - the wrap keeps the cause reachable, so [isAPIException], [isConflict] and
//     [isForbidden] still answer the same question about the wrapped error and
//     an outer translation rule can still fire;
//   - it is idempotent, so a site that wraps and a caller that translates do not
//     nest.
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
