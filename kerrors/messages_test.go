package kerrors_test

import (
	"errors"
	"slices"
	"testing"

	kerr "github.com/KatharaFramework/kathara-go/kerrors"
)

// errCause stands in for a third-party error (docker/k8s client, os) that a
// taxonomy error wraps.
var errCause = errors.New("500 Server Error: Internal Server Error")

type catalogEntry struct {
	name string
	err  error
	code string
	msg  string
}

var catalog = []catalogEntry{
	// Invocation
	{"selected-or-excluded-machines", kerr.ErrSelectedOrExcludedMachines, kerr.CodeInvocation,
		"You can either specify `selected_machines` or `excluded_machines`."},
	{"selected-or-excluded-links", kerr.ErrSelectedOrExcludedLinks, kerr.CodeInvocation,
		"You can either specify `selected_links` or `excluded_links`."},
	{"select-or-exclude-devices", kerr.ErrSelectOrExcludeDevices, kerr.CodeInvocation,
		"You can either select or exclude devices."},
	{"lab-hash-or-name", kerr.ErrLabHashOrName, kerr.CodeInvocation,
		"You must specify a running network scenario hash or name."},
	{"device-name-or-object", kerr.ErrDeviceNameOrObject, kerr.CodeInvocation,
		"You must specify a device name or object."},
	{"no-filesystem", kerr.ErrNoFilesystem, kerr.CodeInvocation,
		"There is no filesystem associated to this object."},
	{"no-filesystem-create", kerr.ErrNoFilesystemCreate, kerr.CodeInvocation,
		"Cannot create a file if the filesystem is not set."},
	{"stream-read-permissions", kerr.ErrStreamReadPermissions, kerr.CodeInvocation,
		"To create a file from stream, you must open it with read permissions."},
	{"only-one-parameter", kerr.NewOnlyOneParameter([]string{"machine_name", "machine"}), kerr.CodeInvocation,
		"You must specify only a parameter among machine_name, machine"},
	{"one-parameter", kerr.NewOneParameter([]string{"machine_name", "machine"}), kerr.CodeInvocation,
		"You must specify a parameter among machine_name, machine"},

	// Settings
	{"settings-invalid-json", kerr.ErrSettingsInvalidJSON, kerr.CodeSettings,
		"Settings file is not valid: Not a valid JSON. Fix it or delete it before launching."},
	{"settings-networks-prefix", kerr.ErrSettingsNetworksPrefix, kerr.CodeSettings,
		"Settings file is not valid: Networks Prefix must only contain lowercase letters and underscore. " +
			"Fix it or delete it before launching."},
	{"settings-device-prefix", kerr.ErrSettingsDevicePrefix, kerr.CodeSettings,
		"Settings file is not valid: Device Prefix must only contain lowercase letters and underscore. " +
			"Fix it or delete it before launching."},
	{"settings-debug-level", kerr.ErrSettingsDebugLevel, kerr.CodeSettings,
		"Settings file is not valid: Debug Level must be one of the following: " +
			"CRITICAL, ERROR, WARNING, INFO, DEBUG, EXCEPTION. Fix it or delete it before launching."},
	{"settings-manager-type", kerr.ErrSettingsManagerType, kerr.CodeSettings,
		"Settings file is not valid: Manager Type not allowed. Fix it or delete it before launching."},
	{"settings-terminal", kerr.NewSettingsTerminal("/usr/bin/xterm"), kerr.CodeSettings,
		"Settings file is not valid: Terminal Emulator `/usr/bin/xterm` not valid! Install it before using it. " +
			"Fix it or delete it before launching."},

	// SettingsNotFound
	{"settings-not-found", kerr.NewSettingsNotFound("/root/.config/kathara.conf"), kerr.CodeSettingsNotFound,
		"Settings file not found in path `/root/.config/kathara.conf`."},

	// DockerDaemonConnection
	{"daemon-connection", kerr.NewDaemonConnection(errCause), kerr.CodeDockerDaemonConnection,
		"Cannot connect to Docker Daemon, this may indicate that it is not running. " +
			"500 Server Error: Internal Server Error"},

	// NotSupported
	{"update-running-device", kerr.ErrUpdateRunningDevice, kerr.CodeNotSupported,
		"Not Supported: Unable to update a running device."},
	{"update-running-lab", kerr.ErrUpdateRunningLab, kerr.CodeNotSupported,
		"Not Supported: Unable to update a running network scenario."},

	// Privilege
	{"privilege-list-all", kerr.ErrPrivilegeListAllUsers, kerr.CodePrivilege,
		"You must be root in order to show all Kathara devices of all users."},
	{"privilege-wipe-all", kerr.ErrPrivilegeWipeAllUsers, kerr.CodePrivilege,
		"You must be root in order to wipe all Kathara devices of all users."},
	{"privilege-lab-privileged", kerr.ErrPrivilegeLabPrivileged, kerr.CodePrivilege,
		"You must be root in order to start Kathara devices in privileged mode."},
	{"privilege-device-privileged", kerr.ErrPrivilegeDevicePrivileged, kerr.CodePrivilege,
		"You must be root in order to start this Kathara device in privileged mode."},
	{"privilege-link-stats", kerr.ErrPrivilegeLinkStats, kerr.CodePrivilege,
		"You must be root to get networks statistics of all users."},
	{"privilege-machine-stats", kerr.ErrPrivilegeMachineStats, kerr.CodePrivilege,
		"You must be root to get devices statistics of all users."},
	{"privilege-machine", kerr.NewPrivilegeMachinePrivileged("pc1"), kerr.CodePrivilege,
		"You must be root in order to start device `pc1` in privileged mode."},

	// HostArchitecture
	{"host-architecture", kerr.NewHostArchitecture("riscv64"), kerr.CodeHostArchitecture,
		"Not implemented for host architecture `riscv64`."},

	// LabAlreadyExists
	{"lab-terminating", kerr.ErrLabTerminating, kerr.CodeLabAlreadyExists,
		"Previous network scenario execution is still terminating. Please wait."},

	// LabNotFound
	{"lab-not-found-device", kerr.NewLabNotFoundDevice("pc1"), kerr.CodeLabNotFound,
		"Device `pc1` is not associated to a network scenario."},
	{"lab-not-found-machine", kerr.NewLabNotFoundMachine("pc1"), kerr.CodeLabNotFound,
		"Machine `pc1` is not associated to a network scenario."},
	{"lab-not-found-cd", kerr.NewLabNotFoundCollisionDomain("A"), kerr.CodeLabNotFound,
		"Collision domain `A` is not associated to a network scenario."},
	{"lab-not-found-link", kerr.NewLabNotFoundLink("A"), kerr.CodeLabNotFound,
		"Link `A` is not associated to a network scenario."},

	// EmptyLab, MachineDependency
	{"empty-lab", kerr.ErrNoDevicesInScenario, kerr.CodeEmptyLab,
		"No devices in the current network scenario."},
	{"dependency-loop", kerr.ErrLabDepLoop, kerr.CodeMachineDependency,
		"Machines' dependency loop in lab.dep file."},

	// MountDenied
	{"mount-denied-host", kerr.ErrHostDriveNotShared, kerr.CodeMountDenied,
		"Host drive is not shared with Docker."},
	{"mount-denied-device", kerr.NewMountDeniedDevice("pc1"), kerr.CodeMountDenied,
		"Device `pc1` cannot mount volumes."},

	// MachineAlreadyExists, NonSequentialMachineInterface
	{"machine-already-exists", kerr.NewMachineAlreadyExists("pc1"), kerr.CodeMachineAlreadyExists,
		"Device with name `pc1` already exists."},
	{"non-sequential-iface", kerr.NewNonSequentialMachineInterface(1, "pc1"), kerr.CodeNonSequentialMachineIface,
		"Interface `1` missing on device `pc1`."},

	// MachineOption
	{"option-sysctl", kerr.NewOptionSysctl("pc1", "kernel.shmmax=1"), kerr.CodeMachineOption,
		"Invalid sysctl value (`kernel.shmmax=1`) on `pc1`, missing `=` or value not in `net.` namespace."},
	{"option-env", kerr.NewOptionEnv("pc1", "FOO"), kerr.CodeMachineOption,
		"Invalid env value (`FOO`) on `pc1`."},
	{"option-ulimit-range", kerr.NewOptionUlimitRange("pc1", "nofile=-2"), kerr.CodeMachineOption,
		"Invalid ulimit value (`nofile=-2`) on `ulimit`. Values must be >= -1."},
	{"option-ulimit-soft-hard", kerr.NewOptionUlimitSoftHard("pc1", "nofile=-1:10", "10"), kerr.CodeMachineOption,
		"Invalid ulimit value (`nofile=-1:10`) on `ulimit`. " +
			"Soft limit (-1) cannot be greater than hard limit (10)."},
	{"option-ulimit", kerr.NewOptionUlimit("pc1", "nofile"), kerr.CodeMachineOption,
		"Invalid ulimit value (`nofile`) on `ulimit`."},
	{"option-port-protocol", kerr.NewOptionPortProtocol("pc1"), kerr.CodeMachineOption,
		"Port protocol value not valid on `pc1`."},
	{"option-port-value", kerr.NewOptionPortValue("pc1"), kerr.CodeMachineOption,
		"Port value not valid on `pc1`."},
	{"option-volume-format", kerr.NewOptionVolumeFormat("pc1", "/host"), kerr.CodeMachineOption,
		"The volume specified `/host` is not in a valid format: <host_path>|<guest_path>|[<mode>]"},
	{"option-volume-mode", kerr.NewOptionVolumeMode("pc1", "rwx", "/host"), kerr.CodeMachineOption,
		"Invalid volume mode `rwx` on `/host` mount. Allowed values are ro, rw, rx. "},
	{"option-memory", kerr.NewOptionMemory("pc1"), kerr.CodeMachineOption,
		"Memory value not valid on `pc1`."},
	{"option-cpu", kerr.NewOptionCPU("pc1"), kerr.CodeMachineOption,
		"CPU value not valid on `pc1`."},
	{"option-terminals-negative", kerr.NewOptionTerminalsNegative("pc1"), kerr.CodeMachineOption,
		"Terminals Number value on `pc1` must be a positive value or zero."},
	{"option-terminals", kerr.NewOptionTerminals("pc1"), kerr.CodeMachineOption,
		"Terminals Number value not valid on `pc1`."},
	{"option-ipv6", kerr.NewOptionIPv6("pc1"), kerr.CodeMachineOption,
		"IPv6 value not valid on `pc1`."},

	// MachineCollisionDomain
	{"cd-iface-taken", kerr.NewInterfaceAlreadySet("pc1", 0), kerr.CodeMachineCollisionDomain,
		"Interface 0 already set on device `pc1`."},
	{"cd-already-connected", kerr.NewMachineAlreadyConnected("pc1", "A"), kerr.CodeMachineCollisionDomain,
		"Device `pc1` is already connected to collision domain `A`."},
	{"cd-not-connected", kerr.NewMachineNotConnected("pc1", "A"), kerr.CodeMachineCollisionDomain,
		"Device `pc1` is not connected to collision domain `A`."},
	{"cd-manager-already-connected", kerr.NewManagerMachineAlreadyConnected("pc1", "A"),
		kerr.CodeMachineCollisionDomain,
		"Device `pc1` is already connected to collision domain `A`."},
	{"cd-manager-not-connected", kerr.NewManagerMachineNotConnected("pc1", "A"), kerr.CodeMachineCollisionDomain,
		"Device `pc1` is not connected to collision domain `A`."},

	// MachineNotFound
	{"machine-not-in-scenario", kerr.NewMachineNotFoundInScenario("pc1"), kerr.CodeMachineNotFound,
		"Device pc1 not in the network scenario."},
	{"machines-not-in-scenario", kerr.NewMachineNotFoundSet([]string{"pc3", "pc1"}), kerr.CodeMachineNotFound,
		"The following devices are not in the network scenario: {'pc1', 'pc3'}."},
	{"machine-not-found-quoted", kerr.NewMachineNotFoundQuoted("pc1"), kerr.CodeMachineNotFound,
		"Device `pc1` not found."},
	{"machine-not-found-unquoted", kerr.NewMachineNotFoundUnquoted("pc1"), kerr.CodeMachineNotFound,
		"Device pc1 not found."},

	// MachineNotRunning, MachineNotReady, MachineBinary, InterfaceMacAddress
	{"machine-not-running", kerr.NewMachineNotRunning("pc1"), kerr.CodeMachineNotRunning,
		"Device `pc1` is not running."},
	{"machine-not-ready", kerr.NewMachineNotReady("pc1"), kerr.CodeMachineNotReady,
		"Device `pc1` is not ready."},
	{"machine-binary", kerr.NewMachineBinary("frr", "pc1"), kerr.CodeMachineBinary,
		"Binary `frr` not found in device `pc1`."},
	{"mac-address", kerr.NewInterfaceMacAddress("00:00:00:00:00:0g", 0, "pc1"), kerr.CodeInterfaceMacAddress,
		"MAC address 00:00:00:00:00:0g on interface `0` of device `pc1` is invalid."},

	// LinkNotFound, LinkAlreadyExists
	{"link-not-in-scenario", kerr.NewLinkNotFoundInScenario("A"), kerr.CodeLinkNotFound,
		"Collision domain A not found in the network scenario."},
	{"link-not-found-quoted", kerr.NewLinkNotFoundQuoted("A"), kerr.CodeLinkNotFound,
		"Collision Domain `A` not found."},
	{"link-not-found-unquoted", kerr.NewLinkNotFoundUnquoted("A"), kerr.CodeLinkNotFound,
		"Collision Domain A not found."},
	{"link-already-exists", kerr.NewLinkAlreadyExists("A"), kerr.CodeLinkAlreadyExists,
		"Collision domain A is already the network scenario."},

	// Docker image / plugin
	{"image-architecture", kerr.NewInvalidImageArchitecture("kathara/base", "arm64"),
		kerr.CodeInvalidImageArchitecture,
		"Docker Image `kathara/base` is not compatible with your host architecture `arm64`"},
	{"image-not-found", kerr.NewDockerImageNotFound("kathara/base"), kerr.CodeDockerImageNotFound,
		"Docker Image `kathara/base` is not available neither on Docker Hub nor in local repository!"},
	{"plugin-not-found", kerr.ErrPluginNotFound, kerr.CodeDockerPlugin,
		"Kathara Network Plugin not found on remote Docker connection."},
	{"plugin-not-enabled", kerr.ErrPluginNotEnabled, kerr.CodeDockerPlugin,
		"Kathara Network Plugin not enabled on remote Docker connection."},
	{"plugin-inconsistent-state", kerr.ErrInconsistentState, kerr.CodeDockerPlugin,
		"Kathara has been left in an inconsistent state! Please run `kathara wipe`."},

	// KubernetesConfigMap
	{"configmap-too-large", kerr.NewConfigMapTooLarge("3.0 MB", "4.2 MB"), kerr.CodeKubernetesConfigMap,
		"Unable to upload device folder. Maximum supported size: 3.0 MB. Current: 4.2 MB."},

	// Syntax
	{"syntax-device-name", kerr.NewSyntaxDeviceName("PC1"), kerr.CodeSyntax,
		"Invalid device name `PC1`."},
	{"syntax-interface-definition", kerr.NewSyntaxInterfaceDefinition("A/00:00:00:00:00:01/x"), kerr.CodeSyntax,
		"Invalid interface definition: `A/00:00:00:00:00:01/x`."},
	{"syntax-eth-number", kerr.NewSyntaxEthNumber("x", "A/00:00:00:00:00:01"), kerr.CodeSyntax,
		"Interface number in `--eth x:A/00:00:00:00:00:01` is not a number."},

	// Value
	{"value-shared-symlink", kerr.ErrSharedSymlink, kerr.CodeValue,
		"`shared` folder is a symlink, delete it."},
	{"value-wait", kerr.ErrInvalidWaitValue, kerr.CodeValue,
		"Invalid `wait` value."},
	{"value-option-parameter", kerr.NewValueOptionParameter("not enough values to unpack (expected 2, got 1)"),
		kerr.CodeValue,
		"Option parameter not valid: not enough values to unpack (expected 2, got 1)."},

	// OS
	{"os-no-conf", kerr.NewOSNoConfInDirectory("lab.conf"), kerr.CodeOS,
		"No lab.conf in given directory."},
	{"os-conf-empty", kerr.NewOSConfEmpty("lab.conf"), kerr.CodeOS,
		"lab.conf file is empty."},
	{"os-cannot-open-conf", kerr.NewOSCannotOpenConf("lab.conf", errCause), kerr.CodeOS,
		"Cannot open lab.conf file."},
	{"os-cannot-open-lab-dep", kerr.NewOSCannotOpenLabDep(errCause), kerr.CodeOS,
		"Cannot open lab.dep file."},

	// FileNotFound
	{"kathara-not-found", kerr.ErrKatharaNotFound, kerr.CodeFileNotFound,
		"Unable to find Kathara."},
	{"iptables-not-found", kerr.ErrIptablesNotFound, kerr.CodeFileNotFound,
		"Cannot find `iptables` in the host."},
	{"hosttmp-not-found", kerr.NewHostTmpNotFound("tmp"), kerr.CodeFileNotFound,
		"Unable to find `tmp` in plugin mounts."},

	// FileExists, NotADirectory, Permission
	{"path-not-exist", kerr.NewPathNotExist("/home/user/vol"), kerr.CodeFileExists,
		"Path `/home/user/vol` does not exist."},
	{"path-not-directory", kerr.NewPathNotDirectory("/home/user/vol"), kerr.CodeNotADirectory,
		"Path `/home/user/vol` must be a directory."},
	{"volume-permission", kerr.NewVolumePermission("/host", "/guest", []string{"read (r)", "write (w)"}),
		kerr.CodePermission,
		"To mount volume `/host` in `/guest` you miss the following permissions: `read (r), write (w)`."},

	// Connection
	{"connection-image-pull", kerr.NewConnectionImagePull("kathara/base"), kerr.CodeConnection,
		"Docker Image `kathara/base` is not available in local repository and " +
			"no Internet connection is available to pull it from Docker Hub."},
	{"connection-kube-config", kerr.ErrKubeConfigUnreadable, kerr.CodeConnection,
		"Cannot read Kubernetes configuration."},

	// Third-party passthrough
	{"docker-api", kerr.NewDockerAPI(errCause), kerr.CodeDockerAPI,
		"500 Server Error: Internal Server Error"},
	{"kubernetes-api", kerr.NewKubernetesAPI(errCause), kerr.CodeKubernetesAPI,
		"500 Server Error: Internal Server Error"},

	// FeatureNotAvailable
	{"feature-lab-ext", kerr.NewFeatureNotAvailable(kerr.FeatureLabExt), kerr.CodeFeatureNotAvailable,
		"lab.ext external links are not supported in this release. Use Kathará 3.8.x."},
	{"feature-linfo", kerr.NewFeatureNotAvailable(kerr.FeatureLinfo), kerr.CodeFeatureNotAvailable,
		"The linfo command is not supported in this release. Use Kathará 3.8.x."},
	{"feature-stats-sampling", kerr.NewFeatureNotAvailable(kerr.FeatureStatsSampling),
		kerr.CodeFeatureNotAvailable,
		"Resource statistics sampling is not supported in this release. Use Kathará 3.8.x."},
	{"feature-webhooks", kerr.NewFeatureNotAvailable(kerr.FeatureWebhooks), kerr.CodeFeatureNotAvailable,
		"Docker Hub image listing is not supported in this release. Use Kathará 3.8.x."},

	// ConfirmationRequired
	{"wipe-confirmation", kerr.ErrWipeConfirmationRequired, kerr.CodeConfirmationRequired,
		"Confirmation required: re-run with `--force` to wipe Kathara."},
}

func TestCatalogMessages(t *testing.T) {
	for _, entry := range catalog {
		t.Run(entry.name, func(t *testing.T) {
			if got := entry.err.Error(); got != entry.msg {
				t.Errorf("message:\n got %q\nwant %q", got, entry.msg)
			}
			if got := kerr.Code(entry.err); got != entry.code {
				t.Errorf("code: got %q, want %q", got, entry.code)
			}
		})
	}
}

// TestCatalogNamesUnique guards the table itself against copy-paste.
func TestCatalogNamesUnique(t *testing.T) {
	seen := make(map[string]bool, len(catalog))
	for _, entry := range catalog {
		if seen[entry.name] {
			t.Errorf("duplicate catalog entry %q", entry.name)
		}
		seen[entry.name] = true
	}
}

// TestCatalogCoversEmittableCodes checks that the catalog can produce every code
// a Go 1.0 run can emit: the registry minus the RESERVED codes and minus
// InternalError, which is the fallback for unmapped errors and has no template.
func TestCatalogCoversEmittableCodes(t *testing.T) {
	notEmittable := map[string]bool{
		kerr.CodeClassNotFound:            true, // cobra reports unknown commands
		kerr.CodeHTTPConnection:           true, // webhooks/ deferred
		kerr.CodeInstantiation:            true, // no singletons in Go
		kerr.CodeInterfaceNotFound:        true, // os/Networking.py:38 deferred
		kerr.CodeTest:                     true, // RESERVED-DEAD
		kerr.CodeMachineSignatureNotFound: true, // RESERVED-DEAD
		kerr.CodeInternalError:            true, // fallback, no template
	}

	produced := make(map[string]bool, len(catalog))
	for _, entry := range catalog {
		produced[entry.code] = true
	}

	for _, code := range kerr.AllCodes {
		switch {
		case notEmittable[code] && produced[code]:
			t.Errorf("code %q is documented as never emitted but the catalog produces it", code)
		case !notEmittable[code] && !produced[code]:
			t.Errorf("code %q is emittable but no catalog entry produces it", code)
		}
	}
}

// TestMachineSetErrorSorting pins the frozen set rendering: sorted bytewise,
// Python repr syntax, and the caller's slice left untouched.
func TestMachineSetErrorSorting(t *testing.T) {
	input := []string{"pc3", "pc1", "pc2"}
	err := kerr.NewMachineNotFoundSet(input)

	want := "The following devices are not in the network scenario: {'pc1', 'pc2', 'pc3'}."
	if got := err.Error(); got != want {
		t.Errorf("message:\n got %q\nwant %q", got, want)
	}

	if !slices.Equal(input, []string{"pc3", "pc1", "pc2"}) {
		t.Errorf("constructor reordered the caller's slice: %v", input)
	}

	var set *kerr.MachineSetError
	if !errors.As(err, &set) {
		t.Fatalf("errors.As(*MachineSetError) = false")
	}
	if !slices.Equal(set.Machines, []string{"pc1", "pc2", "pc3"}) {
		t.Errorf("Machines = %v, want sorted", set.Machines)
	}
}

// TestMachineSetErrorEmpty pins Python's repr of an empty set. The raise sites
// cannot reach it (has_machines already proved the difference non-empty), but
// the rendering must not become "{}".
func TestMachineSetErrorEmpty(t *testing.T) {
	err := kerr.NewMachineNotFoundSet(nil)

	want := "The following devices are not in the network scenario: set()."
	if got := err.Error(); got != want {
		t.Errorf("message:\n got %q\nwant %q", got, want)
	}
}

func TestMachineSetErrorQuotingIsAlwaysSingle(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{`it's`, `The following devices are not in the network scenario: {'it's'}.`},
		{`a\b`, `The following devices are not in the network scenario: {'a\b'}.`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kerr.NewMachineNotFoundSet([]string{tc.name}).Error(); got != tc.want {
				t.Errorf("message:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestOptionUlimitHardLimitIsArbitraryPrecision pins that the hard limit of
// model/Machine.py:224 is rendered from the caller's decimal, not from a
// fixed-width Go integer: Python's int() is arbitrary precision and its \d
// matches Unicode digits, so the message must be able to carry a value no Go
// int can hold, and the normalisation str(int(...)) applies (oracle:
// `nofile=-1:007` renders 7, `nofile=-1:٣` renders 3).
func TestOptionUlimitHardLimitIsArbitraryPrecision(t *testing.T) {
	cases := []struct {
		value string
		hard  string
		want  string
	}{
		{
			"nofile=-1:99999999999999999999999999", "99999999999999999999999999",
			"Invalid ulimit value (`nofile=-1:99999999999999999999999999`) on `ulimit`. " +
				"Soft limit (-1) cannot be greater than hard limit (99999999999999999999999999).",
		},
		{
			"nofile=-1:007", "7",
			"Invalid ulimit value (`nofile=-1:007`) on `ulimit`. " +
				"Soft limit (-1) cannot be greater than hard limit (7).",
		},
		{
			"nofile=-1:٣", "3",
			"Invalid ulimit value (`nofile=-1:٣`) on `ulimit`. " +
				"Soft limit (-1) cannot be greater than hard limit (3).",
		},
	}

	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			if got := kerr.NewOptionUlimitSoftHard("pc1", tc.value, tc.hard).Error(); got != tc.want {
				t.Errorf("message:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestSingleParameterJoinOrder pins that the parameter names keep the caller's
// order: Python interpolates kwargs, whose order is the call order.
func TestSingleParameterJoinOrder(t *testing.T) {
	err := kerr.NewOnlyOneParameter([]string{"machine", "machine_name"})

	want := "You must specify only a parameter among machine, machine_name"
	if got := err.Error(); got != want {
		t.Errorf("message:\n got %q\nwant %q", got, want)
	}
}

// TestFeatureNotAvailableUnknownToken keeps an out-of-set token renderable
// instead of empty.
func TestFeatureNotAvailableUnknownToken(t *testing.T) {
	err := kerr.NewFeatureNotAvailable("nsenter")

	want := "`nsenter` is not supported in this release. Use Kathará 3.8.x."
	if got := err.Error(); got != want {
		t.Errorf("message:\n got %q\nwant %q", got, want)
	}
	if got := kerr.Code(err); got != kerr.CodeFeatureNotAvailable {
		t.Errorf("code: got %q, want %q", got, kerr.CodeFeatureNotAvailable)
	}
}

// TestDaemonConnectionNilCause keeps the constructor total: a nil cause renders
// the prefix alone instead of panicking.
func TestDaemonConnectionNilCause(t *testing.T) {
	err := kerr.NewDaemonConnection(nil)

	want := "Cannot connect to Docker Daemon, this may indicate that it is not running. "
	if got := err.Error(); got != want {
		t.Errorf("message:\n got %q\nwant %q", got, want)
	}
	if got := kerr.Code(err); got != kerr.CodeDockerDaemonConnection {
		t.Errorf("code: got %q, want %q", got, kerr.CodeDockerDaemonConnection)
	}
}

// TestPassthroughNilCause does the same for the passthrough constructors.
func TestPassthroughNilCause(t *testing.T) {
	for name, err := range map[string]error{
		"docker":     kerr.NewDockerAPI(nil),
		"kubernetes": kerr.NewKubernetesAPI(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if got := err.Error(); got != "" {
				t.Errorf("message: got %q, want empty", got)
			}
		})
	}
}
