package kerrors

import (
	"io/fs"
	"strings"
)

// The message catalog of docs/port/ERROR_CODES.md §2, in the order of that
// section. Every string is the Python format string byte-for-byte, with the
// raise site of kathara-python/src/Kathara cited above it. Messages that never
// vary are package-level errors; the rest are constructors.
//
// Templates deliberately absent, each unreachable in Go 1.0:
//   - the lab.ext and external-collision-domain variants, superseded by
//     FeatureNotAvailable (§5);
//   - the reserved-name and "In {conf} - Line {n}" file variants, which
//     labfile.ParseError renders (§0.3);
//   - OptionsHandler.py:17, whose menu machinery the bubbletea rebuild deletes;
//   - utils.py:441, whose site takes bytes in Go (DIVERGENCES.md 9).

// ---------------------------------------------------------------------------
// Invocation
// ---------------------------------------------------------------------------

var (
	// ErrSelectedOrExcludedMachines is DockerMachine.py:137,594;
	// KubernetesMachine.py:161,587.
	ErrSelectedOrExcludedMachines = New(ErrInvocation,
		"You can either specify `selected_machines` or `excluded_machines`.")

	// ErrSelectedOrExcludedLinks is DockerLink.py:48; KubernetesLink.py:57.
	ErrSelectedOrExcludedLinks = New(ErrInvocation,
		"You can either specify `selected_links` or `excluded_links`.")

	// ErrSelectOrExcludeDevices is DockerManager.py:147,342;
	// KubernetesManager.py:107,288.
	ErrSelectOrExcludeDevices = New(ErrInvocation, "You can either select or exclude devices.")

	// ErrLabHashOrName is DockerManager.py:693; KubernetesManager.py:685.
	ErrLabHashOrName = New(ErrInvocation, "You must specify a running network scenario hash or name.")

	// ErrDeviceNameOrObject is model/Lab.py:326.
	ErrDeviceNameOrObject = New(ErrInvocation, "You must specify a device name or object.")

	// ErrNoFilesystem is FilesystemMixin.py:79,99,122,142,166,194,218,256,293.
	// It is the taxonomy form of vfs.ErrNoFilesystem, which the vfs leaf raises
	// without importing this package.
	ErrNoFilesystem = New(ErrInvocation, "There is no filesystem associated to this object.")

	// ErrNoFilesystemCreate is FilesystemMixin.py:56, the taxonomy form of
	// vfs.ErrNoFilesystemCreate.
	ErrNoFilesystemCreate = New(ErrInvocation, "Cannot create a file if the filesystem is not set.")

	// ErrStreamReadPermissions is FilesystemMixin.py:178, where Python raises
	// io.UnsupportedOperation; the frozen mapping is Invocation
	// (ERROR_CODES.md §8.8).
	ErrStreamReadPermissions = New(ErrInvocation,
		"To create a file from stream, you must open it with read permissions.")
)

// NewOnlyOneParameter is utils.check_single_not_none_var (utils.py:114) and the
// second branch of check_required_single_not_none_var (utils.py:123). params
// are the keyword names in Python's kwargs order, which is the call order:
// no sorting, the order is deterministic in Python. No trailing period.
func NewOnlyOneParameter(params []string) error {
	return New(ErrInvocation, "You must specify only a parameter among "+strings.Join(params, ", "))
}

// NewOneParameter is the first branch of check_required_single_not_none_var
// (utils.py:121). No trailing period.
func NewOneParameter(params []string) error {
	return New(ErrInvocation, "You must specify a parameter among "+strings.Join(params, ", "))
}

// ---------------------------------------------------------------------------
// Settings
// ---------------------------------------------------------------------------

var (
	// ErrSettingsInvalidJSON is setting/Setting.py:111.
	ErrSettingsInvalidJSON error = &SettingsInvalidError{Reason: "Not a valid JSON."}

	// ErrSettingsNetworksPrefix is setting/Setting.py:218.
	ErrSettingsNetworksPrefix error = &SettingsInvalidError{
		Reason: "Networks Prefix must only contain lowercase letters and underscore.",
	}

	// ErrSettingsDevicePrefix is setting/Setting.py:223.
	ErrSettingsDevicePrefix error = &SettingsInvalidError{
		Reason: "Device Prefix must only contain lowercase letters and underscore.",
	}

	// ErrSettingsDebugLevel is setting/Setting.py:226. The list is
	// AVAILABLE_DEBUG_LEVELS (setting/Setting.py:17) joined with ", "; it is
	// frozen here and in the settings package, which must not diverge from it.
	ErrSettingsDebugLevel error = &SettingsInvalidError{
		Reason: "Debug Level must be one of the following: CRITICAL, ERROR, WARNING, INFO, DEBUG, EXCEPTION.",
	}

	// ErrSettingsManagerType is setting/Setting.py:241.
	ErrSettingsManagerType error = &SettingsInvalidError{Reason: "Manager Type not allowed."}
)

// NewSettingsTerminal is setting/Setting.py:294, also the macOS
// ApplicationNotFoundError translation (ERROR_CODES.md §3).
func NewSettingsTerminal(terminal string) error {
	return &SettingsInvalidError{
		Reason: "Terminal Emulator `" + terminal + "` not valid! Install it before using it.",
	}
}

// NewSettingsInvalid wraps a check failure in the exceptions.py:21 sentence.
// Prefer the frozen reasons above; this exists for the settings rebuild's own
// checks (PORT_SPEC §3.2), which share the wrapper.
func NewSettingsInvalid(reason string) error {
	return &SettingsInvalidError{Reason: reason}
}

// ---------------------------------------------------------------------------
// SettingsNotFound
// ---------------------------------------------------------------------------

// NewSettingsNotFound is setting/Setting.py:104. The error wraps fs.ErrNotExist.
func NewSettingsNotFound(path string) error {
	return &SettingsNotFoundError{Path: path}
}

// ---------------------------------------------------------------------------
// DockerDaemonConnection
// ---------------------------------------------------------------------------

// NewDaemonConnection is DockerManager.py:50,52,73, where the interpolated
// text is str(e) of the underlying client error (exceptions.py:31).
func NewDaemonConnection(cause error) error {
	return Wrap(ErrDaemonConnection, cause,
		"Cannot connect to Docker Daemon, this may indicate that it is not running. "+causeText(cause))
}

// ---------------------------------------------------------------------------
// NotSupported
// ---------------------------------------------------------------------------

var (
	// ErrUpdateRunningDevice is KubernetesManager.py:161,177.
	ErrUpdateRunningDevice = NewNotSupported("Unable to update a running device.")

	// ErrUpdateRunningLab is KubernetesManager.py:747.
	ErrUpdateRunningLab = NewNotSupported("Unable to update a running network scenario.")
)

// NewNotSupported applies the exceptions.py:36 wrapper to msg.
func NewNotSupported(msg string) error {
	return New(ErrNotSupported, "Not Supported: "+msg)
}

// ---------------------------------------------------------------------------
// Privilege
// ---------------------------------------------------------------------------

var (
	// ErrPrivilegeListAllUsers is cli/command/ListCommand.py:58.
	ErrPrivilegeListAllUsers = New(ErrPrivilege,
		"You must be root in order to show all Kathara devices of all users.")

	// ErrPrivilegeWipeAllUsers is cli/command/WipeCommand.py:66.
	ErrPrivilegeWipeAllUsers = New(ErrPrivilege,
		"You must be root in order to wipe all Kathara devices of all users.")

	// ErrPrivilegeLabPrivileged is cli/command/LstartCommand.py:222.
	ErrPrivilegeLabPrivileged = New(ErrPrivilege,
		"You must be root in order to start Kathara devices in privileged mode.")

	// ErrPrivilegeDevicePrivileged is cli/command/VstartCommand.py:219.
	ErrPrivilegeDevicePrivileged = New(ErrPrivilege,
		"You must be root in order to start this Kathara device in privileged mode.")

	// ErrPrivilegeLinkStats is DockerLink.py:268.
	ErrPrivilegeLinkStats = New(ErrPrivilege, "You must be root to get networks statistics of all users.")

	// ErrPrivilegeMachineStats is DockerMachine.py:1038.
	ErrPrivilegeMachineStats = New(ErrPrivilege, "You must be root to get devices statistics of all users.")
)

// NewPrivilegeMachinePrivileged is DockerMachine.py:329. Privilege carries no
// JSON field, so the device name stays inside the message.
func NewPrivilegeMachinePrivileged(machine string) error {
	return New(ErrPrivilege, "You must be root in order to start device `"+machine+"` in privileged mode.")
}

// ---------------------------------------------------------------------------
// HostArchitecture
// ---------------------------------------------------------------------------

// NewHostArchitecture is utils.py:412 (exceptions.py:50).
func NewHostArchitecture(arch string) error {
	return &HostArchError{Arch: arch}
}

// ---------------------------------------------------------------------------
// LabAlreadyExists
// ---------------------------------------------------------------------------

// ErrLabTerminating is KubernetesManager.py:143, the 403 translation of the
// namespace create.
var ErrLabTerminating = New(ErrLabAlreadyExists,
	"Previous network scenario execution is still terminating. Please wait.")

// ---------------------------------------------------------------------------
// LabNotFound
// ---------------------------------------------------------------------------

// NewLabNotFoundDevice is DockerManager.py:99,196,246,284,433,504,930;
// KubernetesManager.py:431,502,836.
func NewLabNotFoundDevice(machine string) error {
	return WrapMachine(machine, "",
		New(ErrLabNotFound, "Device `"+machine+"` is not associated to a network scenario."))
}

// NewLabNotFoundMachine is KubernetesManager.py:54,193, which says "Machine"
// where the Docker manager says "Device".
func NewLabNotFoundMachine(machine string) error {
	return WrapMachine(machine, "",
		New(ErrLabNotFound, "Machine `"+machine+"` is not associated to a network scenario."))
}

// NewLabNotFoundCollisionDomain is DockerManager.py:120,206,256,306;
// KubernetesManager.py:78,237.
func NewLabNotFoundCollisionDomain(link string) error {
	return WrapLink(link, "",
		New(ErrLabNotFound, "Collision domain `"+link+"` is not associated to a network scenario."))
}

// NewLabNotFoundLink is DockerManager.py:1022; KubernetesManager.py:926, which
// say "Link" where the other sites say "Collision domain".
func NewLabNotFoundLink(link string) error {
	return WrapLink(link, "",
		New(ErrLabNotFound, "Link `"+link+"` is not associated to a network scenario."))
}

// ---------------------------------------------------------------------------
// EmptyLab
// ---------------------------------------------------------------------------

// ErrNoDevicesInScenario is exceptions.py:64, raised at LstartCommand.py:185.
var ErrNoDevicesInScenario = New(ErrEmptyLab, "No devices in the current network scenario.")

// ---------------------------------------------------------------------------
// MachineDependency
// ---------------------------------------------------------------------------

// ErrLabDepLoop is parser/netkit/DepParser.py:73.
var ErrLabDepLoop = New(ErrDependencyLoop, "Machines' dependency loop in lab.dep file.")

// ---------------------------------------------------------------------------
// MountDenied
// ---------------------------------------------------------------------------

// ErrHostDriveNotShared is DockerMachine.py:515, the "Mounts denied"
// translation. It is the only MountDenied that escapes to the user.
var ErrHostDriveNotShared = New(ErrMountDenied, "Host drive is not shared with Docker.")

// NewMountDeniedDevice is model/Machine.py:597. Both backends catch it and warn
// instead (DockerMachine.py:324, KubernetesMachine.py:406); the catch must be
// preserved, so this error normally never reaches the CLI.
func NewMountDeniedDevice(machine string) error {
	return WrapMachine(machine, "", New(ErrMountDenied, "Device `"+machine+"` cannot mount volumes."))
}

// ---------------------------------------------------------------------------
// MachineAlreadyExists
// ---------------------------------------------------------------------------

// NewMachineAlreadyExists is exceptions.py:78, raised at model/Lab.py:287,
// DockerMachine.py:224 and KubernetesMachine.py:368 (the k8s 409 translation).
func NewMachineAlreadyExists(machine string) error {
	return WrapMachine(machine, "",
		New(ErrMachineAlreadyExists, "Device with name `"+machine+"` already exists."))
}

// ---------------------------------------------------------------------------
// NonSequentialMachineInterface
// ---------------------------------------------------------------------------

// NewNonSequentialMachineInterface is exceptions.py:83, raised by
// Machine.check() at model/Machine.py:375.
func NewNonSequentialMachineInterface(iface int, machine string) error {
	return &NonSeqInterfaceError{Iface: iface, Machine: machine}
}

// ---------------------------------------------------------------------------
// MachineOption — every site is model/Machine.py. The Option field is the meta
// name being parsed, which is also the lab.conf key.
// ---------------------------------------------------------------------------

// NewOptionSysctl is model/Machine.py:184.
func NewOptionSysctl(machine, value string) error {
	return &OptionError{
		Machine: machine,
		Option:  "sysctl",
		Message: "Invalid sysctl value (`" + value + "`) on `" + machine +
			"`, missing `=` or value not in `net.` namespace.",
	}
}

// NewOptionEnv is model/Machine.py:202.
func NewOptionEnv(machine, value string) error {
	return &OptionError{
		Machine: machine,
		Option:  "env",
		Message: "Invalid env value (`" + value + "`) on `" + machine + "`.",
	}
}

// NewOptionUlimitRange is model/Machine.py:221. The message names the option,
// not the device: add_meta interpolates its own name parameter
// (DIVERGENCES.md 2, ERROR_CODES.md §0.2).
func NewOptionUlimitRange(machine, value string) error {
	return &OptionError{
		Machine: machine,
		Option:  "ulimit",
		Message: "Invalid ulimit value (`" + value + "`) on `ulimit`. Values must be >= -1.",
	}
}

// NewOptionUlimitSoftHard is model/Machine.py:224. The soft limit is literally
// -1 there: the branch is reached only when soft == -1 and hard != -1.
//
// hard is the already-rendered hard limit, i.e. Python's str(int(group)) for
// the (?P<hard>-?\d+) group, and the caller owns that parse: Python's int is
// arbitrary precision and its re \d matches every Unicode decimal digit, so
// `nofile=-1:99999999999999999999999999` renders the full 26-digit number,
// `-1:007` renders 7 and `-1:٣` renders 3 (oracle-verified). A Go int cannot
// hold the first, and no fixed-width conversion can render the value the way
// Python does, so the type here is a string and the model port must normalise
// (e.g. math/big) before calling.
func NewOptionUlimitSoftHard(machine, value, hard string) error {
	return &OptionError{
		Machine: machine,
		Option:  "ulimit",
		Message: "Invalid ulimit value (`" + value + "`) on `ulimit`. " +
			"Soft limit (-1) cannot be greater than hard limit (" + hard + ").",
	}
}

// NewOptionUlimit is model/Machine.py:238, the malformed-value branch.
func NewOptionUlimit(machine, value string) error {
	return &OptionError{
		Machine: machine,
		Option:  "ulimit",
		Message: "Invalid ulimit value (`" + value + "`) on `ulimit`.",
	}
}

// NewOptionPortProtocol is model/Machine.py:254.
func NewOptionPortProtocol(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "port",
		Message: "Port protocol value not valid on `" + machine + "`.",
	}
}

// NewOptionPortValue is model/Machine.py:263.
func NewOptionPortValue(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "port",
		Message: "Port value not valid on `" + machine + "`.",
	}
}

// NewOptionVolumeFormat is model/Machine.py:274 — no trailing period.
func NewOptionVolumeFormat(machine, value string) error {
	return &OptionError{
		Machine: machine,
		Option:  "volume",
		Message: "The volume specified `" + value +
			"` is not in a valid format: <host_path>|<guest_path>|[<mode>]",
	}
}

// NewOptionVolumeMode is model/Machine.py:279. The allowed modes are
// ALLOWED_VOLUME_MODES (model/Machine.py:28) joined with ", ", and the message
// ends with a trailing space (DIVERGENCES.md 8, ERROR_CODES.md §0.2).
func NewOptionVolumeMode(machine, mode, hostPath string) error {
	return &OptionError{
		Machine: machine,
		Option:  "volume",
		Message: "Invalid volume mode `" + mode + "` on `" + hostPath + "` mount. " +
			"Allowed values are ro, rw, rx. ",
	}
}

// NewOptionMemory is model/Machine.py:504,509.
func NewOptionMemory(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "mem",
		Message: "Memory value not valid on `" + machine + "`.",
	}
}

// NewOptionCPU is model/Machine.py:532,537.
func NewOptionCPU(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "cpus",
		Message: "CPU value not valid on `" + machine + "`.",
	}
}

// NewOptionTerminalsNegative is model/Machine.py:569.
func NewOptionTerminalsNegative(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "num_terms",
		Message: "Terminals Number value on `" + machine + "` must be a positive value or zero.",
	}
}

// NewOptionTerminals is model/Machine.py:571.
func NewOptionTerminals(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "num_terms",
		Message: "Terminals Number value not valid on `" + machine + "`.",
	}
}

// NewOptionIPv6 is model/Machine.py:618.
func NewOptionIPv6(machine string) error {
	return &OptionError{
		Machine: machine,
		Option:  "ipv6",
		Message: "IPv6 value not valid on `" + machine + "`.",
	}
}

// ---------------------------------------------------------------------------
// MachineCollisionDomain
// ---------------------------------------------------------------------------

// NewInterfaceAlreadySet is model/Machine.py:106 (variant 1) — the interface
// number carries no backticks.
func NewInterfaceAlreadySet(machine string, iface int) error {
	return &CollisionDomainError{Machine: machine, Iface: iface, Variant: CDVariantInterfaceTaken}
}

// NewMachineAlreadyConnected is model/Machine.py:109 (variant 2).
func NewMachineAlreadyConnected(machine, link string) error {
	return &CollisionDomainError{Machine: machine, Link: link, Variant: CDVariantAlreadyConnected}
}

// NewMachineNotConnected is model/Machine.py:132 (variant 3).
func NewMachineNotConnected(machine, link string) error {
	return &CollisionDomainError{Machine: machine, Link: link, Variant: CDVariantNotConnected}
}

// NewManagerMachineAlreadyConnected is DockerManager.py:209 (variant 4). Same
// text as variant 2, different site.
func NewManagerMachineAlreadyConnected(machine, link string) error {
	return &CollisionDomainError{Machine: machine, Link: link, Variant: CDVariantManagerAlreadyConnected}
}

// NewManagerMachineNotConnected is DockerManager.py:259 (variant 5). Same text
// as variant 3, different site.
func NewManagerMachineNotConnected(machine, link string) error {
	return &CollisionDomainError{Machine: machine, Link: link, Variant: CDVariantManagerNotConnected}
}

// ---------------------------------------------------------------------------
// MachineNotFound
// ---------------------------------------------------------------------------

// NewMachineNotFoundInScenario is model/Lab.py:268,332 — no backticks.
func NewMachineNotFoundInScenario(machine string) error {
	return WrapMachine(machine, "",
		New(ErrMachineNotFound, "Device "+machine+" not in the network scenario."))
}

// NewMachineNotFoundSet is DockerManager.py:151,155;
// KubernetesManager.py:111,115, where Python interpolates a set. The names are
// rendered in the Python set repr, sorted bytewise ascending, and the sorted
// list is the JSON "machines" array (ERROR_CODES.md §0.2).
func NewMachineNotFoundSet(machines []string) error {
	return &MachineSetError{Machines: sortedCopy(machines)}
}

// NewMachineNotFoundQuoted is DockerManager.py:577.
func NewMachineNotFoundQuoted(machine string) error {
	return WrapMachine(machine, "", New(ErrMachineNotFound, "Device `"+machine+"` not found."))
}

// NewMachineNotFoundUnquoted is KubernetesManager.py:573 — same sentence
// without the backticks.
func NewMachineNotFoundUnquoted(machine string) error {
	return WrapMachine(machine, "", New(ErrMachineNotFound, "Device "+machine+" not found."))
}

// ---------------------------------------------------------------------------
// MachineNotRunning / MachineNotReady
// ---------------------------------------------------------------------------

// NewMachineNotRunning is exceptions.py:100, raised at DockerMachine.py:669,
// 781,801, DockerManager.py:199,203,249,253 and KubernetesMachine.py:715,823.
// It is one of the four classes kathara-lab-checker catches by name.
func NewMachineNotRunning(machine string) error {
	return WrapMachine(machine, "", New(ErrMachineNotRunning, "Device `"+machine+"` is not running."))
}

// NewMachineNotReady is exceptions.py:105, raised at KubernetesMachine.py:719.
func NewMachineNotReady(machine string) error {
	return WrapMachine(machine, "", New(ErrMachineNotReady, "Device `"+machine+"` is not ready."))
}

// ---------------------------------------------------------------------------
// MachineBinary
// ---------------------------------------------------------------------------

// NewMachineBinary is exceptions.py:116, raised at DockerMachine.py:874,890
// (OCI_RUNTIME_RE group 3 or 4) and KubernetesMachine.py:917 (the joined
// command). Both Docker sites are caught and warned about at
// DockerMachine.py:562 and :1110.
func NewMachineBinary(binary, machine string) error {
	return &BinaryError{Binary: binary, Machine: machine}
}

// ---------------------------------------------------------------------------
// InterfaceMacAddress
// ---------------------------------------------------------------------------

// NewInterfaceMacAddress is exceptions.py:123, raised at model/Interface.py:31.
func NewInterfaceMacAddress(mac string, iface int, machine string) error {
	return &MacAddressError{MAC: mac, Iface: iface, Machine: machine}
}

// ---------------------------------------------------------------------------
// LinkNotFound / LinkAlreadyExists
// ---------------------------------------------------------------------------

// NewLinkNotFoundInScenario is model/Lab.py:361 — no backticks.
func NewLinkNotFoundInScenario(link string) error {
	return WrapLink(link, "",
		New(ErrLinkNotFound, "Collision domain "+link+" not found in the network scenario."))
}

// NewLinkNotFoundQuoted is DockerManager.py:644 — capital D in "Domain".
func NewLinkNotFoundQuoted(link string) error {
	return WrapLink(link, "", New(ErrLinkNotFound, "Collision Domain `"+link+"` not found."))
}

// NewLinkNotFoundUnquoted is KubernetesManager.py:640 — capital D, no
// backticks.
func NewLinkNotFoundUnquoted(link string) error {
	return WrapLink(link, "", New(ErrLinkNotFound, "Collision Domain "+link+" not found."))
}

// NewLinkAlreadyExists is model/Lab.py:378. The missing "in" is Python's typo,
// preserved (ERROR_CODES.md §0.2).
func NewLinkAlreadyExists(link string) error {
	return WrapLink(link, "",
		New(ErrLinkAlreadyExists, "Collision domain "+link+" is already the network scenario."))
}

// ---------------------------------------------------------------------------
// InvalidImageArchitecture / DockerImageNotFound / DockerPlugin
// ---------------------------------------------------------------------------

// NewInvalidImageArchitecture is exceptions.py:160, raised at
// DockerImage.py:207. The Python class is-a ValueError, which the Python client
// reproduces (ERROR_CODES.md §4).
func NewInvalidImageArchitecture(image, arch string) error {
	return &ImageArchError{Image: image, Arch: arch}
}

// NewDockerImageNotFound is exceptions.py:165, raised at DockerImage.py:168.
func NewDockerImageNotFound(image string) error {
	return WrapImage(image, New(ErrDockerImageNotFound,
		"Docker Image `"+image+"` is not available neither on Docker Hub nor in local repository!"))
}

var (
	// ErrPluginNotFound is DockerPlugin.py:54.
	ErrPluginNotFound = New(ErrDockerPlugin, "Kathara Network Plugin not found on remote Docker connection.")

	// ErrPluginNotEnabled is DockerPlugin.py:79.
	ErrPluginNotEnabled = New(ErrDockerPlugin, "Kathara Network Plugin not enabled on remote Docker connection.")

	// ErrInconsistentState is DockerMachine.py:424,518, translated from the
	// docker 500 "network does not exist" / "endpoint does not exist".
	ErrInconsistentState = New(ErrDockerPlugin,
		"Kathara has been left in an inconsistent state! Please run `kathara wipe`.")
)

// ---------------------------------------------------------------------------
// KubernetesConfigMap
// ---------------------------------------------------------------------------

// NewConfigMapTooLarge is KubernetesConfigMap.py:88. Both sizes are already
// rendered by utils.human_readable_bytes, which lives in internal/util — below
// this package in the graph, so the caller formats them.
func NewConfigMapTooLarge(maxSize, current string) error {
	return New(ErrKubernetesConfigMap,
		"Unable to upload device folder. Maximum supported size: "+maxSize+". Current: "+current+".")
}

// ---------------------------------------------------------------------------
// Syntax
// ---------------------------------------------------------------------------

// NewSyntaxDeviceName is model/Machine.py:62, the device-name regex failure.
// It also fires for API-created devices.
func NewSyntaxDeviceName(name string) error {
	return New(ErrSyntax, "Invalid device name `"+name+"`.")
}

// NewSyntaxInterfaceDefinition is utils.parse_cd_mac_address (utils.py:468).
// The lab.conf parser re-wraps this message in its "In {conf} - Line {n}: {inner}"
// template (LabParser.py:65); raw, it is reachable through the API's
// connect_machine_to_link.
func NewSyntaxInterfaceDefinition(value string) error {
	return New(ErrSyntax, "Invalid interface definition: `"+value+"`.")
}

// NewSyntaxEthNumber is cli/command/VstartCommand.py:243, where s is
// "{cd}/{mac}" when a MAC was given and "{cd}" otherwise.
func NewSyntaxEthNumber(ifaceNumber, s string) error {
	return New(ErrSyntax, "Interface number in `--eth "+ifaceNumber+":"+s+"` is not a number.")
}

// NewSyntax renders msg under the Syntax code, for sites that build their own
// text (labfile.ParseError is the file-scoped form, ERROR_CODES.md §0.3).
func NewSyntax(msg string) error {
	return New(ErrSyntax, msg)
}

// WrapSyntax is NewSyntax keeping the cause reachable: it is how a Go parse
// failure (strconv, regexp) is mapped onto the Python SyntaxError sites, whose
// message never quotes the underlying error.
func WrapSyntax(cause error, msg string) error {
	return Wrap(ErrSyntax, cause, msg)
}

// ---------------------------------------------------------------------------
// Value
// ---------------------------------------------------------------------------

var (
	// ErrSharedSymlink is model/Lab.py:414. It escapes: the enclosing
	// except OSError at :415 does not catch ValueError.
	ErrSharedSymlink = New(ErrValue, "`shared` folder is a symlink, delete it.")

	// ErrInvalidWaitValue is DockerMachine.py:681,690,786,795.
	ErrInvalidWaitValue = New(ErrValue, "Invalid `wait` value.")
)

// NewValueOptionParameter is parser/netkit/OptionParser.py:29, where inner is
// str(e) of the underlying Python exception. Only the outer sentence is
// portable (DIVERGENCES.md 8).
func NewValueOptionParameter(inner string) error {
	return New(ErrValue, "Option parameter not valid: "+inner+".")
}

// NewValue renders msg under the Value code.
func NewValue(msg string) error {
	return New(ErrValue, msg)
}

// WrapValue is NewValue keeping the cause reachable.
func WrapValue(cause error, msg string) error {
	return Wrap(ErrValue, cause, msg)
}

// ---------------------------------------------------------------------------
// OS (human label OSError; it covers Python's IOError, an alias of OSError)
// ---------------------------------------------------------------------------

// NewOSNoConfInDirectory is parser/netkit/LabParser.py:27, the trigger of the
// -F/--force-lab fallback: LstartCommand.py:170 re-raises it unless --force-lab
// is set, while ExecCommand, ConnectCommand, LcleanCommand and LinfoCommand
// fall back to an empty network scenario.
func NewOSNoConfInDirectory(confName string) error {
	return New(ErrOS, "No "+confName+" in given directory.")
}

// NewOSConfEmpty is parser/netkit/LabParser.py:30.
func NewOSConfEmpty(confName string) error {
	return New(ErrOS, confName+" file is empty.")
}

// NewOSCannotOpenConf is parser/netkit/LabParser.py:37, where Python swallows
// the original exception; Go keeps it reachable through the chain.
func NewOSCannotOpenConf(confName string, cause error) error {
	return Wrap(ErrOS, cause, "Cannot open "+confName+" file.")
}

// NewOSCannotOpenLabDep is parser/netkit/DepParser.py:47.
func NewOSCannotOpenLabDep(cause error) error {
	return Wrap(ErrOS, cause, "Cannot open lab.dep file.")
}

// NewOS renders msg under the OS code.
func NewOS(msg string) error {
	return New(ErrOS, msg)
}

// WrapOS is NewOS keeping the underlying filesystem error reachable.
func WrapOS(cause error, msg string) error {
	return Wrap(ErrOS, cause, msg)
}

// ---------------------------------------------------------------------------
// FileNotFound
// ---------------------------------------------------------------------------

// ErrKatharaNotFound is cli/ui/utils.py:130, raised when a terminal is spawned
// and the Kathara executable cannot be located.
var ErrKatharaNotFound = Wrap(ErrFileNotFound, fs.ErrNotExist, "Unable to find Kathara.")

// ErrIptablesNotFound is os/Networking.py:209, live in 1.0 through the
// get_iptables_version carve-out into backend/docker (ERROR_CODES.md §8.4).
var ErrIptablesNotFound = Wrap(ErrFileNotFound, fs.ErrNotExist, "Cannot find `iptables` in the host.")

// NewHostTmpNotFound is DockerPlugin.py:140, where key is HOSTTMP_KEY
// (DockerPlugin.py:17).
func NewHostTmpNotFound(key string) error {
	return Wrap(ErrFileNotFound, fs.ErrNotExist, "Unable to find `"+key+"` in plugin mounts.")
}

// ---------------------------------------------------------------------------
// FileExists / NotADirectory / Permission
// ---------------------------------------------------------------------------

// NewPathNotExist is utils.py:282,303, Python's inverted FileExistsError: it is
// raised when the path does NOT exist, so the Go error wraps fs.ErrNotExist
// (ERROR_CODES.md §0.2).
func NewPathNotExist(path string) error {
	return WrapPath(path, Wrap(ErrFileExists, fs.ErrNotExist, "Path `"+path+"` does not exist."))
}

// NewPathNotDirectory is utils.py:285,306.
func NewPathNotDirectory(path string) error {
	return WrapPath(path, New(ErrNotADirectory, "Path `"+path+"` must be a directory."))
}

// NewVolumePermission is DockerMachine.py:320; KubernetesMachine.py:402. The
// missing permissions are the strings check_directory_permissions collects, in
// its r, w, x order (utils.py:288-296).
func NewVolumePermission(hostPath, guestPath string, missing []string) error {
	return Wrap(ErrPermission, fs.ErrPermission,
		"To mount volume `"+hostPath+"` in `"+guestPath+"` you miss the following permissions: `"+
			strings.Join(missing, ", ")+"`.")
}

// ---------------------------------------------------------------------------
// Connection
// ---------------------------------------------------------------------------

// NewConnectionImagePull is DockerImage.py:163, the docker 500 with "dial tcp"
// in its explanation.
func NewConnectionImagePull(image string) error {
	return WrapImage(image, New(ErrConnection, "Docker Image `"+image+
		"` is not available in local repository and no Internet connection is available to pull it from Docker Hub."))
}

// ErrKubeConfigUnreadable is KubernetesConfig.py:41.
var ErrKubeConfigUnreadable = New(ErrConnection, "Cannot read Kubernetes configuration.")

// ---------------------------------------------------------------------------
// Third-party passthrough (ERROR_CODES.md §1.3)
// ---------------------------------------------------------------------------

// NewDockerAPI is the untranslated docker error Python re-raises at
// DockerMachine.py:386,427,520,876 and DockerImage.py:152. The message is the
// daemon error text, as in Python's "(APIError) ..." line.
func NewDockerAPI(cause error) error {
	return Wrap(ErrDockerAPI, cause, causeText(cause))
}

// NewKubernetesAPI is the untranslated k8s error Python re-raises at
// KubernetesMachine.py:370,837 and KubernetesManager.py:145.
func NewKubernetesAPI(cause error) error {
	return Wrap(ErrKubernetesAPI, cause, causeText(cause))
}

// ---------------------------------------------------------------------------
// Port-new codes (ERROR_CODES.md §1.4)
// ---------------------------------------------------------------------------

// NewFeatureNotAvailable reports feature as deferred to a later release. Use
// the Feature* tokens: they are the closed set of 1.0.
func NewFeatureNotAvailable(feature string) error {
	return &FeatureNotAvailableError{Feature: feature}
}

// ErrWipeConfirmationRequired is the json/jsonl refusal to wipe without
// --force (JSON_CLI_CONTRACT.md §1.5). Human mode prompts instead and can
// never raise it.
var ErrWipeConfirmationRequired = New(ErrConfirmationRequired,
	"Confirmation required: re-run with `--force` to wipe Kathara.")
