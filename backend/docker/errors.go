// This file holds the Python crashes this backend reproduces: the KeyErrors,
// TypeErrors and AttributeErrors 3.8.3 reaches on paths a user can get to.
//
// They are RETURNED, never panicked — PORT_SPEC §10 forbids a panic on any
// Python-reachable path, and every one of these sits inside the deploy fan-out.
// At the CLI boundary they carry no taxonomy code and fall into
// `InternalError`, the exhaustive fallback of ERROR_CODES.md §1.4;
// [model.PyRuntimeError] is the shared carrier the `model` package already
// defined for exactly this, so a Layer B runner can compare against Python's
// `type(e).__name__`.

package docker

import (
	"strconv"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// pyInt is Python's `int(s)` at the sites `DockerManager` converts a label or a
// payload string without a try, with Python's own ValueError when it fails.
//
// `strconv.Atoi` is NOT `int()`: CPython strips a documented set of surrounding
// whitespace, accepts a leading sign and underscores between digits, and reads
// any Unicode decimal digit; its failure message is `invalid literal for int()
// with base 10: '…'` with the argument's repr. [util.PyInt] is that acceptance
// and [util.PyIntFailure] that message. ERROR_CODES.md §1.2 buckets a bare
// ValueError to `Value`, which is what [kerrors.WrapValue] carries.
func pyInt(s string) (int, error) {
	value, err := util.PyInt(s)
	if err != nil {
		failure := util.PyIntFailure(err, s)
		return 0, kerrors.WrapValue(failure, failure.Error())
	}
	return value, nil
}

// newPyKeyErrorInt is `d[0]` on a dict without that key. CPython's KeyError
// carries `repr(key)`, and the repr of an int is its decimal with no quotes —
// `KeyError: 0`, not `KeyError: '0'`. The distinction matters because
// `machine.interfaces` is keyed by int and every other KeyError in the port is
// keyed by str.
func newPyKeyErrorInt(key int) error {
	return &model.PyRuntimeError{Class: "KeyError", Msg: strconv.Itoa(key)}
}

// newPyKeyError is `d['k']` on a dict without that key: the repr of a str,
// which carries single quotes. Every key this backend can miss is a plain
// ASCII identifier, so the repr is the name in quotes.
func newPyKeyError(key string) error {
	return &model.PyRuntimeError{Class: "KeyError", Msg: "'" + key + "'"}
}

// newPyAttributeError is `None.attr`, the crash an un-deployed api_object
// causes: `first_network.name` when the collision domain was never created,
// `machine_iface.link` on a tombstoned interface slot.
//
// CPython's message names the type and the attribute; the type is always
// NoneType at the sites this reproduces, because what is missing is always an
// api_object or an interface that was set to None.
func newPyAttributeError(attr string) error {
	return &model.PyRuntimeError{
		Class: "AttributeError",
		Msg:   "'NoneType' object has no attribute '" + attr + "'",
	}
}

// newPyTypeError is a CPython TypeError with its own message, used for
// `int(None)` on an exec that has not finished ([execStream.ExitCode]).
func newPyTypeError(msg string) error {
	return &model.PyRuntimeError{Class: "TypeError", Msg: msg}
}

// newPyIndexError is `list.pop()` on an empty list, which is how
// `DockerPlugin.check_and_download_plugin` fails for a plugin that has no
// `xtables_lock` mount (`DockerPlugin.py:68-69`). CPython's message is the
// whole of it: the exception carries no index.
func newPyIndexError(msg string) error {
	return &model.PyRuntimeError{Class: "IndexError", Msg: msg}
}
