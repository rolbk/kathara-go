package model

import (
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/vfs"
)

// labMetadata is `LAB_METADATA` (`model/Lab.py:19`), the lab.conf keys that
// describe the scenario rather than a device. The parser routes an assignment
// to one of these into the matching [Lab] field.
var labMetadata = []string{"LAB_NAME", "LAB_DESCRIPTION", "LAB_VERSION", "LAB_AUTHOR", "LAB_EMAIL", "LAB_WEB"}

// LabMetadata returns `LAB_METADATA` in source order.
func LabMetadata() []string { return append([]string(nil), labMetadata...) }

// Lab is a Kathará network scenario (`model/Lab.py`): the devices, the
// collision domains, the scenario-wide options and the filesystem they are
// described by.
type Lab struct {
	// Description, Version, Author, Email and Web are the LAB_* metadata.
	Description string
	Version     string
	Author      string
	Email       string
	Web         string

	Hash string

	SharedPath string

	// HasDependencies reports whether [Lab.ApplyDependencies] has run. Both
	// backends switch to sequential deploy when it is true.
	HasDependencies bool

	// FS is the scenario filesystem: the lab directory for a scenario with a
	// path, an in-memory filesystem otherwise. It is never nil.
	FS vfs.FS

	// Defaults are the settings-derived fallbacks the device accessors use
	// (the settings-default mapping: injected, never read from a singleton).
	Defaults Defaults

	name                  *string
	machines              *OrderedMap[string, *Machine]
	links                 *OrderedMap[string, *Link]
	generalOptions        *OrderedMap[string, Scalar]
	globalMachineMetadata *OrderedMap[string, Scalar]
}

// newLab is the shared body of the three constructors: `Lab.__init__` with the
// name/path tri-state already resolved by the caller.
func newLab(name *string, path string, defaults Defaults) (*Lab, error) {
	lab := &Lab{
		Defaults:              defaults,
		name:                  name,
		machines:              NewOrderedMap[string, *Machine](),
		links:                 NewOrderedMap[string, *Link](),
		generalOptions:        NewOrderedMap[string, Scalar](),
		globalMachineMetadata: NewOrderedMap[string, Scalar](),
	}

	// `utils.generate_urlsafe_hash(path if self._name is None else self._name)`
	// — the NAME wins whenever there is one, and the path is hashed only for an
	// unnamed scenario (`model/Lab.py:76`).
	source := path
	if name != nil {
		source = *name
	}
	lab.Hash = util.GenerateURLSafeHash(source)

	if path == "" {
		lab.FS = vfs.Memory()
		return lab, nil
	}

	// `open_fs("osfs://…")` fails when the directory is not there; the Go
	// filesystem is lazy, so the check is explicit.
	// The path is used LITERALLY.
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return nil, newCreateFailed(path)
	}
	lab.FS = vfs.OSDir(path)

	return lab, nil
}

// NewLab is `Lab(name)`: a named scenario with no directory, backed by an
// in-memory filesystem. Its hash is the hash of the name.
func NewLab(name string, defaults Defaults) *Lab {
	// The memory branch cannot fail.
	lab, _ := newLab(&name, "", defaults)
	return lab
}

// NewLabWithPath is `Lab(name, path)`: a named scenario rooted at a directory.
// The hash still comes from the name.
func NewLabWithPath(name, path string, defaults Defaults) (*Lab, error) {
	return newLab(&name, path, defaults)
}

// NewLabFromPath is `Lab(None, path)`: an unnamed scenario rooted at a
// directory, whose hash is the hash of the path. It is what `LabParser` builds
// before a `LAB_NAME` line, if any, renames it.
func NewLabFromPath(path string, defaults Defaults) (*Lab, error) {
	return newLab(nil, path, defaults)
}

func newCreateFailed(path string) error {
	return &PyRuntimeError{
		Class: "CreateFailed",
		Msg:   "root path '" + path + "' does not exist",
	}
}

// Name is the scenario name, empty when there is none. An empty name and an
// absent one behave identically everywhere they are read; [Lab.HasName]
// separates them for a serializer that must emit `null`.
func (l *Lab) Name() string {
	if l.name == nil {
		return ""
	}
	return *l.name
}

// HasName reports whether the scenario has a name at all — Python's
// `self._name is not None`, the test that decides whether [Lab.Hash] came from
// the name or from the path.
func (l *Lab) HasName() bool { return l.name != nil }

// SetName is the `name` property setter (`model/Lab.py:85`): it renames the
// scenario AND recomputes [Lab.Hash] from the new name.
func (l *Lab) SetName(name string) {
	l.name = &name
	l.Hash = util.GenerateURLSafeHash(name)
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

func (l *Lab) Machines() []*Machine { return l.machines.Values() }

// MachineNames returns the device names in insertion order.
func (l *Lab) MachineNames() []string { return l.machines.Keys() }

// HasMachine is `has_machine` (`model/Lab.py:437`).
func (l *Lab) HasMachine(name string) bool { return l.machines.Has(name) }

// HasMachines is `has_machines` (`model/Lab.py:448`): every name must be
// present. An empty set is true, as `all(())` is.
func (l *Lab) HasMachines(names []string) bool {
	for _, name := range names {
		if !l.HasMachine(name) {
			return false
		}
	}
	return true
}

// GetMachine is `get_machine` (`model/Lab.py:262`).
func (l *Lab) GetMachine(name string) (*Machine, error) {
	machine, ok := l.machines.Get(name)
	if !ok {
		return nil, kerrors.NewMachineNotFoundInScenario(name)
	}
	return machine, nil
}

// NewMachine is `new_machine` (`model/Lab.py:279`): create the device and
// register it, refusing a name that is already taken.
func (l *Lab) NewMachine(name string, opts *MetaOptions) (*Machine, error) {
	if l.machines.Has(name) {
		return nil, kerrors.NewMachineAlreadyExists(name)
	}

	machine, err := newMachine(l, name, opts)
	if err != nil {
		return nil, err
	}
	l.machines.Set(name, machine)

	return machine, nil
}

// GetOrNewMachine is `get_or_new_machine` (`model/Lab.py:301`).
func (l *Lab) GetOrNewMachine(name string, opts *MetaOptions) (*Machine, error) {
	if machine, ok := l.machines.Get(name); ok {
		return machine, nil
	}

	machine, err := newMachine(l, name, opts)
	if err != nil {
		return nil, err
	}
	l.machines.Set(name, machine)

	return machine, nil
}

// RemoveMachine is `remove_machine(name=…)` (`model/Lab.py:317`): drop the
// device from the scenario and from every collision domain it was attached to.
func (l *Lab) RemoveMachine(name string, deleteFS bool) error {
	machine, ok := l.machines.Get(name)
	if !ok {
		return kerrors.NewMachineNotFoundInScenario(name)
	}

	for _, iface := range machine.interfaces {
		if iface.IsTombstone() {
			return newNoneAttributeError("link")
		}
		if !iface.Link.machines.Delete(name) {
			// `del link.machines[name]` on a key that is not there. Reachable
			// only through the cross-lab hole in
			// [Lab.ConnectMachineObjToLink], which never checks that the device
			// belongs to this scenario.
			return newKeyError(name)
		}
	}

	l.machines.Delete(name)

	if !deleteFS {
		return nil
	}
	for _, suffix := range []string{".startup", ".shutdown"} {
		target := name + suffix
		if !vfs.Exists(l.FS, target) {
			continue
		}
		if vfs.IsDir(l.FS, target) {
			return &fs.PathError{Op: "remove", Path: target, Err: vfs.ErrFileExpected}
		}
		if err := l.FS.Remove(target); err != nil {
			return err
		}
	}
	if vfs.Exists(l.FS, name) {
		if !vfs.IsDir(l.FS, name) {
			return &fs.PathError{Op: "removedir", Path: name, Err: vfs.ErrDirectoryExpected}
		}
		return l.FS.Remove(name)
	}
	return nil
}

// RemoveMachineObj is `remove_machine(machine=…)`, which resolves the object to
// its name and then does exactly what [Lab.RemoveMachine] does.
func (l *Lab) RemoveMachineObj(machine *Machine, deleteFS bool) error {
	if machine == nil {
		return kerrors.ErrDeviceNameOrObject
	}
	return l.RemoveMachine(machine.Name, deleteFS)
}

// ---------------------------------------------------------------------------
// Collision domains
// ---------------------------------------------------------------------------

func (l *Lab) Links() []*Link { return l.links.Values() }

// LinkNames returns the collision-domain names in insertion order.
func (l *Lab) LinkNames() []string { return l.links.Keys() }

// HasLink is `has_link` (`model/Lab.py:459`).
func (l *Lab) HasLink(name string) bool { return l.links.Has(name) }

// HasLinks is `has_links` (`model/Lab.py:470`).
func (l *Lab) HasLinks(names []string) bool {
	for _, name := range names {
		if !l.HasLink(name) {
			return false
		}
	}
	return true
}

// GetLink is `get_link` (`model/Lab.py:355`).
func (l *Lab) GetLink(name string) (*Link, error) {
	link, ok := l.links.Get(name)
	if !ok {
		return nil, kerrors.NewLinkNotFoundInScenario(name)
	}
	return link, nil
}

// NewLink is `new_link` (`model/Lab.py:371`).
func (l *Lab) NewLink(name string) (*Link, error) {
	if l.links.Has(name) {
		return nil, kerrors.NewLinkAlreadyExists(name)
	}

	link := newLink(l, name)
	l.links.Set(name, link)

	return link, nil
}

// GetOrNewLink is `get_or_new_link` (`model/Lab.py:388`).
func (l *Lab) GetOrNewLink(name string) *Link {
	if link, ok := l.links.Get(name); ok {
		return link
	}

	link := newLink(l, name)
	l.links.Set(name, link)

	return link
}

// ---------------------------------------------------------------------------
// Wiring
// ---------------------------------------------------------------------------

// ConnectMachineToLink is `connect_machine_to_link` (`model/Lab.py:88`): attach
// a device to a collision domain, creating BOTH if they do not exist yet.
func (l *Lab) ConnectMachineToLink(machineName, linkName string, opts AddInterfaceOptions) (*Machine, Interface, error) {
	machine, err := l.GetOrNewMachine(machineName, nil)
	if err != nil {
		return nil, Interface{}, err
	}
	link := l.GetOrNewLink(linkName)

	iface, err := machine.AddInterface(link, opts)
	if err != nil {
		return nil, Interface{}, err
	}
	return machine, iface, nil
}

// ConnectMachineObjToLink is `connect_machine_obj_to_link`
// (`model/Lab.py:114`): the same, for a device the caller already holds.
func (l *Lab) ConnectMachineObjToLink(machine *Machine, linkName string, opts AddInterfaceOptions) (Interface, error) {
	link := l.GetOrNewLink(linkName)
	return machine.AddInterface(link, opts)
}

// AssignMetaToMachine is `assign_meta_to_machine` (`model/Lab.py:137`): set one
// meta on a device, creating the device if needed, and report what the key held
// before.
func (l *Lab) AssignMetaToMachine(machineName, metaName, metaValue string) (any, bool, error) {
	machine, err := l.GetOrNewMachine(machineName, nil)
	if err != nil {
		return nil, false, err
	}
	return machine.AddMeta(metaName, metaValue)
}

// AttachExternalLinks is `attach_external_links` (`model/Lab.py:159`).
func (l *Lab) AttachExternalLinks(map[string][]ExternalLink) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
}

// CheckIntegrity is `check_integrity` (`model/Lab.py:181`): run [Machine.Check]
// on every device, in scenario order.
func (l *Lab) CheckIntegrity() error {
	slog.Debug("Checking network scenario integrity...")

	for _, machine := range l.machines.Values() {
		if err := machine.Check(); err != nil {
			return err
		}
	}
	return nil
}

// GetLinksFromMachines is `get_links_from_machines` (`model/Lab.py:194`): the
// names of the collision domains the given devices are attached to.
func (l *Lab) GetLinksFromMachines(names []string) (map[string]struct{}, error) {
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		if l.machines.Has(name) {
			selected[name] = struct{}{}
		}
	}
	return l.linksOf(selected)
}

// GetLinksFromMachineObjs is `get_links_from_machine_objs`
// (`model/Lab.py:217`): the same, keyed by the devices' names — which are still
// intersected against the scenario, so a device object that is not registered
// contributes nothing.
func (l *Lab) GetLinksFromMachineObjs(machines []*Machine) (map[string]struct{}, error) {
	selected := make(map[string]struct{}, len(machines))
	for _, machine := range machines {
		if machine == nil {
			return nil, newNoneAttributeError("name")
		}
		if l.machines.Has(machine.Name) {
			selected[machine.Name] = struct{}{}
		}
	}
	return l.linksOf(selected)
}

// linksOf is the shared tail of the two: walk the selected devices in scenario
// order and collect their collision domains.
func (l *Lab) linksOf(selected map[string]struct{}) (map[string]struct{}, error) {
	links := make(map[string]struct{})
	for _, entry := range l.machines.Entries() {
		if _, ok := selected[entry.Key]; !ok {
			continue
		}
		for _, iface := range entry.Value.interfaces {
			if iface.IsTombstone() {
				return nil, newNoneAttributeError("link")
			}
			links[iface.Link.Name] = struct{}{}
		}
	}
	return links, nil
}

// ApplyDependencies is `apply_dependencies` (`model/Lab.py:236`): reorder the
// devices so that lab.dep's boot order is satisfied.
func (l *Lab) ApplyDependencies(dependencies []string) {
	position := make(map[string]int, len(dependencies))
	for i, name := range dependencies {
		if _, seen := position[name]; !seen {
			position[name] = i + 1
		}
	}

	l.machines.SortStableFunc(func(a, b Entry[string, *Machine]) int {
		return position[a.Key] - position[b.Key]
	})
	l.HasDependencies = true
}

// ---------------------------------------------------------------------------
// Options
// ---------------------------------------------------------------------------

// AddOption is `add_option` (`model/Lab.py:404`): store a scenario-wide option,
// unless the value is absent.
func (l *Lab) AddOption(name string, value Scalar) {
	if !value.IsSet() {
		return
	}
	l.generalOptions.Set(name, value)
}

// GeneralOption reads one scenario-wide option.
func (l *Lab) GeneralOption(name string) (Scalar, bool) { return l.generalOptions.Get(name) }

// GeneralOptions returns every scenario-wide option, in insertion order.
func (l *Lab) GeneralOptions() []Entry[string, Scalar] { return l.generalOptions.Entries() }

// AddGlobalMachineMetadata is `add_global_machine_metadata`
// (`model/Lab.py:417`): a meta applied to every device of the scenario, with
// the same `is not None` gate as [Lab.AddOption].
func (l *Lab) AddGlobalMachineMetadata(name string, value Scalar) {
	if !value.IsSet() {
		return
	}
	l.globalMachineMetadata.Set(name, value)
}

// GlobalMachineMetadata reads one scenario-wide device meta.
func (l *Lab) GlobalMachineMetadata(name string) (Scalar, bool) {
	return l.globalMachineMetadata.Get(name)
}

// GlobalMachineMetadatas returns every scenario-wide device meta, in insertion
// order.
func (l *Lab) GlobalMachineMetadatas() []Entry[string, Scalar] {
	return l.globalMachineMetadata.Entries()
}

// ---------------------------------------------------------------------------
// Filesystem
// ---------------------------------------------------------------------------

// HasHostPath is `has_host_path` (`model/Lab.py:394`): whether the scenario is
// backed by a real directory rather than by memory.
func (l *Lab) HasHostPath() bool { return vfs.Type(l.FS) == "os" }

// CreateSharedFolder is `create_shared_folder` (`model/Lab.py:406`): create the
// scenario's `shared` directory, which every device mounts.
func (l *Lab) CreateSharedFolder() error {
	if !l.HasHostPath() {
		return nil
	}

	// 0o777 is pyfilesystem's default `Permissions`; the umask narrows it (see
	// [Machine.ensureFS]).
	if err := l.FS.MkdirAll("shared", 0o777); err != nil {
		return err
	}

	path, ok := l.FS.SysPath("shared")
	if !ok {
		return nil
	}
	l.SharedPath = path

	info, err := os.Lstat(path)
	if err != nil {
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return kerrors.ErrSharedSymlink
	}
	return nil
}

// FSType is `FilesystemMixin.fs_type()`: "os" or "memory" for a scenario.
func (l *Lab) FSType() string { return vfs.Type(l.FS) }

// FSPath is `FilesystemMixin.fs_path()`: the host path of the scenario
// directory, and whether it has one.
func (l *Lab) FSPath() (string, bool) { return vfs.Path(l.FS) }

// CreateFileFromString is `FilesystemMixin.create_file_from_string` on the
// scenario filesystem.
func (l *Lab) CreateFileFromString(content, dstPath string) error {
	return vfs.CreateFileFromString(l.FS, content, dstPath)
}

// UpdateFileFromString is `FilesystemMixin.update_file_from_string` — append.
func (l *Lab) UpdateFileFromString(content, dstPath string) error {
	return vfs.UpdateFileFromString(l.FS, content, dstPath)
}

// CreateFileFromList is `FilesystemMixin.create_file_from_list`: every line
// gets a trailing newline, the last one included.
func (l *Lab) CreateFileFromList(lines []string, dstPath string) error {
	return vfs.CreateFileFromList(l.FS, lines, dstPath)
}

// UpdateFileFromList is `FilesystemMixin.update_file_from_list`.
func (l *Lab) UpdateFileFromList(lines []string, dstPath string) error {
	return vfs.UpdateFileFromList(l.FS, lines, dstPath)
}

// CreateFileFromPath is `FilesystemMixin.create_file_from_path`.
func (l *Lab) CreateFileFromPath(srcPath, dstPath string) error {
	return vfs.CreateFileFromPath(l.FS, srcPath, dstPath)
}

// CreateFileFromStream is `FilesystemMixin.create_file_from_stream`.
func (l *Lab) CreateFileFromStream(stream io.Reader, dstPath string) error {
	return vfs.CreateFileFromStream(l.FS, stream, dstPath)
}

// CopyDirectoryFromPath is `FilesystemMixin.copy_directory_from_path`.
func (l *Lab) CopyDirectoryFromPath(srcPath, dstPath string) error {
	return vfs.CopyDirectory(l.FS, srcPath, dstPath)
}

// WriteLineBefore is `FilesystemMixin.write_line_before`.
func (l *Lab) WriteLineBefore(filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	return vfs.WriteLineBefore(l.FS, filePath, lineToAdd, searchedLine, firstOccurrence)
}

// WriteLineAfter is `FilesystemMixin.write_line_after`.
func (l *Lab) WriteLineAfter(filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	return vfs.WriteLineAfter(l.FS, filePath, lineToAdd, searchedLine, firstOccurrence)
}

// DeleteLine is `FilesystemMixin.delete_line`.
func (l *Lab) DeleteLine(filePath, lineToDelete string, firstOccurrence bool) (int, error) {
	return vfs.DeleteLine(l.FS, filePath, lineToDelete, firstOccurrence)
}

// The six `LabFilesystemMixin` wrappers (`foundation/model/LabFilesystemMixin.py`).
// Each writes `<device>.startup` at the scenario root; the device's own
// filesystem is not touched, and the device is taken only for its name. There
// are no shutdown-file helpers in 3.8.3 either.

// CreateStartupFileFromString is `create_startup_file_from_string`.
func (l *Lab) CreateStartupFileFromString(machine *Machine, commands string) error {
	return l.CreateFileFromString(commands, startupFileName(machine))
}

// CreateStartupFileFromList is `create_startup_file_from_list`.
func (l *Lab) CreateStartupFileFromList(machine *Machine, commands []string) error {
	return l.CreateFileFromList(commands, startupFileName(machine))
}

// CreateStartupFileFromPath is `create_startup_file_from_path`.
func (l *Lab) CreateStartupFileFromPath(machine *Machine, srcPath string) error {
	return l.CreateFileFromPath(srcPath, startupFileName(machine))
}

// CreateStartupFileFromStream is `create_startup_file_from_stream`.
func (l *Lab) CreateStartupFileFromStream(machine *Machine, stream io.Reader) error {
	return l.CreateFileFromStream(stream, startupFileName(machine))
}

// UpdateStartupFileFromString is `update_startup_file_from_string` — append.
func (l *Lab) UpdateStartupFileFromString(machine *Machine, commands string) error {
	return l.UpdateFileFromString(commands, startupFileName(machine))
}

// UpdateStartupFileFromList is `update_startup_file_from_list` — append.
func (l *Lab) UpdateStartupFileFromList(machine *Machine, commands []string) error {
	return l.UpdateFileFromList(commands, startupFileName(machine))
}

func startupFileName(machine *Machine) string { return machine.Name + ".startup" }

// String is `Lab.__str__` (`model/Lab.py:501`): the scenario description panel,
// one line per metadata field that has a value, in this fixed order.
func (l *Lab) String() string {
	var lines []string
	if l.Name() != "" {
		lines = append(lines, "Name: "+l.Name())
	}
	if l.Description != "" {
		lines = append(lines, "Description: "+l.Description)
	}
	if l.Version != "" {
		lines = append(lines, "Version: "+l.Version)
	}
	if l.Author != "" {
		lines = append(lines, "Author(s): "+l.Author)
	}
	if l.Email != "" {
		lines = append(lines, "Email: "+l.Email)
	}
	if l.Web != "" {
		lines = append(lines, "Website: "+l.Web)
	}
	return strings.Join(lines, "\n")
}
