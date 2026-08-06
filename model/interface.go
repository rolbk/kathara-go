package model

import (
	"regexp"
	"strconv"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// macAddressRegex is `MAC_ADDRESS_REGEX` (`model/Interface.py:7`): exactly six
// colon-separated hex octets, no dashes and no dots.
//
// The `\n?$` tail is not decoration. Python's `$` also matches immediately
// before a trailing newline, so `re.match(MAC_ADDRESS_REGEX,
// "00:00:00:00:00:01\n")` succeeds on the oracle; Go's `$` is end-of-text only,
// and without the tail a MAC carrying a trailing newline — reachable through
// the API, which never strips — would be rejected here and accepted there.
var macAddressRegex = regexp.MustCompile(`^([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\n?$`)

// Interface is one device network interface: the edge between a [Machine] and a
// [Link] (`model/Interface.py`), in the shape PORT_SPEC §4.1 froze.
//
// A zero Link marks a tombstone rather than an interface; see
// [Machine.Interfaces].
type Interface struct {
	// Machine is the device the interface belongs to. It is `Interface.machine`
	// in Python, a public attribute the error messages read.
	Machine *Machine

	// Link is the attached collision domain — and nil in a tombstone slot, the
	// entry [Machine.RemoveInterface] leaves behind (NILABILITY.tsv:7).
	Link *Link

	// Number is the interface number, i.e. the `N` of `pc1[N]=A` and the `N` of
	// `ethN` inside the device.
	Number int

	// MAC is the hardware address, empty when the backend derives one.
	//
	// Python's guard is `if self.mac_address and not …match`, so an empty
	// string skips validation and is stored (NILABILITY.tsv:20,
	// `model/Interface.py:30`). "" and None are therefore the same thing here,
	// and both mean "generate".
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

// AddInterfaceOptions carries the two optional arguments of
// [Machine.AddInterface] (PORT_SPEC §4.1).
type AddInterfaceOptions struct {
	// Number is the interface number to claim. nil auto-assigns, and 0 is a
	// real number that must not be conflated with it (NILABILITY.tsv:5).
	//
	// Auto-assignment is `len(self.interfaces)` — the count of slots, tombstones
	// included — and not "the first free number", despite the docstring saying
	// so (ORDERING.tsv `model/Machine.py:102`).
	Number *int

	// MAC is the hardware address; empty derives one at deploy time.
	MAC string
}

// InterfaceNumber is a helper for filling [AddInterfaceOptions.Number].
func InterfaceNumber(n int) *int { return &n }
