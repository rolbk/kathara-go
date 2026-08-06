// This file is the port of `Kathara/trdparty/strtobool/strtobool.py`, vendored
// into utils' package because it is a leaf helper of the same shape
// (PACKAGE_GRAPH.md §2.1).

package util

import (
	"errors"
)

// StrToBool is trdparty.strtobool.strtobool, the parser behind the boolean
// device metas: `pc1[ipv6]=true`, `--exec-print`, and every `add_meta` of a
// bool-typed key (Machine.py:159,168,616).
//
// It is a vendored copy of the `distutils.util.strtobool` that Python 3.12
// deleted, which is why the accepted set is wider than any config format
// needs: y, yes, t, true, on and 1 are true; n, no, f, false, off and 0 are
// false; everything else is an error.
//
// The comparison is on `val.lower()`, so "TRUE" and "On" work — and the error
// message quotes the *lower-cased* value, not what the user typed, so
// `pc1[ipv6]=YEP` reports the value as `yep`. That is why the fold is
// [pyLower] and not [strings.ToLower]: the folded value is interpolated into
// frozen message text, where the two disagree for "İ" and for a final sigma.
//
// The returned error carries no kerrors class: `ERROR_CODES.md` gives this
// ValueError no code of its own, and the meta boundary that calls it decides
// how it surfaces (PACKAGE_GRAPH.md §2.5).
func StrToBool(val string) (bool, error) {
	val = pyLower(val)

	switch val {
	case "y", "yes", "t", "true", "on", "1":
		return true, nil
	case "n", "no", "f", "false", "off", "0":
		return false, nil
	}

	return false, errors.New("Invalid truth value `" + val + "`.")
}
