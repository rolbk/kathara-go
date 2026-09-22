// This file preserves the Python exception behaviour for KeyErrors, TypeErrors,
// and AttributeErrors that 3.8.3 reaches on user-accessible paths.

package docker

import (
	"strconv"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// pyInt is Python's `int(s)` at the sites `DockerManager` converts a label or a
// payload string without a try, with Python's own ValueError when it fails.
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
// `machine.interfaces` is keyed by int and every other KeyError in this implementation is
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

// newPyAttributeError is the exception from `None.attr`: `first_network.name`
// when the collision domain was never created, or
// `machine_iface.link` on a tombstoned interface slot.
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
