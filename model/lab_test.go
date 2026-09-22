package model

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/vfs"
)

func TestLabConstruction(t *testing.T) {
	t.Parallel()

	t.Run("named, no path", func(t *testing.T) {
		t.Parallel()

		lab := NewLab("x", DefaultDefaults())
		if lab.Hash != "ndTkYSaMgDT1yFZOFVxnpg" {
			t.Errorf("Hash = %q, want the oracle's hash of the name", lab.Hash)
		}
		if lab.FSType() != "memory" {
			t.Errorf("FSType() = %q, want memory", lab.FSType())
		}
		if lab.HasHostPath() {
			t.Error("HasHostPath() is true for an in-memory scenario")
		}
		if lab.Description != "" || lab.Version != "" || lab.Author != "" ||
			lab.Email != "" || lab.Web != "" || lab.SharedPath != "" {
			t.Error("a fresh scenario carries metadata")
		}
		if len(lab.Machines()) != 0 || len(lab.Links()) != 0 || len(lab.GeneralOptions()) != 0 {
			t.Error("a fresh scenario is not empty")
		}
		if lab.HasDependencies {
			t.Error("HasDependencies is true on a fresh scenario")
		}
		if !lab.HasName() || lab.Name() != "x" {
			t.Errorf("Name() = %q, HasName() = %v", lab.Name(), lab.HasName())
		}
	})

	t.Run("empty name is a value", func(t *testing.T) {
		t.Parallel()

		// `Lab("")` hashes the empty string — a real, constant hash — and is
		// still a *named* scenario as far as the hash rule is concerned.
		lab := NewLab("", DefaultDefaults())
		if lab.Hash != "1B2M2Y8AsgTpgAmY7PhCfg" {
			t.Errorf("Hash = %q, want the oracle's hash of \"\"", lab.Hash)
		}
		if !lab.HasName() {
			t.Error("HasName() is false for an empty-but-set name")
		}
	})

	t.Run("path only", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if lab.Hash != util.GenerateURLSafeHash(dir) {
			t.Errorf("Hash = %q, want the hash of the path", lab.Hash)
		}
		if lab.HasName() {
			t.Error("HasName() is true for an unnamed scenario")
		}
		if lab.FSType() != "os" || !lab.HasHostPath() {
			t.Errorf("FSType() = %q, want os", lab.FSType())
		}
		if got, ok := lab.FSPath(); !ok || got != dir {
			t.Errorf("FSPath() = %q, %v; want %q", got, ok, dir)
		}
	})

	t.Run("name wins over path for the hash", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		lab, err := NewLabWithPath("x", dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabWithPath: %v", err)
		}
		if lab.Hash != NewLab("x", DefaultDefaults()).Hash {
			t.Errorf("Hash = %q, want the hash of the name", lab.Hash)
		}
		if !lab.HasHostPath() {
			t.Error("a scenario with a path has no host path")
		}
	})

	t.Run("empty path is the memory filesystem", func(t *testing.T) {
		t.Parallel()

		// `Lab(None, "")`: the truthiness test picks memory, the `is None` test
		// still hashes the (empty) path.
		lab, err := NewLabFromPath("", DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if lab.FSType() != "memory" {
			t.Errorf("FSType() = %q, want memory", lab.FSType())
		}
		if lab.Hash != "1B2M2Y8AsgTpgAmY7PhCfg" {
			t.Errorf("Hash = %q, want the hash of \"\"", lab.Hash)
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()

		// test_default_scenario_creation_with_non_existing_path: Python raises
		// fs.errors.CreateFailed from open_fs.
		_, err := NewLabFromPath(filepath.Join(t.TempDir(), "nope"), DefaultDefaults())
		if err == nil {
			t.Fatal("NewLabFromPath on a missing directory returned no error")
		}
		var runtimeErr *PyRuntimeError
		if !errors.As(err, &runtimeErr) || runtimeErr.Class != "CreateFailed" {
			t.Errorf("error = %v, want a CreateFailed", err)
		}
	})

	t.Run("renaming recomputes the hash", func(t *testing.T) {
		t.Parallel()

		// test_set_new_name, and the reason `lstart --name` works at all.
		lab := NewLab("x", DefaultDefaults())
		before := lab.Hash
		lab.SetName("y")
		if lab.Name() != "y" {
			t.Errorf("Name() = %q", lab.Name())
		}
		if lab.Hash == before || lab.Hash != "QVKQdpWURg4uSFkikE80XQ" {
			t.Errorf("Hash = %q, want the oracle's hash of \"y\"", lab.Hash)
		}
	})
}

func TestLabMachines(t *testing.T) {
	t.Parallel()

	t.Run("new and get", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		got, err := lab.GetMachine("pc1")
		if err != nil || got != machine {
			t.Errorf("GetMachine = %v, %v", got, err)
		}
		if !lab.HasMachine("pc1") || lab.HasMachine("pc2") {
			t.Error("HasMachine is wrong")
		}
		if !lab.HasMachines([]string{"pc1"}) || lab.HasMachines([]string{"pc1", "pc2"}) {
			t.Error("HasMachines is wrong")
		}
		// `all(())` is True.
		if !lab.HasMachines(nil) {
			t.Error("HasMachines(nil) is false")
		}
	})

	t.Run("duplicate", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		_, err := lab.NewMachine("pc1", nil)
		if !errors.Is(err, kerrors.ErrMachineAlreadyExists) {
			t.Fatalf("error = %v, want ErrMachineAlreadyExists", err)
		}
		if want := "Device with name `pc1` already exists."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		_, err := lab.GetMachine("z")
		if !errors.Is(err, kerrors.ErrMachineNotFound) {
			t.Fatalf("error = %v, want ErrMachineNotFound", err)
		}
		// No backticks in this one, unlike most of the catalog.
		if want := "Device z not in the network scenario."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
	})

	t.Run("get or new", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		first, err := lab.GetOrNewMachine("pc1", &MetaOptions{Image: strptr("a")})
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}
		second, err := lab.GetOrNewMachine("pc1", &MetaOptions{Image: strptr("b")})
		if err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}
		if first != second || len(lab.Machines()) != 1 {
			t.Error("GetOrNewMachine created a second device")
		}
		// Oracle P20: the options of the second call are silently dropped.
		if got := first.GetImage(); got != "a" {
			t.Errorf("GetImage() = %q, want a: the second call's options must be ignored", got)
		}

		if _, err := lab.GetOrNewMachine("pc2", nil); err != nil {
			t.Fatalf("GetOrNewMachine: %v", err)
		}
		if got := lab.MachineNames(); len(got) != 2 || got[0] != "pc1" || got[1] != "pc2" {
			t.Errorf("MachineNames() = %v, want insertion order", got)
		}
	})

	t.Run("remove", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		if _, _, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{}); err != nil {
			t.Fatalf("ConnectMachineToLink: %v", err)
		}
		if _, _, err := lab.ConnectMachineToLink("pc2", "A", AddInterfaceOptions{}); err != nil {
			t.Fatalf("ConnectMachineToLink: %v", err)
		}

		if err := lab.RemoveMachine("pc1", false); err != nil {
			t.Fatalf("RemoveMachine: %v", err)
		}
		if lab.HasMachine("pc1") {
			t.Error("the device is still in the scenario")
		}
		link, err := lab.GetLink("A")
		if err != nil {
			t.Fatalf("GetLink: %v", err)
		}
		// test_remove_machine_two_device_on_link: only the removed device
		// leaves; the collision domain itself survives even when empty.
		if link.HasMachine("pc1") || !link.HasMachine("pc2") {
			t.Errorf("link machines = %v", link.MachineNames())
		}
	})

	t.Run("remove by object", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if err := lab.RemoveMachineObj(machine, false); err != nil {
			t.Fatalf("RemoveMachineObj: %v", err)
		}
		if lab.HasMachine("pc1") {
			t.Error("the device is still in the scenario")
		}

		// The InvocationError of model/Lab.py:326, whose only Go spelling is a
		// nil device.
		if err := lab.RemoveMachineObj(nil, false); !errors.Is(err, kerrors.ErrInvocation) {
			t.Errorf("RemoveMachineObj(nil) = %v, want ErrInvocation", err)
		}
	})

	t.Run("remove missing", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		err := lab.RemoveMachine("pc1", false)
		if !errors.Is(err, kerrors.ErrMachineNotFound) {
			t.Errorf("error = %v, want ErrMachineNotFound", err)
		}
	})
}

func TestLabRemoveMachineTombstoneCrash(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, _, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{})
	if err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	link, err := lab.GetLink("A")
	if err != nil {
		t.Fatalf("GetLink: %v", err)
	}
	if err := machine.RemoveInterface(link); err != nil {
		t.Fatalf("RemoveInterface: %v", err)
	}

	err = lab.RemoveMachine("pc1", false)
	if !errors.Is(err, ErrPyAttributeError) {
		t.Fatalf("error = %v, want an AttributeError", err)
	}
	if want := "'NoneType' object has no attribute 'link'"; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	// Python mutates after the loop, so the device is still registered.
	if !lab.HasMachine("pc1") {
		t.Error("the device was removed despite the crash")
	}
}

// TestLabRemoveMachineDeleteFS pins the three removals and the non-empty
// directory failure (oracle Q7).
func TestLabRemoveMachineDeleteFS(t *testing.T) {
	t.Parallel()

	t.Run("empty directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "pc1.startup"), "x")
		mustWrite(t, filepath.Join(dir, "pc1.shutdown"), "x")
		if err := os.Mkdir(filepath.Join(dir, "pc1"), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if err := lab.RemoveMachine("pc1", true); err != nil {
			t.Fatalf("RemoveMachine: %v", err)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("directory still holds %d entries", len(entries))
		}
	})

	t.Run("non-empty directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "pc1.startup"), "x")
		if err := os.Mkdir(filepath.Join(dir, "pc1"), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		mustWrite(t, filepath.Join(dir, "pc1", "f"), "x")

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		err = lab.RemoveMachine("pc1", true)
		if !errors.Is(err, vfs.ErrDirectoryNotEmpty) {
			t.Fatalf("error = %v, want ErrDirectoryNotEmpty", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "pc1.startup")); !os.IsNotExist(err) {
			t.Error("the startup file survived, so the partial cleanup did not happen")
		}
	})

	t.Run("in-memory scenario", func(t *testing.T) {
		t.Parallel()

		lab := NewLab("test_lab", DefaultDefaults())
		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if err := lab.RemoveMachine("pc1", true); err != nil {
			t.Errorf("RemoveMachine: %v", err)
		}
	})

	// Python uses `fs.removedir` for the device directory and `fs.remove` for
	// the two startup files, and neither takes the other kind: the entry
	// survives and the call raises. A single generic Remove would DELETE what
	// 3.8.3 declines to touch (both oracle-verified).
	t.Run("device path is a file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "pc1"), "x")

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		err = lab.RemoveMachine("pc1", true)
		if !errors.Is(err, vfs.ErrDirectoryExpected) {
			t.Fatalf("error = %v, want ErrDirectoryExpected", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "pc1")); err != nil {
			t.Errorf("the file was deleted: %v", err)
		}
	})

	t.Run("startup path is a directory", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "pc1.startup"), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if _, err := lab.NewMachine("pc1", nil); err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		err = lab.RemoveMachine("pc1", true)
		if !errors.Is(err, vfs.ErrFileExpected) {
			t.Fatalf("error = %v, want ErrFileExpected", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "pc1.startup")); err != nil {
			t.Errorf("the empty directory was deleted: %v", err)
		}
	})
}

func TestLabRemoveMachineTombstoneCrashOrder(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	a, b := lab.GetOrNewLink("A"), lab.GetOrNewLink("B")
	if _, err := machine.AddInterface(a, AddInterfaceOptions{Number: InterfaceNumber(5)}); err != nil {
		t.Fatalf("AddInterface(A): %v", err)
	}
	if _, err := machine.AddInterface(b, AddInterfaceOptions{Number: InterfaceNumber(0)}); err != nil {
		t.Fatalf("AddInterface(B): %v", err)
	}
	if err := machine.RemoveInterface(a); err != nil {
		t.Fatalf("RemoveInterface: %v", err)
	}

	// The error and the "device stays registered" effect are Python's exactly.
	err = lab.RemoveMachine("pc1", false)
	if !errors.Is(err, ErrPyAttributeError) {
		t.Fatalf("error = %v, want an AttributeError", err)
	}
	if !lab.HasMachine("pc1") {
		t.Error("the device was removed despite the crash")
	}
	// The divergence: 3.8.3 hits the tombstone (slot 5, added first) before it
	// reaches slot 0, so B keeps its back-reference; here slot 0 comes first.
	if len(b.MachineNames()) != 0 {
		t.Errorf("B.MachineNames() = %v; expected the destination to be cleared first", b.MachineNames())
	}
}

func TestLabLinks(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())

	link, err := lab.NewLink("A")
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}
	if got, err := lab.GetLink("A"); err != nil || got != link {
		t.Errorf("GetLink = %v, %v", got, err)
	}

	_, err = lab.NewLink("A")
	if !errors.Is(err, kerrors.ErrLinkAlreadyExists) {
		t.Fatalf("error = %v, want ErrLinkAlreadyExists", err)
	}

	if want := "Collision domain A is already the network scenario."; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}

	_, err = lab.GetLink("Z")
	if !errors.Is(err, kerrors.ErrLinkNotFound) {
		t.Fatalf("error = %v, want ErrLinkNotFound", err)
	}
	if want := "Collision domain Z not found in the network scenario."; err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}

	if got := lab.GetOrNewLink("A"); got != link {
		t.Error("GetOrNewLink created a second collision domain")
	}
	lab.GetOrNewLink("B")
	if got := lab.LinkNames(); len(got) != 2 || got[0] != "A" || got[1] != "B" {
		t.Errorf("LinkNames() = %v, want insertion order", got)
	}
	if !lab.HasLink("A") || lab.HasLink("Z") {
		t.Error("HasLink is wrong")
	}
	if !lab.HasLinks([]string{"A", "B"}) || lab.HasLinks([]string{"A", "Z"}) || !lab.HasLinks(nil) {
		t.Error("HasLinks is wrong")
	}
}

func TestLabConnect(t *testing.T) {
	t.Parallel()

	t.Run("creates both ends", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, iface, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{})
		if err != nil {
			t.Fatalf("ConnectMachineToLink: %v", err)
		}
		if !lab.HasMachine("pc1") || !lab.HasLink("A") {
			t.Error("the device or the collision domain was not created")
		}
		if iface.Number != 0 || iface.Link.Name != "A" || iface.Machine != machine {
			t.Errorf("interface = %+v", iface)
		}

		// A second collision domain gets the next number.
		_, second, err := lab.ConnectMachineToLink("pc1", "B", AddInterfaceOptions{})
		if err != nil {
			t.Fatalf("ConnectMachineToLink: %v", err)
		}
		if second.Number != 1 {
			t.Errorf("second interface number = %d, want 1", second.Number)
		}
	})

	t.Run("explicit number and mac", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		_, iface, err := lab.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{
			Number: InterfaceNumber(2),
			MAC:    "00:00:00:00:00:01",
		})
		if err != nil {
			t.Fatalf("ConnectMachineToLink: %v", err)
		}
		if iface.Number != 2 || iface.MAC != "00:00:00:00:00:01" {
			t.Errorf("interface = %+v", iface)
		}
	})

	t.Run("by object", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		iface, err := lab.ConnectMachineObjToLink(machine, "A", AddInterfaceOptions{})
		if err != nil {
			t.Fatalf("ConnectMachineObjToLink: %v", err)
		}
		if iface.Number != 0 || !lab.HasLink("A") {
			t.Errorf("interface = %+v", iface)
		}
	})

	t.Run("by object across scenarios", func(t *testing.T) {
		t.Parallel()

		first := NewLab("a", DefaultDefaults())
		second := NewLab("b", DefaultDefaults())

		machine, err := first.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if _, err := second.ConnectMachineObjToLink(machine, "X", AddInterfaceOptions{}); err != nil {
			t.Fatalf("ConnectMachineObjToLink: %v", err)
		}
		if !second.HasLink("X") || second.HasMachine("pc1") {
			t.Error("the cross-scenario wiring did not behave like Python's")
		}
	})

	t.Run("two devices on one collision domain", func(t *testing.T) {
		t.Parallel()
		lab := NewLab("test_lab", DefaultDefaults())

		for _, name := range []string{"pc1", "pc2"} {
			if _, _, err := lab.ConnectMachineToLink(name, "A", AddInterfaceOptions{}); err != nil {
				t.Fatalf("ConnectMachineToLink: %v", err)
			}
		}
		link, err := lab.GetLink("A")
		if err != nil {
			t.Fatalf("GetLink: %v", err)
		}
		if got := link.MachineNames(); len(got) != 2 || got[0] != "pc1" || got[1] != "pc2" {
			t.Errorf("link machines = %v, want attach order", got)
		}
	})
}

func TestLabAssignMetaToMachine(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())

	prev, existed, err := lab.AssignMetaToMachine("pc1", "image", "a")
	if err != nil || existed || prev != nil {
		t.Fatalf("first = %v, %v, %v", prev, existed, err)
	}
	if !lab.HasMachine("pc1") {
		t.Error("the device was not created")
	}

	prev, existed, err = lab.AssignMetaToMachine("pc1", "image", "b")
	if err != nil || !existed || prev != "a" {
		t.Errorf("second = %v, %v, %v", prev, existed, err)
	}

	// The option errors propagate unchanged.
	_, _, err = lab.AssignMetaToMachine("pc1", "port", "value")
	if !errors.Is(err, kerrors.ErrMachineOption) {
		t.Errorf("error = %v, want ErrMachineOption", err)
	}
}

func TestLabAttachExternalLinks(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	lab.GetOrNewLink("A")

	err := lab.AttachExternalLinks(map[string][]ExternalLink{"A": {{Interface: "eth0"}}})
	if !errors.Is(err, kerrors.ErrNotSupported) {
		t.Fatalf("error = %v, want ErrNotSupported", err)
	}
	var feature *kerrors.FeatureNotAvailableError
	if !errors.As(err, &feature) || feature.Feature != kerrors.FeatureLabExt {
		t.Fatalf("error = %v, want a FeatureNotAvailableError for lab.ext", err)
	}
	want := "lab.ext external links are not supported in this release. Use Kathará 3.8.x."
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if link, _ := lab.GetLink("A"); len(link.External) != 0 {
		t.Error("the deferred call attached something")
	}
}

func TestLabCheckIntegrity(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	first, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	second, err := lab.NewMachine("pc2", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}
	if _, err := lab.ConnectMachineObjToLink(first, "A", AddInterfaceOptions{Number: InterfaceNumber(3)}); err != nil {
		t.Fatalf("ConnectMachineObjToLink: %v", err)
	}
	if _, err := lab.ConnectMachineObjToLink(second, "B", AddInterfaceOptions{Number: InterfaceNumber(5)}); err != nil {
		t.Fatalf("ConnectMachineObjToLink: %v", err)
	}

	err = lab.CheckIntegrity()
	if want := "Interface `0` missing on device `pc1`."; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}

	// A complete scenario passes.
	clean := NewLab("test_lab", DefaultDefaults())
	if _, _, err := clean.ConnectMachineToLink("pc1", "A", AddInterfaceOptions{}); err != nil {
		t.Fatalf("ConnectMachineToLink: %v", err)
	}
	if err := clean.CheckIntegrity(); err != nil {
		t.Errorf("CheckIntegrity: %v", err)
	}
}

func TestLabGetLinksFromMachines(t *testing.T) {
	t.Parallel()

	build := func(t *testing.T) *Lab {
		t.Helper()
		lab := NewLab("test_lab", DefaultDefaults())
		for _, pair := range [][2]string{{"pc1", "A"}, {"pc1", "B"}, {"pc2", "C"}} {
			if _, _, err := lab.ConnectMachineToLink(pair[0], pair[1], AddInterfaceOptions{}); err != nil {
				t.Fatalf("ConnectMachineToLink: %v", err)
			}
		}
		return lab
	}

	t.Run("by name", func(t *testing.T) {
		t.Parallel()
		lab := build(t)

		// An unknown name is silently dropped.
		got, err := lab.GetLinksFromMachines([]string{"pc1", "zz"})
		if err != nil {
			t.Fatalf("GetLinksFromMachines: %v", err)
		}
		if want := []string{"A", "B"}; !sameSet(got, want) {
			t.Errorf("links = %v, want %v", keysOf(got), want)
		}

		empty, err := lab.GetLinksFromMachines(nil)
		if err != nil || len(empty) != 0 {
			t.Errorf("GetLinksFromMachines(nil) = %v, %v", empty, err)
		}
	})

	t.Run("by object", func(t *testing.T) {
		t.Parallel()
		lab := build(t)

		machine, err := lab.GetMachine("pc2")
		if err != nil {
			t.Fatalf("GetMachine: %v", err)
		}
		got, err := lab.GetLinksFromMachineObjs([]*Machine{machine})
		if err != nil {
			t.Fatalf("GetLinksFromMachineObjs: %v", err)
		}
		if want := []string{"C"}; !sameSet(got, want) {
			t.Errorf("links = %v, want %v", keysOf(got), want)
		}
	})

	t.Run("nil device crashes", func(t *testing.T) {
		t.Parallel()
		lab := build(t)

		machine, err := lab.GetMachine("pc2")
		if err != nil {
			t.Fatalf("GetMachine: %v", err)
		}

		// `set(map(lambda x: x.name, machines))` runs before the intersection,
		// so a None anywhere in the argument dies on `.name` — whether or not
		// the other entries are registered devices. Oracle-verified:
		// get_links_from_machine_objs([pc2, None]) and ([None]) both raise
		// AttributeError: 'NoneType' object has no attribute 'name'.
		for _, arg := range [][]*Machine{{machine, nil}, {nil}} {
			got, err := lab.GetLinksFromMachineObjs(arg)
			if !errors.Is(err, ErrPyAttributeError) ||
				err.Error() != "'NoneType' object has no attribute 'name'" {
				t.Errorf("GetLinksFromMachineObjs(%v) error = %v, want the AttributeError", arg, err)
			}
			if got != nil {
				t.Errorf("GetLinksFromMachineObjs(%v) = %v, want no links", arg, keysOf(got))
			}
		}
	})

	t.Run("tombstone crashes", func(t *testing.T) {
		t.Parallel()
		lab := build(t)

		machine, err := lab.GetMachine("pc1")
		if err != nil {
			t.Fatalf("GetMachine: %v", err)
		}
		link, err := lab.GetLink("A")
		if err != nil {
			t.Fatalf("GetLink: %v", err)
		}
		if err := machine.RemoveInterface(link); err != nil {
			t.Fatalf("RemoveInterface: %v", err)
		}

		_, err = lab.GetLinksFromMachines([]string{"pc1"})
		if !errors.Is(err, ErrPyAttributeError) ||
			err.Error() != "'NoneType' object has no attribute 'link'" {
			t.Errorf("error = %v, want the AttributeError", err)
		}
		_, err = lab.GetLinksFromMachineObjs([]*Machine{machine})
		if !errors.Is(err, ErrPyAttributeError) {
			t.Errorf("error = %v, want the AttributeError", err)
		}
	})
}

func TestLabApplyDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		deps []string
		want []string
	}{
		// Unlisted devices come first, in insertion order; the listed ones
		// follow in list order.
		{name: "partial", deps: []string{"pc3", "pc1", "pc2"}, want: []string{"pc4", "pc3", "pc1", "pc2"}},
		// A repeated name takes its FIRST position.
		{name: "duplicate", deps: []string{"pc2", "pc2", "pc1"}, want: []string{"pc3", "pc4", "pc2", "pc1"}},
		{name: "empty", deps: nil, want: []string{"pc1", "pc2", "pc3", "pc4"}},
		{name: "unknown only", deps: []string{"zz"}, want: []string{"pc1", "pc2", "pc3", "pc4"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lab := NewLab("test_lab", DefaultDefaults())
			for _, name := range []string{"pc1", "pc2", "pc3", "pc4"} {
				if _, err := lab.NewMachine(name, nil); err != nil {
					t.Fatalf("NewMachine: %v", err)
				}
			}

			lab.ApplyDependencies(tt.deps)

			if got := lab.MachineNames(); strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Errorf("MachineNames() = %v, want %v", got, tt.want)
			}
			// Set even for an empty list: it is what switches both backends to
			// the sequential deploy path.
			if !lab.HasDependencies {
				t.Error("HasDependencies is false after ApplyDependencies")
			}
		})
	}
}

// TestLabOptions pins the `is not None` gates of add_option and
// add_global_machine_metadata: a falsy value IS stored (oracle P21).
func TestLabOptions(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())

	lab.AddOption("absent", Scalar{})
	lab.AddOption("false", Bool(false))
	lab.AddOption("empty", Str(""))
	lab.AddGlobalMachineMetadata("absent", Scalar{})
	lab.AddGlobalMachineMetadata("zero", Int(0))

	if _, ok := lab.GeneralOption("absent"); ok {
		t.Error("an absent option was stored")
	}
	if got, ok := lab.GeneralOption("false"); !ok || got.Value() != false {
		t.Errorf("general option false = %v, %v", got.Value(), ok)
	}
	if got, ok := lab.GeneralOption("empty"); !ok || got.Value() != "" {
		t.Errorf("general option empty = %v, %v", got.Value(), ok)
	}
	if _, ok := lab.GlobalMachineMetadata("absent"); ok {
		t.Error("an absent global metadata was stored")
	}
	if got, ok := lab.GlobalMachineMetadata("zero"); !ok || got.Value() != int64(0) {
		t.Errorf("global metadata zero = %v, %v", got.Value(), ok)
	}
	if got := lab.GeneralOptions(); len(got) != 2 || got[0].Key != "false" {
		t.Errorf("GeneralOptions() = %v, want insertion order", got)
	}
	if got := lab.GlobalMachineMetadatas(); len(got) != 1 {
		t.Errorf("GlobalMachineMetadatas() = %v", got)
	}
}

func TestLabCreateSharedFolder(t *testing.T) {
	t.Parallel()

	t.Run("host-backed", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		if err := lab.CreateSharedFolder(); err != nil {
			t.Fatalf("CreateSharedFolder: %v", err)
		}
		if want := filepath.Join(dir, "shared"); lab.SharedPath != want {
			t.Errorf("SharedPath = %q, want %q", lab.SharedPath, want)
		}
		if info, err := os.Stat(filepath.Join(dir, "shared")); err != nil || !info.IsDir() {
			t.Errorf("shared directory: %v", err)
		}
		// recreate=True: a second call is a no-op, not an error.
		if err := lab.CreateSharedFolder(); err != nil {
			t.Errorf("second CreateSharedFolder: %v", err)
		}
	})

	t.Run("in-memory", func(t *testing.T) {
		t.Parallel()

		lab := NewLab("test_lab", DefaultDefaults())
		if err := lab.CreateSharedFolder(); err != nil {
			t.Fatalf("CreateSharedFolder: %v", err)
		}
		if lab.SharedPath != "" {
			t.Errorf("SharedPath = %q, want empty", lab.SharedPath)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(dir, "shared")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		err = lab.CreateSharedFolder()
		if !errors.Is(err, kerrors.ErrValue) {
			t.Fatalf("error = %v, want ErrValue", err)
		}
		if want := "`shared` folder is a symlink, delete it."; err.Error() != want {
			t.Errorf("message = %q, want %q", err.Error(), want)
		}
		if lab.SharedPath == "" {
			t.Error("SharedPath was not set on the error path")
		}
	})

	t.Run("shared is a file", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		mustWrite(t, filepath.Join(dir, "shared"), "x")

		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}

		// `except OSError` catches nothing here: pyfilesystem raises FSError
		// subclasses, which do not derive from OSError, so 3.8.3 propagates
		// `fs.errors.DirectoryExpected` (oracle-verified). Swallowing it would
		// let `lstart` continue with no shared folder where Python dies.
		err = lab.CreateSharedFolder()
		if !errors.Is(err, vfs.ErrDirectoryExpected) {
			t.Fatalf("error = %v, want ErrDirectoryExpected", err)
		}
		if lab.SharedPath != "" {
			t.Errorf("SharedPath = %q, want empty: Python never reaches the assignment", lab.SharedPath)
		}
	})
}

func TestLabFilesystemHelpers(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	machine, err := lab.NewMachine("pc1", nil)
	if err != nil {
		t.Fatalf("NewMachine: %v", err)
	}

	if err := lab.CreateStartupFileFromList(machine, []string{"a", "b"}); err != nil {
		t.Fatalf("CreateStartupFileFromList: %v", err)
	}
	if err := lab.UpdateStartupFileFromString(machine, "c\n"); err != nil {
		t.Fatalf("UpdateStartupFileFromString: %v", err)
	}

	got, err := vfs.ReadFile(lab.FS, "pc1.startup")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if want := "a\nb\nc\n"; string(got) != want {
		t.Errorf("pc1.startup = %q, want %q", got, want)
	}

	// create truncates.
	if err := lab.CreateStartupFileFromString(machine, "z"); err != nil {
		t.Fatalf("CreateStartupFileFromString: %v", err)
	}
	if got, err := vfs.ReadFile(lab.FS, "pc1.startup"); err != nil || string(got) != "z" {
		t.Errorf("pc1.startup = %q, %v; want z", got, err)
	}
}

// TestMachineFilesystemLazyDir pins model/Machine.py:635: the device directory
// is created on the first write, and the device adopts it (oracle Q5).
func TestMachineFilesystemLazyDir(t *testing.T) {
	t.Parallel()

	t.Run("created on first write", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if machine.FS != nil {
			t.Fatal("the device already has a filesystem")
		}

		if err := machine.CreateFileFromString("hello", "etc/a.txt"); err != nil {
			t.Fatalf("CreateFileFromString: %v", err)
		}
		if machine.FSType() != "sub" {
			t.Errorf("FSType() = %q, want sub", machine.FSType())
		}
		body, err := os.ReadFile(filepath.Join(dir, "pc1", "etc", "a.txt"))
		if err != nil || string(body) != "hello" {
			t.Errorf("file = %q, %v", body, err)
		}
	})

	t.Run("adopted at construction", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "pc1"), 0o755); err != nil {
			t.Fatalf("Mkdir: %v", err)
		}
		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}
		if machine.FS == nil {
			t.Fatal("the existing device directory was not adopted")
		}
		if got, ok := machine.FSPath(); !ok || got != filepath.Join(dir, "pc1") {
			t.Errorf("FSPath() = %q, %v", got, ok)
		}
	})

	t.Run("the directory appears even when the write fails", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		lab, err := NewLabFromPath(dir, DefaultDefaults())
		if err != nil {
			t.Fatalf("NewLabFromPath: %v", err)
		}
		machine, err := lab.NewMachine("pc1", nil)
		if err != nil {
			t.Fatalf("NewMachine: %v", err)
		}

		// An update on a missing file: the mkdir has already happened.
		if _, err := machine.DeleteLine("nope.txt", "x", false); err == nil {
			t.Error("DeleteLine on a missing file returned no error")
		}
		if info, err := os.Stat(filepath.Join(dir, "pc1")); err != nil || !info.IsDir() {
			t.Errorf("the device directory was not created as a side effect: %v", err)
		}
	})
}

// TestLabString pins Lab.__str__: the fixed field order, and an empty string
// hidden exactly like an absent one (oracle P11/Q10).
func TestLabString(t *testing.T) {
	t.Parallel()

	lab := NewLab("mylab", DefaultDefaults())
	if got, want := lab.String(), "Name: mylab"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	lab.Description = "d"
	lab.Version = "1"
	lab.Author = "a"
	lab.Email = "e"
	lab.Web = "w"
	want := "Name: mylab\nDescription: d\nVersion: 1\nAuthor(s): a\nEmail: e\nWebsite: w"
	if got := lab.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	empty := NewLab("", DefaultDefaults())
	empty.Description = ""
	if got := empty.String(); got != "" {
		t.Errorf("String() = %q, want empty", got)
	}
}

// TestLinkString pins Link.__repr__, which is what a %s of a Link produces.
func TestLinkString(t *testing.T) {
	t.Parallel()

	lab := NewLab("test_lab", DefaultDefaults())
	link := lab.GetOrNewLink("A")
	if got, want := link.String(), "Link(A, [])"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}

	link.External = []ExternalLink{{Interface: "eth0"}, {Interface: "eth1", VLAN: 10}}
	if got, want := link.String(), "Link(A, [ExternalLink(eth0, None), ExternalLink(eth1, 10)])"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func strptr(s string) *string { return &s }

func mustWrite(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func keysOf(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sameSet(got map[string]struct{}, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, name := range want {
		if _, ok := got[name]; !ok {
			return false
		}
	}
	return true
}
