package util

import (
	"errors"
)

// StrToBool is trdparty.strtobool.strtobool, the parser behind the boolean
// device metas: `pc1[ipv6]=true`, `--exec-print`, and every `add_meta` of a
// bool-typed key (Machine.py:159,168,616).
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
