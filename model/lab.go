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
	// Description, Version, Author, Email and Web are the LAB_* metadata. An
	// empty string is indistinguishable from unset, which is Python's own
	// behaviour: [Lab.String] tests them for truthiness (NILABILITY.tsv:25).
	Description string
	Version     string
	Author      string
	Email       string
	Web         string

	// Hash is the scenario identity — half of every container name, network
	// name and label. It is derived from the name when there is one and from
	// the path otherwise, and [Lab.SetName] recomputes it. PORT_SPEC §0.4
	// forbids changing how it is computed.
	Hash string

	// SharedPath is the host path of the scenario's `shared` folder, set only
	// by [Lab.CreateSharedFolder] and empty until then (NILABILITY.tsv:26).
	SharedPath string

	// HasDependencies reports whether [Lab.ApplyDependencies] has run. Both
	// backends switch to sequential deploy when it is true.
	HasDependencies bool

	// FS is the scenario filesystem: the lab directory for a scenario with a
	// path, an in-memory filesystem otherwise. It is never nil.
	FS vfs.FS

	// Defaults are the settings-derived fallbacks the device accessors use
	// (OQ-4: injected, never read from a singleton).
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

	// `if path:` — a truthiness test, not an `is None` one, so the empty path
	// takes the memory filesystem while still having been hashed above
	// (NILABILITY.tsv:23-24).
	if path == "" {
		lab.FS = vfs.Memory()
		return lab, nil
	}

	// `open_fs("osfs://…")` fails when the directory is not there; the Go
	// filesystem is lazy, so the check is explicit.
	//
	// The path is used LITERALLY. Python wraps it in a pyfilesystem URL, whose
	// parser splits it at a `?` or a `!` before `OSFS.__init__` expands `~` and
	// `$VAR` — so `Lab(None, "/x/my?lab")` opens `/x/my` while hashing the full
	// path. DIVERGENCES.md 43 records that; §6 left no URL layer to reproduce
	// it in.
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
//
// An empty path reproduces `Lab(None, "")`: the empty string is hashed and the
// filesystem is in memory. `Lab(None, None)`, which crashes 3.8.3 inside
// `re.sub`, has no spelling here — Go cannot tell the two apart, and
// NILABILITY.tsv:24 maps the Python None onto "".
func NewLabFromPath(path string, defaults Defaults) (*Lab, error) {
	return newLab(nil, path, defaults)
}

// newCreateFailed is pyfilesystem's `CreateFailed`, which `open_fs("osfs://…")`
// raises for a missing root. It has no ERROR_CODES.md row — no Kathará class
// and no builtin — so it buckets to InternalError like the other unmapped
// classes, with the class name kept for the vector runner.
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
//
// That is not a detail. `lstart --name` and a `LAB_NAME` line both land here,
// and the recomputed hash is the identity every container of the scenario is
// then created under.
func (l *Lab) SetName(name string) {
	l.name = &name
	l.Hash = util.GenerateURLSafeHash(name)
}

// ---------------------------------------------------------------------------
// Devices
// ---------------------------------------------------------------------------

// Machines returns the devices in insertion order, which is lab.conf
// first-mention order and therefore the sequential deploy order
// (ORDERING.tsv `model/Lab.py:65`). [Lab.ApplyDependencies] rewrites it.
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
//
// The message has no backticks around the name, unlike almost every other one
// in the subsystem; that is Python's and ERROR_CODES.md §2 freezes it.
func (l *Lab) GetMachine(name string) (*Machine, error) {
	machine, ok := l.machines.Get(name)
	if !ok {
		return nil, kerrors.NewMachineNotFoundInScenario(name)
	}
	return machine, nil
}

// NewMachine is `new_machine` (`model/Lab.py:279`): create the device and
// register it, refusing a name that is already taken.
//
// opts may be nil, which is `Machine(lab, name)` with no keyword arguments.
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
//
// When the device already exists the options are SILENTLY IGNORED — not merged
// (`model/Lab.py:315`, oracle-verified). A second `vstart`-shaped call with
// different metas therefore changes nothing, and the port keeps that.
//
// Note the name is registered exactly as given, while [Machine.Name] is the
// stripped form: a name with surrounding whitespace registers under the padded
// key, which is Python's behaviour too (`self.machines[name]` uses the caller's
// string, `model/Lab.py:317`).
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
//
// Collision domains are NOT removed when they are left empty.
//
// TOMBSTONE CRASH. The loop walks `interfaces.values()` and dereferences
// `interface.link` without checking, so a device that has been disconnected
// from a collision domain — leaving a nulled slot — makes this fail with the
// AttributeError Python raises (DIVERGENCES.md 28, [PyRuntimeError]). The
// device is left registered when that happens, because Python's mutation
// happens after the loop.
//
// deleteFS additionally removes `<name>.startup`, `<name>.shutdown` and the
// device directory, in that order. The directory removal is pyfilesystem's
// `removedir`, which refuses a non-empty directory
// ([vfs.ErrDirectoryNotEmpty]) — the two startup files are gone by then, so the
// failure leaves a partially cleaned scenario. Do not "upgrade" it to a
// recursive removal (model.md gotcha 20).
//
// The two spellings are kept apart. `fs.remove` is file-only and `fs.removedir`
// is directory-only, while [vfs.FS.Remove] covers both, so the kind is checked
// first: a *directory* named `pc1.startup` is a [vfs.ErrFileExpected] and a
// plain *file* named `pc1` is a [vfs.ErrDirectoryExpected], both refusals
// leaving the entry on disk exactly as 3.8.3 does (oracle-verified). Without
// the check this deletes what Python declines to touch.
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
//
// A nil device is the `InvocationError` of `model/Lab.py:326`: Python raises it
// when neither argument is given, and splitting the one Python function into
// two Go ones leaves this as its only reachable spelling.
func (l *Lab) RemoveMachineObj(machine *Machine, deleteFS bool) error {
	if machine == nil {
		return kerrors.ErrDeviceNameOrObject
	}
	return l.RemoveMachine(machine.Name, deleteFS)
}

// ---------------------------------------------------------------------------
// Collision domains
// ---------------------------------------------------------------------------

// Links returns the collision domains in insertion order, which is the order
// the managers create the networks in (ORDERING.tsv `model/Lab.py:66`).
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
//
// The duplicate message is missing the word "in" — `Collision domain A is
// already the network scenario.` — which ERROR_CODES.md §0.2 freezes verbatim.
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
//
// It is the parser's main entry point, and the implicit creation is why a
// lab.conf needs no device or collision-domain declarations.
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
//
// It does NOT check that the device belongs to this scenario, so a device from
// another one can be wired to this one's collision domain — and then
// [Lab.RemoveMachine] on the owning scenario removes the back-reference this
// scenario's link still expects. Ported as-is (model.md §1.1).
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
//
// lab.ext is DEFERRED to post-1.0 (PORT_SPEC §0.3), so this always answers
// [kerrors.NewFeatureNotAvailable] with the [kerrors.FeatureLabExt] token — the
// `lab.ext external links are not supported in this release. Use Kathará
// 3.8.x.` message of ERROR_CODES.md §5 — and attaches nothing.
func (l *Lab) AttachExternalLinks(map[string][]ExternalLink) error {
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
}

// CheckIntegrity is `check_integrity` (`model/Lab.py:181`): run [Machine.Check]
// on every device, in scenario order.
//
// The order decides which device's error the user sees, and both backends call
// this at the top of `deploy_lab` — which is why a lab.conf can never produce a
// device with a hole in its interface numbering.
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
//
// Names that are not in the scenario are silently dropped. The result is a set
// and is deliberately unordered — the callers use it for membership only, and
// ORDERING.tsv row O16 forbids inventing an order here.
//
// TOMBSTONE CRASH: a device with a disconnected interface makes this fail with
// Python's AttributeError, exactly as 3.8.3 does (see [Lab.RemoveMachine]).
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
//
// A nil device is NOT skipped. Python reads every name up front,
// `set(map(lambda x: x.name, machines))`, and that map runs before the
// intersection, so a `None` anywhere in the argument dies on `.name` no matter
// what the rest of the list holds. Oracle-verified for both `[pc2, None]` and
// `[None]`: `AttributeError: 'NoneType' object has no attribute 'name'`
// (InternalError at the CLI boundary, ERROR_CODES.md §1.4). Reachable from the
// §7 client API, which takes the device objects from the caller.
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
//
// The sort key is `dependencies.index(name) + 1`, or 0 when the name is not
// listed, and the sort is STABLE. So the devices lab.dep says nothing about
// come FIRST, keeping their insertion order, and the listed ones follow in list
// order; a name repeated in the list takes its first position
// (oracle-verified). This ordering is what the sequential deploy path consumes.
//
// It sets [Lab.HasDependencies] unconditionally, even for an empty list — which
// is what switches both backends off the parallel deploy path.
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
//
// The gate is `is not None`, not truthiness, so a false or empty value IS
// stored and DOES override whatever default the reader would have used. That is
// exactly how `hosthome_mount=False` and `_mount_volumes=False` work
// (NILABILITY.tsv:28).
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
//
// Only six keys are ever read — `image`, `mem`, `cpus`, `num_terms`, `ipv6` and
// `privileged` — and each of them BEATS the device's own meta, falsy values
// included: a scenario-wide `ipv6=False` turns IPv6 off on a device that asked
// for it.
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
//
// It compares the filesystem's type against "os", so a device's own directory —
// a sub-filesystem, type "sub" — never answers true (model.md gotcha 23).
func (l *Lab) HasHostPath() bool { return vfs.Type(l.FS) == "os" }

// CreateSharedFolder is `create_shared_folder` (`model/Lab.py:406`): create the
// scenario's `shared` directory, which every device mounts.
//
// Three details are Python's:
//
//   - a scenario with no host path returns immediately, with no error;
//   - [Lab.SharedPath] is assigned BEFORE the symlink check, so it is set even
//     on the error path;
//   - the symlink refusal escapes as [kerrors.ErrSharedSymlink].
//
// The `except OSError: return` around the body is DEAD CODE in 3.8.3 and is not
// reproduced. pyfilesystem funnels every OS failure through
// `convert_os_errors` (`fs/error_tools.py:34-46`), which re-raises it as an
// `FSError` — and `FSError` derives from `Exception`, not from `OSError`. So a
// lab directory that already holds a *file* named `shared` fails the makedir
// with `fs.errors.DirectoryExpected` and 3.8.3 propagates it (oracle-verified),
// rather than continuing without a shared folder. The failure is returned here
// for the same reason, carrying the [vfs] sentinel the caller can test.
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

// CreateStartupFileFromList is `create_startup_file_from_list`. It is part of
// the published tutorial API (PORT_SPEC §9 Layer D).
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
//
// An empty string is skipped exactly like an absent one, because the test is
// truthiness.
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
