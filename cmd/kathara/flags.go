package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

var wordPattern = regexp.MustCompile(`^[\p{L}\p{N}_]+$`)

// stringList is a pflag value that accumulates the values of an option
// argparse declares with `nargs='+'` or `nargs='*'`.
type stringList struct {
	values  []string
	present bool
	// validate is the argparse `type=` callable, run here when a failure is
	// meant to be a usage error.
	validate func(string) error
}

func (l *stringList) String() string { return "[" + strings.Join(l.values, ",") + "]" }

func (l *stringList) Set(v string) error {
	l.present = true
	if v == emptyListSentinel {
		return nil
	}
	if l.validate != nil {
		if err := l.validate(v); err != nil {
			return err
		}
	}
	l.values = append(l.values, v)
	return nil
}

func (l *stringList) Type() string { return "stringList" }

func (l *stringList) Values() []string { return l.values }

// Present reports whether the option appeared at all, which is what tells
// argparse's `None` default from its empty list.
func (l *stringList) Present() bool { return l.present }

// tristate is a pflag value for argparse's `action='store_const'` options —
// `--terminals`/`--noterminals`, `--hosthome`/`--no-hosthome`,
// `--shared`/`--no-shared`, `--privileged`.
type tristate struct {
	value *bool
	// constant is the `const=` of the flag this value is bound to.
	constant bool
}

func (t *tristate) String() string {
	if t.value == nil {
		return "unset"
	}
	return fmt.Sprintf("%t", *t.value)
}

func (t *tristate) Set(string) error {
	v := t.constant
	t.value = &v
	return nil
}

func (t *tristate) Type() string { return "bool" }

// IsBoolFlag makes pflag accept the option with no value, which is what
// `action='store_const'` is.
func (t *tristate) IsBoolFlag() bool { return true }

// Get is the tri-state itself: nil for "the user did not say".
func (t *tristate) Get() *bool { return t.value }

// alphanumeric is `cli/ui/utils.alphanumeric`, the `type=` of `--rm` on
// `vconfig` and `lconfig`. Its `ArgumentTypeError` is a usage error, exit 2.
func alphanumeric(value string) error {
	if !wordPattern.MatchString(value) {
		return fmt.Errorf("invalid alphanumeric value")
	}
	return nil
}

// ethSpec is one parsed `--eth N:CD[/MAC]` value.
type ethSpec struct {
	// Number is the interface number **as written**. It stays a string
	// because `interface_cd_mac` never converts it: the `int()` happens in
	// `VstartCommand.run`, and its failure is a `SyntaxError` (exit 1), not an
	// `ArgumentTypeError` (exit 2).
	Number string
	// CD is the collision-domain name, already checked against `^\w+$`.
	CD string
	// MAC is the optional MAC address, empty when the value carried none.
	MAC string
}

// interfaceCDMAC is `cli/ui/utils.interface_cd_mac`, the `type=` of `--eth`.
func interfaceCDMAC(value string) (ethSpec, error) {
	invalid := fmt.Errorf("invalid interface definition: %s", value)

	parts := strings.Split(value, "/")
	head := strings.SplitN(parts[0], ":", -1)
	if len(head) != 2 {
		// `str.split(':')` returning anything but two parts is the
		// `(n, cd) = …` unpacking's ValueError.
		return ethSpec{}, invalid
	}
	spec := ethSpec{Number: head[0], CD: head[1]}
	if len(parts) == 2 {
		if parts[1] == "" {
			return ethSpec{}, invalid
		}
		spec.MAC = parts[1]
	}

	if !wordPattern.MatchString(spec.CD) {
		return ethSpec{}, fmt.Errorf(
			"invalid interface definition, collision domain `%s` contains non-alphanumeric characters", spec.CD)
	}
	return spec, nil
}

// validateEth is [interfaceCDMAC] reduced to its error, for use as a
// [stringList] validator.
func validateEth(value string) error {
	_, err := interfaceCDMAC(value)
	return err
}

// volumeSpec is `cli/ui/utils.volume`: the value is returned unchanged and only
// its shape is checked — two or three non-empty `|`-separated parts.
func volumeSpec(value string) error {
	n := 0
	for _, part := range strings.Split(value, "|") {
		if part != "" {
			n++
		}
	}
	if n != 2 && n != 3 {
		return fmt.Errorf("invalid volume definition: %s", value)
	}
	return nil
}

// cdMAC is `cli/ui/utils.cd_mac` → `utils.parse_cd_mac_address`, the `type=` of
// `--add`.
func cdMAC(value string) (cd, mac string, err error) {
	return util.ParseCDMACAddress(value)
}

// assert the shared taxonomy is the one `cdMAC` reports through.
var _ = kerrors.CodeSyntax
