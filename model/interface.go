package model

import (
	"regexp"
	"strconv"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// macAddressRegex is `MAC_ADDRESS_REGEX` (`model/Interface.py:7`): exactly six
// colon-separated hex octets, no dashes and no dots.
var macAddressRegex = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\n?$`)

type Interface struct {
	// Machine is the device the interface belongs to. It is `Interface.machine`
	// in Python, a public attribute the error messages read.
	Machine *Machine

	Link *Link

	// Number is the interface number, i.e. the `N` of `pc1[N]=A` and the `N` of
	// `ethN` inside the device.
	Number int

	// MAC is the hardware address, empty when the backend derives one.

	MAC string
}

// newInterface is `Interface.__init__`: it stores the four fields and then
// validates the MAC, so a rejected MAC never reaches the device's slot map.
func newInterface(machine *Machine, link *Link, number int, mac string) (Interface, error) {
	iface := Interface{Machine: machine, Link: link, Number: number, MAC: mac}
	if mac != "" && !macAddressRegex.MatchString(mac) {
		return Interface{}, kerrors.NewInterfaceMacAddress(mac, number, machine.Name)
	}
	return iface, nil
}

// IsTombstone reports whether this entry is a removed interface's slot rather
// than a live interface — Python's `None` value, which every iteration in the
// subsystem has to skip (`if interface:`, `model/Machine.py:825`).
func (i Interface) IsTombstone() bool { return i.Link == nil }

// String is `Interface.__repr__` (`model/Interface.py:33`), which is what the
// debug line of [Machine.Check] interpolates.
func (i Interface) String() string {
	name := ""
	if i.Machine != nil {
		name = i.Machine.Name
	}
	mac := i.MAC
	if mac == "" {
		// Python prints None for an unset MAC; "" and None are conflated in
		// the field, and None is the value every real caller stored.
		mac = "None"
	}
	return "Interface(" + name + ", " + strconv.Itoa(i.Number) + ", " + mac + ")"
}

type AddInterfaceOptions struct {
	// Number is the interface number to claim.

	Number *int

	// MAC is the hardware address; empty derives one at deploy time.
	MAC string
}

// InterfaceNumber is a helper for filling [AddInterfaceOptions.Number].
func InterfaceNumber(n int) *int { return &n }
