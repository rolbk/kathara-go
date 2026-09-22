package model

import (
	"errors"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

func TestInterfaceConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		number  int
		mac     string
		wantErr bool
	}{
		// test_normal_iface / test_iface_num / test_iface_mac_address /
		// test_iface_num_and_mac_address.
		{name: "no mac", number: 0},
		{name: "number 2", number: 2},
		{name: "valid mac", number: 0, mac: "00:00:00:00:00:01"},
		{name: "valid mac upper", number: 2, mac: "AA:BB:CC:DD:EE:FF"},
		// The empty MAC skips validation entirely: Python guards with
		// `if self.mac_address and not …match` (model/Interface.py:30), so ""
		// is stored unchecked (oracle: `mac '' => OK ''`).
		{name: "empty mac skips validation", number: 0, mac: ""},
		// Python's `$` also matches before a trailing newline, so this one is
		// accepted on the oracle and must be accepted here.
		{name: "trailing newline accepted", number: 0, mac: "00:00:00:00:00:01\n"},
		// test_iface_mac_address_error_* .
		{name: "five octets", number: 0, mac: "00:00:00:00:00", wantErr: true},
		{name: "seven octets", number: 0, mac: "00:00:00:00:00:01:02", wantErr: true},
		{name: "dash separator", number: 0, mac: "00-00-00-00-00-01", wantErr: true},
		{name: "invalid hex", number: 0, mac: "gg:00:00:00:00:01", wantErr: true},
		{name: "single hex digit octet", number: 0, mac: "f:00:00:00:00:01", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lab := NewLab("test_lab", DefaultDefaults())
			machine, err := lab.NewMachine("test_machine", nil)
			if err != nil {
				t.Fatalf("NewMachine: %v", err)
			}
			link := lab.GetOrNewLink("A")

			iface, err := machine.AddInterface(link, AddInterfaceOptions{
				Number: InterfaceNumber(tt.number),
				MAC:    tt.mac,
			})
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrInterfaceMacAddress) {
					t.Fatalf("AddInterface(%q) error = %v, want ErrInterfaceMacAddress", tt.mac, err)
				}
				// The rejected interface never reaches the device.
				if machine.HasInterfaceNumber(tt.number) {
					t.Errorf("slot %d taken after a rejected MAC", tt.number)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddInterface(%q): %v", tt.mac, err)
			}
			if iface.Number != tt.number || iface.MAC != tt.mac {
				t.Errorf("interface = {%d, %q}, want {%d, %q}", iface.Number, iface.MAC, tt.number, tt.mac)
			}
			if iface.Link != link || iface.Machine != machine {
				t.Error("interface does not point back at its link and device")
			}
		})
	}
}

// TestInterfaceMacErrorMessage pins the frozen text of exceptions.py:123 as the
// oracle renders it: `MAC address 00:00:00:00:00 on interface `0` of device
// `pc1` is invalid.` — the MAC itself carries no backticks.
func TestInterfaceMacErrorMessage(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}

	_, err = machine.AddInterface(lab.GetOrNewLink("A"), AddInterfaceOptions{MAC: "00:00:00:00:00"})
	want := "MAC address 00:00:00:00:00 on interface `0` of device `pc1` is invalid."
	if err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}
}

// TestInterfaceString pins `Interface.__repr__`, which the debug line of
// Machine.check() interpolates. Oracle: `Interface(pc1, 1, None)` for an unset
// MAC.
func TestInterfaceString(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	link := lab.GetOrNewLink("A")

	plain, err := machine.AddInterface(link, AddInterfaceOptions{})
	if err != nil {
		t.Fatalf("AddInterface: %v", err)
	}
	if got, want := plain.String(), "Interface(pc1, 0, None)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	withMAC, err := machine.AddInterface(lab.GetOrNewLink("B"), AddInterfaceOptions{MAC: "00:00:00:00:00:01"})
	if err != nil {
		t.Fatalf("AddInterface: %v", err)
	}
	if got, want := withMAC.String(), "Interface(pc1, 1, 00:00:00:00:00:01)"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
