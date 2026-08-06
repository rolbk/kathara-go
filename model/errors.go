package model

import (
	"errors"

	"github.com/KatharaFramework/kathara-go/internal/util"
)

// PyRuntimeError is a Python exception class that the ERROR_CODES.md registry
// does not carry: the builtins §1.2 buckets into `InternalError` —
// `TypeError`, `AttributeError`, `KeyError`, `OverflowError` — plus
// pyfilesystem's `CreateFailed`, which `open_fs("osfs://…")` raises for a
// missing directory. They are the classes 3.8.3 reaches by crashing rather than
// by raising deliberately.
//
// They are ported because the crashes are observable — a tombstoned interface
// makes `lstart` die with `AttributeError: 'NoneType' object has no attribute
// 'link'`, and a `bridged_iface` set from lab.conf makes it die with a
// TypeError from `sorted()` (DIVERGENCES.md 1, vector
// `labconf/bridged_iface_with_interface`) — but they are returned, never
// panicked: PORT_SPEC §10 forbids a panic on any Python-reachable path, and
// these paths run inside the deploy fan-out.
//
// At the CLI boundary they carry no code and fall into `InternalError`
// (ERROR_CODES.md §1.4). Class is kept so the Layer B vector runner can compare
// against Python's `type(e).__name__`.
type PyRuntimeError struct {
	// Class is Python's exception class name: "TypeError", "AttributeError" or
	// "KeyError".
	Class string
	// Msg is `str(e)`, byte-for-byte.
	Msg string
}

func (e *PyRuntimeError) Error() string { return e.Msg }

// Is lets callers test for the class without matching on the message.
func (e *PyRuntimeError) Is(target error) bool {
	switch target {
	case ErrPyTypeError:
		return e.Class == "TypeError"
	case ErrPyAttributeError:
		return e.Class == "AttributeError"
	case ErrPyKeyError:
		return e.Class == "KeyError"
	}
	return false
}

// The three classes a caller has a reason to test for, as sentinels for
// errors.Is. They are not part of the ERROR_CODES.md registry — a value
// carrying one is an InternalError there — and exist so that a caller can tell
// a ported crash from a taxonomy error.
var (
	// ErrPyTypeError is Python's TypeError.
	ErrPyTypeError = errors.New("model: python TypeError")
	// ErrPyAttributeError is Python's AttributeError.
	ErrPyAttributeError = errors.New("model: python AttributeError")
	// ErrPyKeyError is Python's KeyError.
	ErrPyKeyError = errors.New("model: python KeyError")
)

func newTypeError(msg string) error { return &PyRuntimeError{Class: "TypeError", Msg: msg} }

// newNoneAttributeError is the crash a tombstoned interface causes:
// `interface.link` where interface is None (`model/Lab.py:206,229,334`).
func newNoneAttributeError(attr string) error {
	return &PyRuntimeError{
		Class: "AttributeError",
		Msg:   "'NoneType' object has no attribute '" + attr + "'",
	}
}

// newLowerAttributeError is `strtobool(x)` with a non-str x, which dies on
// `x.lower()` inside `is_ipv6_enabled`'s try block — and is not caught there,
// because the handler only takes ValueError (`model/Machine.py:610-618`).
func newLowerAttributeError(typeName string) error {
	return &PyRuntimeError{
		Class: "AttributeError",
		Msg:   "'" + typeName + "' object has no attribute 'lower'",
	}
}

// newKeyError is `del d[k]` on a missing key. Reachable in `remove_machine`
// only through the cross-lab hole in `connect_machine_obj_to_link`, which does
// not check that the device belongs to the scenario (`model/Lab.py:334`).
func newKeyError(key string) error {
	return &PyRuntimeError{Class: "KeyError", Msg: util.PythonRepr(key)}
}

// ErrBridgedIfaceType is the TypeError `Machine.check()` raises when meta
// `bridged_iface` holds a string: it is appended to a list of int interface
// numbers and `sort()` then compares the two (`model/Machine.py:370-371`).
//
// Every lab.conf that sets `bridged_iface` hits this, because the lab.conf
// parser stores every meta as a string — there is no lab.conf that sets it and
// parses (DIVERGENCES.md 1, vector `labconf/bridged_iface_with_interface`).
var ErrBridgedIfaceType = newBridgedIfaceTypeError("str")

// newBridgedIfaceTypeError is that TypeError for any unorderable type. The
// message names the stored type, so a list `bridged_iface` — which only the API
// can put there — reports `'list' and 'int'` and not `'str' and 'int'`
// (oracle-verified).
func newBridgedIfaceTypeError(typeName string) *PyRuntimeError {
	return &PyRuntimeError{
		Class: "TypeError",
		Msg:   "'<' not supported between instances of '" + typeName + "' and 'int'",
	}
}

// errPyIntNaN is `int(float('nan'))`, a ValueError. Both of its call sites
// (`get_cpu`, `get_num_terms`) catch ValueError and report their own
// MachineOptionError, so the text never reaches a user.
var errPyIntNaN = errors.New("model: cannot convert float NaN to integer")

// errPyIntInf is `int(float('inf'))`, an OverflowError — which `get_cpu` does
// NOT catch, so `pc1[cpus]=inf` crashes 3.8.3 (oracle-verified).
var errPyIntInf = &PyRuntimeError{
	Class: "OverflowError",
	Msg:   "cannot convert float infinity to integer",
}
