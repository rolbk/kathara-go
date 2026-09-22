package model

import (
	"cmp"
	"errors"
	"io"
	"log/slog"
	"math"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/vfs"
)

// machineNameRegex is `model/Machine.py:60`: lower-case ASCII letters, digits
// and underscore, one to thirty characters.
var machineNameRegex = regexp.MustCompile(`^[a-z0-9_]{1,30}$`)

// Machine is a Kathará device (`model/Machine.py`).
type Machine struct {
	Lab *Lab

	// Name is the validated device name, already stripped.
	Name string

	Meta Meta

	APIObject any

	// FS is the device's directory inside the scenario filesystem, or nil when
	// the scenario has no directory of that name.

	FS vfs.FS

	interfaces []Interface
}

// newMachine is `Machine.__init__` (`model/Machine.py:46`).
func newMachine(lab *Lab, name string, opts *MetaOptions) (*Machine, error) {
	name = pyStrip(name)
	if !machineNameRegex.MatchString(name) {
		return nil, kerrors.NewSyntaxDeviceName(name)
	}

	machine := &Machine{
		Lab:  lab,
		Name: name,
		Meta: newMeta(),
	}

	// `self.lab.fs.opendir(self.name) if exists and isdir else None`.
	if vfs.Exists(lab.FS, name) && vfs.IsDir(lab.FS, name) {
		sub, err := vfs.Sub(lab.FS, name)
		if err != nil {
			return nil, err
		}
		machine.FS = sub
	}

	if opts != nil {
		if err := machine.UpdateMeta(*opts); err != nil {
			return nil, err
		}
	}

	return machine, nil
}

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

// Interfaces returns the device's interface slots, sorted by number.
func (m *Machine) Interfaces() []Interface { return slices.Clone(m.interfaces) }

// Interface returns the live interface numbered n. A tombstone reports false:
// the number is taken, but there is no interface there.
func (m *Machine) Interface(n int) (Interface, bool) {
	idx, found := m.interfaceIndex(n)
	if !found || m.interfaces[idx].IsTombstone() {
		return Interface{}, false
	}
	return m.interfaces[idx], true
}

// HasInterfaceNumber reports whether slot n is occupied, tombstones included.
// It is `number in self.interfaces`, the duplicate-number guard of
// [Machine.AddInterface] — and the reason a removed interface's number can
// never be reused.
func (m *Machine) HasInterfaceNumber(n int) bool {
	_, found := m.interfaceIndex(n)
	return found
}

// interfaceIndex is a binary search over the sorted slice.
func (m *Machine) interfaceIndex(n int) (int, bool) {
	return slices.BinarySearchFunc(m.interfaces, n, func(iface Interface, target int) int {
		return cmp.Compare(iface.Number, target)
	})
}

// AddInterface is `Machine.add_interface` (`model/Machine.py:85`): attach the
// device to a collision domain.
func (m *Machine) AddInterface(link *Link, opts AddInterfaceOptions) (Interface, error) {
	number := len(m.interfaces)
	if opts.Number != nil {
		number = *opts.Number
	}

	if m.HasInterfaceNumber(number) {
		return Interface{}, kerrors.NewInterfaceAlreadySet(m.Name, number)
	}

	if link.HasMachine(m.Name) {
		return Interface{}, kerrors.NewMachineAlreadyConnected(m.Name, link.Name)
	}

	iface, err := newInterface(m, link, number, opts.MAC)
	if err != nil {
		return Interface{}, err
	}

	m.insertInterface(iface)
	link.machines.Set(m.Name, m)

	return iface, nil
}

// insertInterface keeps the slice ordered by Number.
func (m *Machine) insertInterface(iface Interface) {
	idx, _ := m.interfaceIndex(iface.Number)
	m.interfaces = slices.Insert(m.interfaces, idx, iface)
}

// RemoveInterface is `Machine.remove_interface` (`model/Machine.py:119`):
// detach the device from a collision domain.
func (m *Machine) RemoveInterface(link *Link) error {
	if !link.HasMachine(m.Name) {
		return kerrors.NewMachineNotConnected(m.Name, link.Name)
	}

	for i, iface := range m.interfaces {
		if !iface.IsTombstone() && iface.Link.Name == link.Name {
			m.interfaces[i] = Interface{Number: iface.Number}
		}
	}
	link.machines.Delete(m.Name)

	return nil
}

// Check is `Machine.check` (`model/Machine.py:356`): the interface numbers must
// be exactly 0..n-1.
func (m *Machine) Check() error {
	slog.Debug("Checking `" + m.Name + "` integrity...")

	if err := m.checkNumbering(); err != nil {
		return err
	}

	slog.Debug("`" + m.Name + "` interfaces are " + m.interfacesRepr() + ".")

	return nil
}

// checkNumbering is the sort-and-compare loop of `check()`.
func (m *Machine) checkNumbering() error {
	if !m.Meta.BridgedIface.IsSet() {
		for i, iface := range m.interfaces {
			if iface.Number != i {
				return kerrors.NewNonSequentialMachineInterface(i, m.Name)
			}
		}
		return nil
	}

	bridged, err := m.bridgedIfaceValue()
	if err != nil {
		return err
	}

	// Where the appended value lands: the count of interface numbers strictly
	// below it. Python's sort is stable, so among equals it would sit last
	// instead — the two spellings cannot differ, because equal values answer
	// the `!= i` test identically wherever they sit.
	// A NaN is the exception: it compares false against everything, so
	// `list.sort()` sees an already-ascending run and leaves it where it was —
	// at the end (oracle-verified for zero, one and two interfaces).
	position := len(m.interfaces)
	if !bridged.isNaN {
		position = sort.Search(len(m.interfaces), func(i int) bool {
			return cmpIntFloat(m.interfaces[i].Number, bridged.value) >= 0
		})
	}

	for i := 0; i <= len(m.interfaces); i++ {
		switch {
		case i < position:
			if m.interfaces[i].Number != i {
				return kerrors.NewNonSequentialMachineInterface(i, m.Name)
			}
		case i == position:
			// `num_iface != i` between a float and an int is Python's exact
			// numeric equality: 1.0 fills slot 1, 1.5 fills nothing, and a NaN
			// is equal to no index at all.
			if bridged.isNaN || cmpIntFloat(i, bridged.value) != 0 {
				return kerrors.NewNonSequentialMachineInterface(i, m.Name)
			}
		default:
			if m.interfaces[i-1].Number != i {
				return kerrors.NewNonSequentialMachineInterface(i, m.Name)
			}
		}
	}
	return nil
}

// bridgedNumber is the `bridged_iface` meta as `check()`'s sort sees it: an
// exact numeric value, or the one float that has no place in an ordering.
type bridgedNumber struct {
	value *big.Float
	isNaN bool
}

// bridgedIfaceValue is the value `check()` appends to the number list.
func (m *Machine) bridgedIfaceValue() (bridgedNumber, error) {
	switch m.Meta.BridgedIface.Kind() {
	case KindInt, KindBool:
		value, err := m.Meta.BridgedIface.pyInt()
		if err != nil {
			return bridgedNumber{}, err
		}
		return bridgedNumber{value: new(big.Float).SetInt(value)}, nil
	case KindFloat:
		value, err := m.Meta.BridgedIface.pyFloat()
		if err != nil {
			return bridgedNumber{}, err
		}
		if math.IsNaN(value) {
			return bridgedNumber{isNaN: true}, nil
		}
		return bridgedNumber{value: new(big.Float).SetFloat64(value)}, nil
	case KindAbsent, KindString, KindStrings:
	}
	if len(m.interfaces) == 0 {
		// Nothing to compare against: `sort()` on a one-element list never
		// calls `<`. The value is then whatever the meta held, which is not
		// int 0, so the sequence check reports interface 0 as missing.
		return bridgedNumber{}, kerrors.NewNonSequentialMachineInterface(0, m.Name)
	}
	return bridgedNumber{}, newBridgedIfaceTypeError(m.Meta.BridgedIface.pyTypeName())
}

// cmpIntFloat compares an interface number against a [big.Float] exactly, the
// way Python compares an int against a float.
func cmpIntFloat(n int, f *big.Float) int {
	return new(big.Float).SetInt64(int64(n)).Cmp(f)
}

// interfacesRepr renders the debug line of `check()`, which interpolates
// `sorted(self.interfaces.items())` — a list of `(number, Interface)` tuples,
// with a tombstone rendering as `None`.
func (m *Machine) interfacesRepr() string {
	parts := make([]string, 0, len(m.interfaces))
	for _, iface := range m.interfaces {
		value := "None"
		if !iface.IsTombstone() {
			value = iface.String()
		}
		parts = append(parts, "("+strconv.Itoa(iface.Number)+", "+value+")")
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// ---------------------------------------------------------------------------
// Meta accessors. Each one names the Python line it mirrors, because the
// precedence differs per key and the differences are not accidental.
// ---------------------------------------------------------------------------

// IsPrivileged is `model/Machine.py:420`: the scenario-wide value first, then
// the device's, then false.
func (m *Machine) IsPrivileged() bool {
	if global, ok := m.Lab.GlobalMachineMetadata("privileged"); ok {
		return global.Truthy()
	}
	if m.Meta.Privileged != nil {
		return *m.Meta.Privileged
	}
	return false
}

// ExecCommands is `get_exec_commands` (`model/Machine.py:429`): the boot
// commands, in the order they were added.
func (m *Machine) ExecCommands() []string { return m.Meta.ExecCommands }

// IsBridged is `model/Machine.py:437`. Unlike [Machine.IsPrivileged] it does
// NOT consult the scenario-wide metadata — the asymmetry is Python's and is
// deliberately preserved.
func (m *Machine) IsBridged() bool {
	return m.Meta.Bridged != nil && *m.Meta.Bridged
}

// Sysctls is `get_sysctls` (`model/Machine.py:445`). Values are ints or
// strings, whichever the literal was.
func (m *Machine) Sysctls() *OrderedMap[string, Scalar] { return m.Meta.Sysctls }

// Envs is `get_envs` (`model/Machine.py:453`).
func (m *Machine) Envs() *OrderedMap[string, string] { return m.Meta.Envs }

// Ulimits is `get_ulimits` (`model/Machine.py:461`).
func (m *Machine) Ulimits() *OrderedMap[string, Ulimit] { return m.Meta.Ulimits }

// Ports is `get_ports` (`model/Machine.py:469`).
func (m *Machine) Ports() *OrderedMap[PortKey, int] { return m.Meta.Ports }

// GetImage is `model/Machine.py:477`: scenario-wide, then device meta, then
// [Defaults.Image].
func (m *Machine) GetImage() string {
	if global, ok := m.Lab.GlobalMachineMetadata("image"); ok {
		return global.String()
	}
	if m.Meta.Image.IsSet() {
		return m.Meta.Image.String()
	}
	return m.Lab.Defaults.Image
}

// GetMem is `model/Machine.py:486`: the memory limit, normalised.
func (m *Machine) GetMem() (string, error) {
	memory, ok := m.Lab.GlobalMachineMetadata("mem")
	if !ok {
		memory = m.Meta.Mem
	}
	if !memory.Truthy() {
		return "", nil
	}

	raw, isString := memory.AsString()
	if !isString {
		// `memory[-1]` on a non-str, which Python answers with an uncaught
		// TypeError. Only the API can store one; the CLI and lab.conf both
		// deal in strings. (A *list* takes a different Python path — it is
		// subscriptable, so it survives to `int(list)` and dies there with a
		// different message — and nothing can produce one.)
		return "", newTypeError("'" + memory.pyTypeName() + "' object is not subscriptable")
	}

	runes := []rune(raw)
	unit := strings.ToLower(string(runes[len(runes)-1]))
	if !slices.Contains([]string{"b", "k", "m", "g"}, unit) {
		value, err := pyBigInt(raw)
		if err != nil {
			return "", kerrors.NewOptionMemory(m.Name)
		}
		return value.String() + "m", nil
	}

	value, err := pyBigInt(string(runes[:len(runes)-1]))
	if err != nil {
		return "", kerrors.NewOptionMemory(m.Name)
	}
	return value.String() + unit, nil
}

// GetCPU is `model/Machine.py:513`: the CPU limit scaled by multiplier, or nil
// when no limit is set.
func (m *Machine) GetCPU(multiplier float64) (*int64, error) {
	value, ok := m.Lab.GlobalMachineMetadata("cpus")
	if !ok {
		if !m.Meta.CPUs.IsSet() {
			return nil, nil
		}
		value = m.Meta.CPUs
	}

	cpus, err := value.pyFloat()
	if err != nil {
		if errors.Is(err, ErrPyTypeError) {
			return nil, err
		}
		return nil, kerrors.NewOptionCPU(m.Name)
	}

	scaled, err := floatToBigInt(cpus * multiplier)
	if err != nil {
		if errors.Is(err, errPyIntNaN) {
			return nil, kerrors.NewOptionCPU(m.Name)
		}
		return nil, err
	}

	result := saturateInt64(scaled)
	return &result, nil
}

// GetShell is `model/Machine.py:541`: the device meta, then
// [Defaults.DeviceShell]. Like [Machine.IsBridged] it ignores the
// scenario-wide metadata.
func (m *Machine) GetShell() string {
	if m.Meta.Shell.IsSet() {
		return m.Meta.Shell.String()
	}
	return m.Lab.Defaults.DeviceShell
}

// GetNumTerms is `model/Machine.py:549`: how many terminals to open, default 1.
func (m *Machine) GetNumTerms() (int, error) {
	value := Int(1)
	if global, ok := m.Lab.GlobalMachineMetadata("num_terms"); ok {
		value = global
	} else if m.Meta.NumTerms.IsSet() {
		value = m.Meta.NumTerms
	}

	numTerms, err := value.pyInt()
	if err != nil {
		if errors.Is(err, ErrPyTypeError) || errors.Is(err, errPyIntInf) {
			return 0, err
		}
		return 0, kerrors.NewOptionTerminals(m.Name)
	}

	if numTerms.Sign() < 0 {
		return 0, kerrors.NewOptionTerminalsNegative(m.Name)
	}

	return saturateInt(numTerms), nil
}

// GetVolumes is `model/Machine.py:575`: the extra bind mounts, or a
// MountDenied error when the scenario is not allowed to mount any.
func (m *Machine) GetVolumes() (*OrderedMap[string, Volume], error) {
	canMount := true
	if m.Meta.Volumes.Len() > 0 {
		if option, ok := m.Lab.GeneralOption("_mount_volumes"); ok {
			canMount = option.Truthy()
		} else {
			canMount = m.Lab.Defaults.VolumeMountPolicy == "Prompt" ||
				m.Lab.Defaults.VolumeMountPolicy == "Always"
		}
	}

	if !canMount {
		return nil, kerrors.NewMountDeniedDevice(m.Name)
	}
	return m.Meta.Volumes, nil
}

// IsIPv6Enabled is `model/Machine.py:599`: scenario-wide, then device meta,
// then [Defaults.EnableIPv6].
func (m *Machine) IsIPv6Enabled() (bool, error) {
	value := Bool(m.Lab.Defaults.EnableIPv6)
	if global, ok := m.Lab.GlobalMachineMetadata("ipv6"); ok {
		value = global
	} else if m.Meta.IPv6.IsSet() {
		value = m.Meta.IPv6
	}

	if enabled, isBool := value.AsBool(); isBool {
		return enabled, nil
	}
	raw, isString := value.AsString()
	if !isString {
		return false, newLowerAttributeError(value.pyTypeName())
	}

	enabled, err := util.StrToBool(raw)
	if err != nil {
		return false, kerrors.NewOptionIPv6(m.Name)
	}
	return enabled, nil
}

// ensureFS is the two lines every overridden file method of
// `model/Machine.py:620-813` starts with: if the device has no directory yet,
// create it inside the scenario filesystem and adopt it.
func (m *Machine) ensureFS() error {
	if m.FS != nil {
		return nil
	}
	if m.Lab == nil || m.Lab.FS == nil {
		return vfs.ErrNoFilesystem
	}
	// 0o777, not 0o755: pyfilesystem's default `Permissions` is `0o777` and the
	// process umask does the rest, so a device directory comes out 0775 under
	// the common umask 002 and 0755 under 022 (oracle-verified). A literal
	// 0o755 would silently drop the group write bit a shared lab relies on.
	if err := m.Lab.FS.MkdirAll(m.Name, 0o777); err != nil {
		return err
	}
	sub, err := vfs.Sub(m.Lab.FS, m.Name)
	if err != nil {
		return err
	}
	m.FS = sub
	return nil
}

// FSType is `FilesystemMixin.fs_type()`: "sub" for a device directory opened
// from the scenario, "" when the device has none.
func (m *Machine) FSType() string { return vfs.Type(m.FS) }

// FSPath is `FilesystemMixin.fs_path()`: the host path of the device directory,
// and whether it has one.
func (m *Machine) FSPath() (string, bool) { return vfs.Path(m.FS) }

// CreateFileFromString is `model/Machine.py:622`.
func (m *Machine) CreateFileFromString(content, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.CreateFileFromString(m.FS, content, dstPath)
}

// UpdateFileFromString is `model/Machine.py:640` — append, no makedirs.
func (m *Machine) UpdateFileFromString(content, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.UpdateFileFromString(m.FS, content, dstPath)
}

// CreateFileFromList is `model/Machine.py:659`.
func (m *Machine) CreateFileFromList(lines []string, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.CreateFileFromList(m.FS, lines, dstPath)
}

// UpdateFileFromList is `model/Machine.py:677`.
func (m *Machine) UpdateFileFromList(lines []string, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.UpdateFileFromList(m.FS, lines, dstPath)
}

// CreateFileFromPath is `model/Machine.py:696`: copy a host file in.
func (m *Machine) CreateFileFromPath(srcPath, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.CreateFileFromPath(m.FS, srcPath, dstPath)
}

// CreateFileFromStream is `model/Machine.py:714`.
func (m *Machine) CreateFileFromStream(stream io.Reader, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.CreateFileFromStream(m.FS, stream, dstPath)
}

// CopyDirectoryFromPath is `model/Machine.py:733`.
func (m *Machine) CopyDirectoryFromPath(srcPath, dstPath string) error {
	if err := m.ensureFS(); err != nil {
		return err
	}
	return vfs.CopyDirectory(m.FS, srcPath, dstPath)
}

// WriteLineBefore is `model/Machine.py:748`. searchedLine is a regular
// expression matched with `search`, not a literal.
func (m *Machine) WriteLineBefore(filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	if err := m.ensureFS(); err != nil {
		return 0, err
	}
	return vfs.WriteLineBefore(m.FS, filePath, lineToAdd, searchedLine, firstOccurrence)
}

// WriteLineAfter is `model/Machine.py:771`.
func (m *Machine) WriteLineAfter(filePath, lineToAdd, searchedLine string, firstOccurrence bool) (int, error) {
	if err := m.ensureFS(); err != nil {
		return 0, err
	}
	return vfs.WriteLineAfter(m.FS, filePath, lineToAdd, searchedLine, firstOccurrence)
}

// DeleteLine is `model/Machine.py:794`.
func (m *Machine) DeleteLine(filePath, lineToDelete string, firstOccurrence bool) (int, error) {
	if err := m.ensureFS(); err != nil {
		return 0, err
	}
	return vfs.DeleteLine(m.FS, filePath, lineToDelete, firstOccurrence)
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// String is `Machine.__str__` (`model/Machine.py:818`), the device description
// the CLI prints.
func (m *Machine) String() string {
	var b strings.Builder
	b.WriteString("Name: " + m.Name)
	b.WriteString("\nImage: " + m.GetImage())

	if len(m.interfaces) > 0 {
		b.WriteString("\nInterfaces: ")
		for _, iface := range m.interfaces {
			if iface.IsTombstone() {
				continue
			}
			b.WriteString("\n\t- " + strconv.Itoa(iface.Number) + ": " + iface.Link.Name)
			if iface.MAC != "" {
				b.WriteString(" (MAC Address: " + iface.MAC + ")")
			}
		}
	}

	if m.Meta.Bridged != nil {
		b.WriteString("\nBridged Connection: " + Bool(*m.Meta.Bridged).String())
	}

	if m.Meta.Sysctls.Len() > 0 {
		b.WriteString("\nSysctls:")
		for _, entry := range m.Meta.Sysctls.Entries() {
			b.WriteString("\n\t- " + entry.Key + " = " + entry.Value.String())
		}
	}

	if m.Meta.Ports.Len() > 0 {
		b.WriteString("\nExposed Ports:")
		for _, entry := range m.Meta.Ports.Entries() {
			b.WriteString("\n\t- Host: " + entry.Key.String() + " -> Guest: " + strconv.Itoa(entry.Value))
		}
	}

	return b.String()
}
