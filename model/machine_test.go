package model

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// newTestMachine is the fixture of tests/model/machine_test.py:
// `Machine(Lab("test_lab"), "test_machine")`.
func newTestMachine(t *testing.T) (*Lab, *Machine) {
	t.Helper()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("test_machine", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	return lab, machine
}

func TestDefaultDeviceParameters(t *testing.T) {
	t.Parallel()

	lab, machine := newTestMachine(t)

	if machine.Name != "test_machine" {
		t.Errorf("Name = %q", machine.Name)
	}
	if len(machine.Interfaces()) != 0 {
		t.Errorf("Interfaces() = %v, want empty", machine.Interfaces())
	}
	if machine.APIObject != nil {
		t.Error("APIObject is not nil")
	}
	if machine.FS != nil {
		t.Error("FS is not nil for a device with no directory")
	}
	if lab.HasHostPath() {
		t.Error("an in-memory scenario reports a host path")
	}

	// The six containers exist and are empty; every scalar is absent.
	if machine.Meta.ExecCommands == nil || len(machine.Meta.ExecCommands) != 0 {
		t.Errorf("ExecCommands = %v, want empty non-nil", machine.Meta.ExecCommands)
	}
	for name, length := range map[string]int{
		"sysctls": machine.Meta.Sysctls.Len(),
		"envs":    machine.Meta.Envs.Len(),
		"ports":   machine.Meta.Ports.Len(),
		"ulimits": machine.Meta.Ulimits.Len(),
		"volumes": machine.Meta.Volumes.Len(),
		"extras":  machine.Meta.Extras.Len(),
	} {
		if length != 0 {
			t.Errorf("meta %s has %d entries, want 0", name, length)
		}
	}
	if got := machine.Meta.Scalars(); len(got) != 0 {
		t.Errorf("Scalars() = %v, want none", got)
	}
}

// TestMachineNameValidation pins model/Machine.py:59-62. Every row was run
// against the oracle: ` pc1 ` strips to `pc1`, `PC1`, `pc-1`, `pc.1`, the empty
// name and a 31-character name are all SyntaxError, `_x` and a 30-character
// name are fine.
func TestMachineNameValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "pc1", want: "pc1"},
		{in: " pc1 ", want: "pc1"},
		{in: "pc1\n", want: "pc1"},
		// Python's str.strip() also removes 0x1C-0x1F, which unicode.IsSpace
		// does not (oracle: '\x1cpc1' => 'pc1').
		{in: "\x1cpc1", want: "pc1"},
		{in: " pc1 ", want: "pc1"},
		{in: "_x", want: "_x"},
		{in: strings.Repeat("a", 30), want: strings.Repeat("a", 30)},
		{in: strings.Repeat("a", 31), wantErr: true},
		{in: "PC1", wantErr: true},
		{in: "pc-1", wantErr: true},
		{in: "pc.1", wantErr: true},
		{in: "", wantErr: true},
		// U+200B is not whitespace in Python either, so it survives the strip
		// and fails the ASCII class (oracle-verified).
		{in: "pc1\u200b", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()

			lab := NewLab("test_lab", DefaultDefaults())
			machine, err := lab.NewMachine(tt.in, nil)
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrSyntax) {
					t.Fatalf("NewMachine(%q) error = %v, want ErrSyntax", tt.in, err)
				}
				// The message quotes the STRIPPED name, which is what Python
				// interpolates after `name = name.strip()`.
				want := "Invalid device name `" + pyStrip(tt.in) + "`."
				if err.Error() != want {
					t.Errorf("message = %q, want %q", err.Error(), want)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewMachine(%q): %v", tt.in, err)
			}
			if machine.Name != tt.want {
				t.Errorf("Name = %q, want %q", machine.Name, tt.want)
			}
		})
	}
}

func TestMachineReservedNamesAreNotChecked(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	for _, name := range []string{"shared", "_test"} {
		if _, err := lab.NewMachine(name, nil); err != nil {
			t.Errorf("NewMachine(%q) = %v, want no error", name, err)
		}
	}
}

func TestAddInterface(t *testing.T) {
	t.Parallel()

	t.Run("auto number", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		iface, err := machine.AddInterface(lab.GetOrNewLink("A"), AddInterfaceOptions{})
		if err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		if iface.Number != 0 || iface.Link.Name != "A" {
			t.Errorf("interface = {%d, %s}, want {0, A}", iface.Number, iface.Link.Name)
		}
		stored, ok := machine.Interface(0)
		if !ok || stored != iface {
			t.Errorf("Interface(0) = %v, %v; want the returned interface", stored, ok)
		}
	})

	t.Run("explicit number is sparse", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if _, err := machine.AddInterface(lab.GetOrNewLink("A"), AddInterfaceOptions{
			Number: InterfaceNumber(2),
		}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		// test_add_interface_with_number: stored at slot 2, len == 1. Sparse
		// numbering is representable in the model and rejected only by Check.
		if got := machine.Interfaces(); len(got) != 1 || got[0].Number != 2 {
			t.Errorf("Interfaces() = %v, want one entry numbered 2", got)
		}
	})

	t.Run("duplicate number", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if _, err := machine.AddInterface(lab.GetOrNewLink("A"), AddInterfaceOptions{}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		_, err := machine.AddInterface(lab.GetOrNewLink("B"), AddInterfaceOptions{
			Number: InterfaceNumber(0),
		})
		if !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
			t.Fatalf("error = %v, want ErrMachineCollisionDomain", err)
		}

		if want := "Interface 0 already set on device `test_machine`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("same collision domain twice", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)
		link := lab.GetOrNewLink("A")

		if _, err := machine.AddInterface(link, AddInterfaceOptions{}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		_, err := machine.AddInterface(link, AddInterfaceOptions{})
		if !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
			t.Fatalf("error = %v, want ErrMachineCollisionDomain", err)
		}
		if want := "Device `test_machine` is already connected to collision domain `A`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("link back reference", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)
		link := lab.GetOrNewLink("A")

		if _, err := machine.AddInterface(link, AddInterfaceOptions{}); err != nil {
			t.Fatalf("AddInterface: %v", err)
		}
		if !link.HasMachine(machine.Name) {
			t.Error("link does not know about the device")
		}
		if got := link.MachineNames(); len(got) != 1 || got[0] != "test_machine" {
			t.Errorf("link.MachineNames() = %v", got)
		}
	})
}

func TestAddInterfaceAutoNumberCountsSlots(t *testing.T) {
	t.Parallel()

	t.Run("sparse", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{})
		mustAdd(t, machine, lab.GetOrNewLink("B"), AddInterfaceOptions{Number: InterfaceNumber(5)})
		iface := mustAdd(t, machine, lab.GetOrNewLink("C"), AddInterfaceOptions{})

		if iface.Number != 2 {
			t.Errorf("auto number = %d, want 2 (len of {0,5})", iface.Number)
		}
	})

	t.Run("after remove", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		for _, name := range []string{"A", "B", "C"} {
			mustAdd(t, machine, lab.GetOrNewLink(name), AddInterfaceOptions{})
		}
		if err := machine.RemoveInterface(lab.GetOrNewLink("C")); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		iface := mustAdd(t, machine, lab.GetOrNewLink("D"), AddInterfaceOptions{})
		if iface.Number != 3 {
			t.Errorf("auto number = %d, want 3 (slot 2 stays taken)", iface.Number)
		}
	})
}

// TestInterfaceSlotOrderingIsTotal pins the comparator of the binary search
// against overflow. Python's dict keys any int, and `add_interface(number=-1)`
// is a legal API call, so the ordered slice must stay sorted for numbers whose
// difference does not fit in an int.
func TestInterfaceSlotOrderingIsTotal(t *testing.T) {
	t.Parallel()
	lab, machine := newTestMachine(t)

	numbers := []int{math.MaxInt, -1, math.MinInt, 0}
	for i, number := range numbers {
		mustAdd(t, machine, lab.GetOrNewLink(string(rune('A'+i))),
			AddInterfaceOptions{Number: InterfaceNumber(number)})
	}

	got := machine.Interfaces()
	for i := 1; i < len(got); i++ {
		if got[i-1].Number >= got[i].Number {
			t.Fatalf("interfaces are not sorted: %d then %d", got[i-1].Number, got[i].Number)
		}
	}
	for _, number := range numbers {
		if !machine.HasInterfaceNumber(number) {
			t.Errorf("HasInterfaceNumber(%d) = false; the slot is taken", number)
		}
	}
	// The duplicate guard is Python's `number in self.interfaces`, which a
	// search over an unsorted slice would answer wrongly.
	if _, err := machine.AddInterface(lab.GetOrNewLink("Z"),
		AddInterfaceOptions{Number: InterfaceNumber(math.MaxInt)}); !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
		t.Errorf("re-adding MaxInt = %v, want ErrMachineCollisionDomain", err)
	}
}

func TestRemoveInterface(t *testing.T) {
	t.Parallel()

	t.Run("slot survives as a tombstone", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)
		link := lab.GetOrNewLink("A")

		mustAdd(t, machine, link, AddInterfaceOptions{})
		if err := machine.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		slots := machine.Interfaces()
		if len(slots) != 1 {
			t.Fatalf("Interfaces() = %v, want one (tombstoned) slot", slots)
		}
		if !slots[0].IsTombstone() || slots[0].Number != 0 {
			t.Errorf("slot = %+v, want a tombstone numbered 0", slots[0])
		}
		if _, ok := machine.Interface(0); ok {
			t.Error("Interface(0) reports a live interface on a tombstone")
		}
		if !machine.HasInterfaceNumber(0) {
			t.Error("HasInterfaceNumber(0) is false: the number must stay taken")
		}
		if link.HasMachine(machine.Name) {
			t.Error("the device is still attached to the collision domain")
		}
	})

	t.Run("only the removed slot is nulled", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		for _, name := range []string{"A", "B", "C"} {
			mustAdd(t, machine, lab.GetOrNewLink(name), AddInterfaceOptions{})
		}
		if err := machine.RemoveInterface(lab.GetOrNewLink("A")); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		slots := machine.Interfaces()
		if len(slots) != 3 {
			t.Fatalf("Interfaces() = %v, want three slots", slots)
		}
		if !slots[0].IsTombstone() {
			t.Error("slot 0 is not a tombstone")
		}
		if slots[1].IsTombstone() || slots[1].Link.Name != "B" {
			t.Errorf("slot 1 = %+v, want B", slots[1])
		}
		if slots[2].IsTombstone() || slots[2].Link.Name != "C" {
			t.Errorf("slot 2 = %+v, want C", slots[2])
		}
	})

	t.Run("not connected", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		err := machine.RemoveInterface(lab.GetOrNewLink("A"))
		if !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
			t.Fatalf("error = %v, want ErrMachineCollisionDomain", err)
		}
		if want := "Device `test_machine` is not connected to collision domain `A`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("removing twice fails", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)
		link := lab.GetOrNewLink("A")

		mustAdd(t, machine, link, AddInterfaceOptions{})
		if err := machine.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}
		if err := machine.RemoveInterface(link); !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
			t.Errorf("second RemoveInterface = %v, want ErrMachineCollisionDomain", err)
		}
	})

	t.Run("tombstoned slot cannot be reclaimed", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)
		link := lab.GetOrNewLink("A")

		mustAdd(t, machine, link, AddInterfaceOptions{})
		if err := machine.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		// Oracle: `re-add on tombstoned slot => MachineCollisionDomainError:
		// Interface 0 already set on device `pc1`.`
		_, err := machine.AddInterface(link, AddInterfaceOptions{Number: InterfaceNumber(0)})
		if !errors.Is(err, kerrors.ErrMachineCollisionDomain) {
			t.Fatalf("error = %v, want ErrMachineCollisionDomain", err)
		}
	})
}

func TestCheck(t *testing.T) {
	t.Parallel()

	t.Run("sequential", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(1)})
		mustAdd(t, machine, lab.GetOrNewLink("B"), AddInterfaceOptions{Number: InterfaceNumber(0)})

		if err := machine.Check(); err != nil {
			t.Fatalf("Check: %v", err)
		}
		if got := machine.Interfaces(); got[0].Number != 0 || got[1].Number != 1 {
			t.Errorf("interfaces are not in numeric order: %v", got)
		}
	})

	t.Run("gap", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(2)})
		mustAdd(t, machine, lab.GetOrNewLink("B"), AddInterfaceOptions{Number: InterfaceNumber(4)})

		err := machine.Check()
		if !errors.Is(err, kerrors.ErrNonSequentialMachineInterface) {
			t.Fatalf("error = %v, want ErrNonSequentialMachineInterface", err)
		}
		// The FIRST missing index is the one reported.
		if want := "Interface `0` missing on device `test_machine`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("bridged_iface int fills the hole", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(0)})
		mustAdd(t, machine, lab.GetOrNewLink("B"), AddInterfaceOptions{Number: InterfaceNumber(2)})
		machine.Meta.BridgedIface = Int(1)

		if err := machine.Check(); err != nil {
			t.Fatalf("Check: %v", err)
		}
		// The bridged number is not turned into an interface.
		if got := machine.Interfaces(); len(got) != 2 {
			t.Errorf("Interfaces() = %v, want two", got)
		}
	})

	t.Run("bridged_iface string is fatal", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(0)})
		// This is what every lab.conf produces: add_meta stores the raw string.
		if _, _, err := machine.AddMeta("bridged_iface", "1"); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}

		err := machine.Check()
		if !errors.Is(err, ErrPyTypeError) {
			t.Fatalf("error = %v, want a TypeError", err)
		}
		// Vector labconf/bridged_iface_with_interface.
		if want := "'<' not supported between instances of 'str' and 'int'"; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("bridged_iface float is not truncated", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(0)})
		// int(1.5) would be 1 and would complete the sequence; Python keeps the
		// fraction through `sort()` and 1.5 is not slot 1 (oracle-verified).
		machine.Meta.BridgedIface = Float(1.5)

		err := machine.Check()
		if !errors.Is(err, kerrors.ErrNonSequentialMachineInterface) {
			t.Fatalf("error = %v, want ErrNonSequentialMachineInterface", err)
		}
		if want := "Interface `1` missing on device `test_machine`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}

		// An integral float DOES fill the slot, as `1.0 != 1` is false.
		machine.Meta.BridgedIface = Float(1.0)
		if err := machine.Check(); err != nil {
			t.Errorf("Check with bridged_iface 1.0 = %v, want nil", err)
		}
	})

	t.Run("bridged_iface NaN and infinity reach the sequence check", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(0)})

		// `int()` would raise a ValueError on the NaN and an OverflowError on
		// the infinity; Python's `sort()` raises neither — the NaN compares
		// false against everything and stays put, the infinity sorts to the end.
		for _, value := range []float64{math.NaN(), math.Inf(1)} {
			machine.Meta.BridgedIface = Float(value)
			err := machine.Check()
			if !errors.Is(err, kerrors.ErrNonSequentialMachineInterface) {
				t.Fatalf("bridged_iface %v: error = %v, want ErrNonSequentialMachineInterface", value, err)
			}
			if want := "Interface `1` missing on device `test_machine`."; err.Error() != want {
				t.Errorf("bridged_iface %v: message = %q, want %q", value, err.Error(), want)
			}
		}

		// A negative infinity sorts to the FRONT, so slot 0 is the one reported.
		machine.Meta.BridgedIface = Float(math.Inf(-1))
		err := machine.Check()
		if want := "Interface `0` missing on device `test_machine`."; err == nil || err.Error() != want {
			t.Errorf("bridged_iface -inf: error = %v, want %q", err, want)
		}
	})

	t.Run("bridged_iface list names its own type", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{Number: InterfaceNumber(0)})
		machine.Meta.BridgedIface = Strings([]string{"x"})

		err := machine.Check()
		if !errors.Is(err, ErrPyTypeError) {
			t.Fatalf("error = %v, want a TypeError", err)
		}
		if want := "'<' not supported between instances of 'list' and 'int'"; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("bridged_iface string with no interfaces", func(t *testing.T) {
		t.Parallel()
		_, machine := newTestMachine(t)

		if _, _, err := machine.AddMeta("bridged_iface", "1"); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}

		// A one-element sort never calls `<`, so Python gets past it and fails
		// the sequence check instead (vector labconf/bridged_iface_only).
		err := machine.Check()
		if !errors.Is(err, kerrors.ErrNonSequentialMachineInterface) {
			t.Fatalf("error = %v, want ErrNonSequentialMachineInterface", err)
		}
		if want := "Interface `0` missing on device `test_machine`."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("tombstones keep their number in the sequence", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		mustAdd(t, machine, lab.GetOrNewLink("A"), AddInterfaceOptions{})
		mustAdd(t, machine, lab.GetOrNewLink("B"), AddInterfaceOptions{})
		if err := machine.RemoveInterface(lab.GetOrNewLink("A")); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		if err := machine.Check(); err != nil {
			t.Errorf("Check: %v", err)
		}
	})
}

func TestMachineAccessors(t *testing.T) {
	t.Parallel()

	t.Run("image", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if got := machine.GetImage(); got != "kathara/base" {
			t.Errorf("GetImage() = %q, want the settings default", got)
		}
		machine.Meta.Image = Str("kathara/frr")
		if got := machine.GetImage(); got != "kathara/frr" {
			t.Errorf("GetImage() = %q, want the device meta", got)
		}
		lab.AddGlobalMachineMetadata("image", Str("kathara/global"))
		if got := machine.GetImage(); got != "kathara/global" {
			t.Errorf("GetImage() = %q, want the scenario-wide value", got)
		}
	})

	t.Run("mem", func(t *testing.T) {
		t.Parallel()

		// Oracle P3: int() semantics on the number, lower-cased unit, bare
		// numbers default to megabytes, "" is returned unchanged.
		tests := []struct {
			in      string
			want    string
			wantErr bool
		}{
			{in: "064m", want: "64m"},
			{in: "100", want: "100m"},
			{in: " 12 ", want: "12m"},
			{in: "0", want: "0m"},
			{in: "", want: ""},
			{in: "12K", want: "12k"},
			{in: "12G", want: "12g"},
			{in: "12B", want: "12b"},
			{in: "+5m", want: "5m"},
			{in: "5 m", want: "5m"},
			{in: "-5m", want: "-5m"},
			{in: "١٢m", want: "12m"},
			{in: "999999999999999999999999999999m", want: "999999999999999999999999999999m"},
			{in: "1e3", wantErr: true},
			{in: "12x", wantErr: true},
			{in: "12.5", wantErr: true},
			{in: "0x10", wantErr: true},
		}

		for _, tt := range tests {
			_, machine := newTestMachine(t)
			machine.Meta.Mem = Str(tt.in)

			got, err := machine.GetMem()
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Errorf("GetMem(%q) error = %v, want ErrMachineOption", tt.in, err)
					continue
				}
				if want := "Memory value not valid on `test_machine`."; err.Error() != want {
					t.Errorf("GetMem(%q) message = %q, want %q", tt.in, err.Error(), want)
				}
				continue
			}
			if err != nil {
				t.Errorf("GetMem(%q): %v", tt.in, err)
				continue
			}
			if got != tt.want {
				t.Errorf("GetMem(%q) = %q, want %q", tt.in, got, tt.want)
			}
		}

		// Unset is the empty string, which every caller treats as "no limit".
		_, machine := newTestMachine(t)
		if got, err := machine.GetMem(); err != nil || got != "" {
			t.Errorf("GetMem(unset) = %q, %v", got, err)
		}

		for _, falsy := range []Scalar{Int(0), Bool(false), Float(0), Str(""), Strings(nil)} {
			_, machine := newTestMachine(t)
			machine.Meta.Mem = falsy
			if got, err := machine.GetMem(); err != nil || got != "" {
				t.Errorf("GetMem(%v) = %q, %v; want \"\"", falsy.Value(), got, err)
			}
		}
	})

	t.Run("mem precedence", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		machine.Meta.Mem = Str("200m")
		if got, _ := machine.GetMem(); got != "200m" {
			t.Errorf("GetMem() = %q, want the device meta", got)
		}
		lab.AddGlobalMachineMetadata("mem", Str("150m"))
		if got, _ := machine.GetMem(); got != "150m" {
			t.Errorf("GetMem() = %q, want the scenario-wide value", got)
		}
	})

	t.Run("cpu", func(t *testing.T) {
		t.Parallel()

		// Oracle P4 with multiplier 1 and 1e9 (what the Docker backend passes).
		tests := []struct {
			in         string
			want       int64
			wantScaled int64
			wantErr    bool
		}{
			{in: "0.5", want: 0, wantScaled: 500000000},
			{in: "2", want: 2, wantScaled: 2000000000},
			{in: "1e3", want: 1000, wantScaled: 1000000000000},
			{in: "  3 ", want: 3, wantScaled: 3000000000},
			{in: "-1.5", want: -1, wantScaled: -1500000000},
			{in: "١٢", want: 12, wantScaled: 12000000000},
			{in: "1_0", want: 10, wantScaled: 10000000000},
			{in: "abc", wantErr: true},
			{in: "", wantErr: true},
			{in: "0x1", wantErr: true},
			{in: "nan", wantErr: true},
		}

		for _, tt := range tests {
			_, machine := newTestMachine(t)
			machine.Meta.CPUs = Str(tt.in)

			got, err := machine.GetCPU(1)
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Errorf("GetCPU(%q) error = %v, want ErrMachineOption", tt.in, err)
					continue
				}
				if want := "CPU value not valid on `test_machine`."; err.Error() != want {
					t.Errorf("GetCPU(%q) message = %q", tt.in, err.Error())
				}
				continue
			}
			if err != nil || got == nil {
				t.Errorf("GetCPU(%q) = %v, %v", tt.in, got, err)
				continue
			}
			if *got != tt.want {
				t.Errorf("GetCPU(%q) = %d, want %d", tt.in, *got, tt.want)
			}
			scaled, err := machine.GetCPU(1e9)
			if err != nil || scaled == nil || *scaled != tt.wantScaled {
				t.Errorf("GetCPU(%q, 1e9) = %v, %v; want %d", tt.in, scaled, err, tt.wantScaled)
			}
		}

		_, machine := newTestMachine(t)
		if got, err := machine.GetCPU(1); err != nil || got != nil {
			t.Errorf("GetCPU(unset) = %v, %v; want nil", got, err)
		}

		// An infinity is an uncaught OverflowError in Python, not a
		// MachineOptionError (oracle: get_cpu('infinity') => OverflowError).
		_, machine = newTestMachine(t)
		machine.Meta.CPUs = Str("infinity")
		if _, err := machine.GetCPU(1); err == nil ||
			err.Error() != "cannot convert float infinity to integer" {
			t.Errorf("GetCPU(infinity) = %v, want the OverflowError", err)
		}
	})

	t.Run("cpu precedence", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		machine.Meta.CPUs = Str("1")
		if got, _ := machine.GetCPU(1); got == nil || *got != 1 {
			t.Errorf("GetCPU() = %v, want the device meta", got)
		}
		lab.AddGlobalMachineMetadata("cpus", Str("2"))
		if got, _ := machine.GetCPU(1); got == nil || *got != 2 {
			t.Errorf("GetCPU() = %v, want the scenario-wide value", got)
		}
	})

	t.Run("num_terms", func(t *testing.T) {
		t.Parallel()

		// Oracle P5.
		tests := []struct {
			in       string
			want     int
			wantErr  bool
			negative bool
		}{
			{in: "2", want: 2},
			{in: "0", want: 0},
			{in: " 3 ", want: 3},
			{in: "٣", want: 3},
			{in: "1_0", want: 10},
			{in: "+4", want: 4},
			{in: "-1", negative: true},
			{in: "2.5", wantErr: true},
			{in: "", wantErr: true},
		}

		for _, tt := range tests {
			_, machine := newTestMachine(t)
			machine.Meta.NumTerms = Str(tt.in)

			got, err := machine.GetNumTerms()
			switch {
			case tt.negative:
				want := "Terminals Number value on `test_machine` must be a positive value or zero."
				if err == nil || err.Error() != want {
					t.Errorf("GetNumTerms(%q) = %v, want %q", tt.in, err, want)
				}
			case tt.wantErr:
				want := "Terminals Number value not valid on `test_machine`."
				if err == nil || err.Error() != want {
					t.Errorf("GetNumTerms(%q) = %v, want %q", tt.in, err, want)
				}
			default:
				if err != nil || got != tt.want {
					t.Errorf("GetNumTerms(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
				}
			}
		}

		_, machine := newTestMachine(t)
		if got, err := machine.GetNumTerms(); err != nil || got != 1 {
			t.Errorf("GetNumTerms(unset) = %d, %v; want 1", got, err)
		}
	})

	t.Run("num_terms precedence", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		machine.Meta.NumTerms = Str("1")
		lab.AddGlobalMachineMetadata("num_terms", Str("2"))
		// test_get_num_terms_mix: the scenario-wide value wins for every
		// device, even one that set its own.
		if got, err := machine.GetNumTerms(); err != nil || got != 2 {
			t.Errorf("GetNumTerms() = %d, %v; want 2", got, err)
		}
	})

	t.Run("shell", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if got := machine.GetShell(); got != "/bin/bash" {
			t.Errorf("GetShell() = %q, want the settings default", got)
		}
		// No scenario-wide lookup for this one (oracle P22).
		lab.AddGlobalMachineMetadata("shell", Str("/bin/zsh"))
		if got := machine.GetShell(); got != "/bin/bash" {
			t.Errorf("GetShell() = %q, want the default: shell ignores the global", got)
		}
		machine.Meta.Shell = Str("/bin/sh")
		if got := machine.GetShell(); got != "/bin/sh" {
			t.Errorf("GetShell() = %q, want the device meta", got)
		}
	})

	t.Run("privileged", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if machine.IsPrivileged() {
			t.Error("IsPrivileged() is true by default")
		}
		if _, _, err := machine.AddMeta("privileged", "True"); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}
		if !machine.IsPrivileged() {
			t.Error("IsPrivileged() is false after the device meta")
		}
		// test_is_privileged_mix, and the reverse: a scenario-wide False beats
		// a device-level True (oracle P22).
		lab.AddGlobalMachineMetadata("privileged", Bool(false))
		if machine.IsPrivileged() {
			t.Error("a scenario-wide false did not override the device meta")
		}
	})

	t.Run("bridged", func(t *testing.T) {
		t.Parallel()
		lab, machine := newTestMachine(t)

		if machine.IsBridged() {
			t.Error("IsBridged() is true by default")
		}
		// No scenario-wide lookup for this one either (oracle P22).
		lab.AddGlobalMachineMetadata("bridged", Bool(true))
		if machine.IsBridged() {
			t.Error("IsBridged() consulted the scenario-wide metadata")
		}
		if _, _, err := machine.AddMeta("bridged", "1"); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}
		if !machine.IsBridged() {
			t.Error("IsBridged() is false after the device meta")
		}
	})

	t.Run("ipv6", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name    string
			global  Scalar
			meta    Scalar
			want    bool
			wantErr bool
		}{
			{name: "default", want: false},
			{name: "meta bool", meta: Bool(true), want: true},
			{name: "meta string", meta: Str("True"), want: true},
			{name: "meta string false", meta: Str("false")},
			{name: "meta yes", meta: Str("yes"), want: true},
			{name: "global true", global: Bool(true), want: true},
			// test_is_ipv6_enabled_mix: a scenario-wide false beats the device.
			{name: "global false beats meta", global: Bool(false), meta: Bool(true)},
			{name: "invalid", meta: Str("maybe"), wantErr: true},
		}

		for _, tt := range tests {
			lab, machine := newTestMachine(t)
			if tt.global.IsSet() {
				lab.AddGlobalMachineMetadata("ipv6", tt.global)
			}
			machine.Meta.IPv6 = tt.meta

			got, err := machine.IsIPv6Enabled()
			if tt.wantErr {
				if !errors.Is(err, kerrors.ErrMachineOption) {
					t.Errorf("%s: error = %v, want ErrMachineOption", tt.name, err)
					continue
				}
				if want := "IPv6 value not valid on `test_machine`."; err.Error() != want {
					t.Errorf("%s: message = %q", tt.name, err.Error())
				}
				continue
			}
			if err != nil || got != tt.want {
				t.Errorf("%s: IsIPv6Enabled() = %v, %v; want %v", tt.name, got, err, tt.want)
			}
		}
	})

	t.Run("ipv6 falls back to the injected default", func(t *testing.T) {
		t.Parallel()

		defaults := DefaultDefaults()
		defaults.EnableIPv6 = true
		lab := NewLab("test_lab", defaults)
		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		if got, err := machine.IsIPv6Enabled(); err != nil || !got {
			t.Errorf("IsIPv6Enabled() = %v, %v; want true", got, err)
		}
		// A device-level false still wins over the setting.
		machine.Meta.IPv6 = Bool(false)
		if got, err := machine.IsIPv6Enabled(); err != nil || got {
			t.Errorf("IsIPv6Enabled() = %v, %v; want false", got, err)
		}
	})

	t.Run("ipv6 non-string non-bool matches Python exception behaviour", func(t *testing.T) {
		t.Parallel()
		_, machine := newTestMachine(t)

		machine.Meta.IPv6 = Int(1)
		_, err := machine.IsIPv6Enabled()
		// Oracle: `ipv6 meta=1 => AttributeError: 'int' object has no attribute
		// 'lower'`.
		if !errors.Is(err, ErrPyAttributeError) {
			t.Fatalf("error = %v, want an AttributeError", err)
		}
		if want := "'int' object has no attribute 'lower'"; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("volumes", func(t *testing.T) {
		t.Parallel()

		for _, policy := range []string{"Always", "Prompt", "Never"} {
			defaults := DefaultDefaults()
			defaults.VolumeMountPolicy = policy
			lab := NewLab("test_lab", defaults)
			machine, err := lab.NewMachine("pc1", nil)
			if err != nil {
				t.Fatalf("NewMachine: %v", err)
			}

			if got, err := machine.GetVolumes(); err != nil || got.Len() != 0 {
				t.Errorf("%s: GetVolumes() with no volumes = %v, %v", policy, got, err)
			}

			if _, _, err := machine.AddMeta("volume", "/h|/g"); err != nil {
				t.Fatalf("AddMeta: %v", err)
			}
			got, err := machine.GetVolumes()
			if policy == "Never" {
				if !errors.Is(err, kerrors.ErrMountDenied) {
					t.Errorf("%s: error = %v, want ErrMountDenied", policy, err)
				}
				if want := "Device `pc1` cannot mount volumes."; err == nil || err.Error() != want {
					t.Errorf("%s: message = %v, want %q", policy, err, want)
				}
				continue
			}
			if err != nil || got.Len() != 1 {
				t.Errorf("%s: GetVolumes() = %v, %v", policy, got, err)
			}
		}
	})

	t.Run("volumes honour the scenario option", func(t *testing.T) {
		t.Parallel()

		defaults := DefaultDefaults()
		defaults.VolumeMountPolicy = "Never"
		lab := NewLab("test_lab", defaults)
		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if _, _, err := machine.AddMeta("volume", "/h|/g"); err != nil {
			t.Fatalf("AddMeta: %v", err)
		}

		// Oracle: `policy Never but _mount_volumes True => OK {...}`.
		lab.AddOption("_mount_volumes", Bool(true))
		if got, err := machine.GetVolumes(); err != nil || got.Len() != 1 {
			t.Errorf("GetVolumes() = %v, %v; want the volume", got, err)
		}

		lab.AddOption("_mount_volumes", Bool(false))
		if _, err := machine.GetVolumes(); !errors.Is(err, kerrors.ErrMountDenied) {
			t.Errorf("GetVolumes() = %v, want ErrMountDenied", err)
		}
	})
}

// TestMachineString pins `Machine.__str__` against the oracle rendering,
// trailing space in the `Interfaces: ` header and Python tuple port keys
// included.
func TestMachineString(t *testing.T) {
	t.Parallel()

	lab := NewLab("mylab", DefaultDefaults())
	machine, _, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{MAC: "00:00:00:00:00:01"})
	if err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	if _, _, err := lab.ConnectMachineToLink("pc1", "B", AddInterfaceOptions{}); err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	for _, meta := range [][2]string{{"bridged", "false"}, {"sysctl", "net.a.b=1"}, {"port", "8080"}} {
		if _, _, err := machine.AddMeta(meta[0], meta[1]); err != nil {
			t.Fatalf("AddMeta(%q): %v", meta[0], err)
		}
	}

	want := "Name: pc1\nImage: kathara/base\nInterfaces: \n\t- 0: A (MAC Address: 00:00:00:00:00:01)" +
		"\n\t- 1: B\nBridged Connection: False\nSysctls:\n\t- net.a.b = 1" +
		"\nExposed Ports:\n\t- Host: (3000, 'tcp') -> Guest: 8080"
	if got := machine.String(); got != want {
		t.Errorf("String() =\n%q\nwant\n%q", got, want)
	}

	// Oracle: after removing interface 0 the header stays and only the live
	// interface is listed.
	if err := machine.RemoveInterface(lab.GetOrNewLink("A")); err != nil {
		t.Fatalf("RemoveInterface: %v", err)
	}
	want = "Name: pc1\nImage: kathara/base\nInterfaces: \n\t- 1: B\nBridged Connection: False" +
		"\nSysctls:\n\t- net.a.b = 1\nExposed Ports:\n\t- Host: (3000, 'tcp') -> Guest: 8080"
	if got := machine.String(); got != want {
		t.Errorf("String() after remove =\n%q\nwant\n%q", got, want)
	}
}

// TestMachineStringAllTombstones pins the oracle's odd but real output: the
// header is printed because the dict is non-empty, and nothing follows it.
func TestMachineStringAllTombstones(t *testing.T) {
	t.Parallel()

	lab, machine := newTestMachine(t)
	link := lab.GetOrNewLink("A")
	mustAdd(t, machine, link, AddInterfaceOptions{})
	if err := machine.RemoveInterface(link); err != nil {
		t.Fatalf("RemoveInterface: %v", err)
	}

	want := "Name: test_machine\nImage: kathara/base\nInterfaces: "
	if got := machine.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func mustAdd(t *testing.T, machine *Machine, link *Link, opts AddInterfaceOptions) Interface {
	t.Helper()

	iface, err := machine.AddInterface(link, opts)
	if err != nil {
		t.Fatalf("AddInterface(%s): %v", link.Name, err)
	}
	return iface
}
