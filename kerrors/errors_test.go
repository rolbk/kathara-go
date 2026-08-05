package kerrors_test

import (
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	kerr "github.com/KatharaFramework/kathara-go/kerrors"
)

// TestIsClassSentinel checks that every catalog error answers errors.Is for its
// class sentinel: the Go API surface of PORT_SPEC §4.3 is matched that way, and
// kathara-lab-checker's Python classes map onto the same identities.
func TestIsClassSentinel(t *testing.T) {
	cases := []struct {
		name  string
		err   error
		class error
	}{
		{"invocation", kerr.ErrSelectOrExcludeDevices, kerr.ErrInvocation},
		{"settings", kerr.ErrSettingsInvalidJSON, kerr.ErrSettings},
		{"settings-not-found", kerr.NewSettingsNotFound("/x"), kerr.ErrSettingsNotFound},
		{"daemon-connection", kerr.NewDaemonConnection(errCause), kerr.ErrDaemonConnection},
		{"not-supported", kerr.ErrUpdateRunningDevice, kerr.ErrNotSupported},
		{"privilege", kerr.ErrPrivilegeWipeAllUsers, kerr.ErrPrivilege},
		{"host-architecture", kerr.NewHostArchitecture("riscv64"), kerr.ErrHostArchitecture},
		{"lab-already-exists", kerr.ErrLabTerminating, kerr.ErrLabAlreadyExists},
		{"lab-not-found", kerr.NewLabNotFoundDevice("pc1"), kerr.ErrLabNotFound},
		{"empty-lab", kerr.ErrNoDevicesInScenario, kerr.ErrEmptyLab},
		{"dependency-loop", kerr.ErrLabDepLoop, kerr.ErrDependencyLoop},
		{"mount-denied", kerr.NewMountDeniedDevice("pc1"), kerr.ErrMountDenied},
		{"machine-already-exists", kerr.NewMachineAlreadyExists("pc1"), kerr.ErrMachineAlreadyExists},
		{"non-sequential-iface", kerr.NewNonSequentialMachineInterface(1, "pc1"),
			kerr.ErrNonSequentialMachineInterface},
		{"machine-option", kerr.NewOptionIPv6("pc1"), kerr.ErrMachineOption},
		{"collision-domain", kerr.NewMachineNotConnected("pc1", "A"), kerr.ErrMachineCollisionDomain},
		{"machine-not-found", kerr.NewMachineNotFoundQuoted("pc1"), kerr.ErrMachineNotFound},
		{"machine-set-not-found", kerr.NewMachineNotFoundSet([]string{"pc1"}), kerr.ErrMachineNotFound},
		{"machine-not-running", kerr.NewMachineNotRunning("pc1"), kerr.ErrMachineNotRunning},
		{"machine-not-ready", kerr.NewMachineNotReady("pc1"), kerr.ErrMachineNotReady},
		{"machine-binary", kerr.NewMachineBinary("frr", "pc1"), kerr.ErrMachineBinary},
		{"mac-address", kerr.NewInterfaceMacAddress("x", 0, "pc1"), kerr.ErrInterfaceMacAddress},
		{"link-not-found", kerr.NewLinkNotFoundQuoted("A"), kerr.ErrLinkNotFound},
		{"link-already-exists", kerr.NewLinkAlreadyExists("A"), kerr.ErrLinkAlreadyExists},
		{"image-architecture", kerr.NewInvalidImageArchitecture("i", "a"), kerr.ErrInvalidImageArchitecture},
		{"image-not-found", kerr.NewDockerImageNotFound("i"), kerr.ErrDockerImageNotFound},
		{"docker-plugin", kerr.ErrInconsistentState, kerr.ErrDockerPlugin},
		{"configmap", kerr.NewConfigMapTooLarge("1 MB", "2 MB"), kerr.ErrKubernetesConfigMap},
		{"syntax", kerr.NewSyntaxDeviceName("PC1"), kerr.ErrSyntax},
		{"value", kerr.ErrSharedSymlink, kerr.ErrValue},
		{"os", kerr.NewOSConfEmpty("lab.conf"), kerr.ErrOS},
		{"file-not-found", kerr.ErrIptablesNotFound, kerr.ErrFileNotFound},
		{"file-exists", kerr.NewPathNotExist("/x"), kerr.ErrFileExists},
		{"not-a-directory", kerr.NewPathNotDirectory("/x"), kerr.ErrNotADirectory},
		{"permission", kerr.NewVolumePermission("/h", "/g", []string{"read (r)"}), kerr.ErrPermission},
		{"connection", kerr.ErrKubeConfigUnreadable, kerr.ErrConnection},
		{"docker-api", kerr.NewDockerAPI(errCause), kerr.ErrDockerAPI},
		{"kubernetes-api", kerr.NewKubernetesAPI(errCause), kerr.ErrKubernetesAPI},
		// FeatureNotAvailable wraps NotSupported: the Python client raises
		// NotSupportedError for it (ERROR_CODES.md §1.4).
		{"feature-not-available", kerr.NewFeatureNotAvailable(kerr.FeatureLinfo), kerr.ErrNotSupported},
		{"confirmation-required", kerr.ErrWipeConfirmationRequired, kerr.ErrConfirmationRequired},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.class) {
				t.Errorf("errors.Is(%q, %v) = false", tc.err, tc.class)
			}
			if errors.Is(tc.err, kerr.ErrHTTPConnection) {
				t.Errorf("errors.Is matched an unrelated class sentinel")
			}
		})
	}
}

// TestAsDataBearingTypes checks the errors.As half of the ERROR_CODES.md §0.1
// contract: the same value answers both spellings, and the JSON fields are
// readable from the struct.
func TestAsDataBearingTypes(t *testing.T) {
	t.Run("binary", func(t *testing.T) {
		var e *kerr.BinaryError
		if !errors.As(kerr.NewMachineBinary("frr", "pc1"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Binary != "frr" || e.Machine != "pc1" {
			t.Errorf("fields = %q/%q", e.Binary, e.Machine)
		}
	})

	t.Run("image-arch", func(t *testing.T) {
		var e *kerr.ImageArchError
		if !errors.As(kerr.NewInvalidImageArchitecture("kathara/base", "arm64"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Image != "kathara/base" || e.Arch != "arm64" {
			t.Errorf("fields = %q/%q", e.Image, e.Arch)
		}
	})

	t.Run("non-sequential-iface", func(t *testing.T) {
		var e *kerr.NonSeqInterfaceError
		if !errors.As(kerr.NewNonSequentialMachineInterface(2, "pc1"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Iface != 2 || e.Machine != "pc1" {
			t.Errorf("fields = %d/%q", e.Iface, e.Machine)
		}
	})

	t.Run("mac-address", func(t *testing.T) {
		var e *kerr.MacAddressError
		if !errors.As(kerr.NewInterfaceMacAddress("00:00", 1, "pc1"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.MAC != "00:00" || e.Iface != 1 || e.Machine != "pc1" {
			t.Errorf("fields = %q/%d/%q", e.MAC, e.Iface, e.Machine)
		}
	})

	t.Run("collision-domain-iface-variant", func(t *testing.T) {
		var e *kerr.CollisionDomainError
		if !errors.As(kerr.NewInterfaceAlreadySet("pc1", 3), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Machine != "pc1" || e.Iface != 3 || e.Variant != kerr.CDVariantInterfaceTaken {
			t.Errorf("fields = %q/%d/%d", e.Machine, e.Iface, e.Variant)
		}
		if e.Link != "" {
			t.Errorf("Link = %q, want empty for variant 1", e.Link)
		}
	})

	t.Run("collision-domain-link-variant", func(t *testing.T) {
		var e *kerr.CollisionDomainError
		if !errors.As(kerr.NewManagerMachineNotConnected("pc1", "A"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Machine != "pc1" || e.Link != "A" || e.Variant != kerr.CDVariantManagerNotConnected {
			t.Errorf("fields = %q/%q/%d", e.Machine, e.Link, e.Variant)
		}
	})

	t.Run("option", func(t *testing.T) {
		var e *kerr.OptionError
		if !errors.As(kerr.NewOptionUlimitRange("pc1", "nofile=-2"), &e) {
			t.Fatal("errors.As = false")
		}
		// DIVERGENCES.md 2: the message names the meta, the field names the device.
		if e.Machine != "pc1" || e.Option != "ulimit" {
			t.Errorf("fields = %q/%q", e.Machine, e.Option)
		}
	})

	t.Run("settings-invalid", func(t *testing.T) {
		var e *kerr.SettingsInvalidError
		if !errors.As(kerr.ErrSettingsManagerType, &e) {
			t.Fatal("errors.As = false")
		}
		if e.Reason != "Manager Type not allowed." {
			t.Errorf("Reason = %q", e.Reason)
		}
	})

	t.Run("settings-not-found", func(t *testing.T) {
		var e *kerr.SettingsNotFoundError
		if !errors.As(kerr.NewSettingsNotFound("/root/.config/kathara.conf"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Path != "/root/.config/kathara.conf" {
			t.Errorf("Path = %q", e.Path)
		}
	})

	t.Run("host-arch", func(t *testing.T) {
		var e *kerr.HostArchError
		if !errors.As(kerr.NewHostArchitecture("riscv64"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Arch != "riscv64" {
			t.Errorf("Arch = %q", e.Arch)
		}
	})

	t.Run("feature-not-available", func(t *testing.T) {
		var e *kerr.FeatureNotAvailableError
		if !errors.As(kerr.NewFeatureNotAvailable(kerr.FeatureStatsSampling), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Feature != kerr.FeatureStatsSampling {
			t.Errorf("Feature = %q", e.Feature)
		}
	})
}

// TestAsContextWrappers checks the JSON fields the context wrappers attach
// (ERROR_CODES.md §1.1 "JSON fields" column) and that they leave the message
// of the wrapped error untouched.
func TestAsContextWrappers(t *testing.T) {
	t.Run("machine", func(t *testing.T) {
		var e *kerr.MachineError
		if !errors.As(kerr.NewMachineNotRunning("pc1"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Machine != "pc1" {
			t.Errorf("Machine = %q", e.Machine)
		}
		if e.Error() != "Device `pc1` is not running." {
			t.Errorf("Error() = %q", e.Error())
		}
	})

	t.Run("link", func(t *testing.T) {
		var e *kerr.LinkError
		if !errors.As(kerr.NewLinkAlreadyExists("A"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Link != "A" {
			t.Errorf("Link = %q", e.Link)
		}
	})

	t.Run("image", func(t *testing.T) {
		var e *kerr.ImageError
		if !errors.As(kerr.NewConnectionImagePull("kathara/base"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Image != "kathara/base" {
			t.Errorf("Image = %q", e.Image)
		}
	})

	t.Run("path", func(t *testing.T) {
		var e *kerr.PathError
		if !errors.As(kerr.NewPathNotDirectory("/home/user/vol"), &e) {
			t.Fatal("errors.As = false")
		}
		if e.Path != "/home/user/vol" {
			t.Errorf("Path = %q", e.Path)
		}
	})

	t.Run("op-is-carried", func(t *testing.T) {
		wrapped := kerr.WrapMachine("pc1", "deploy", kerr.ErrHostDriveNotShared)

		var e *kerr.MachineError
		if !errors.As(wrapped, &e) {
			t.Fatal("errors.As = false")
		}
		if e.Op != "deploy" {
			t.Errorf("Op = %q", e.Op)
		}
		if e.Error() != "Host drive is not shared with Docker." {
			t.Errorf("Op leaked into the message: %q", e.Error())
		}
		if !errors.Is(wrapped, kerr.ErrMountDenied) {
			t.Error("wrapping hid the class sentinel")
		}
	})
}

// TestWrapNil keeps the wrappers usable in "return WrapMachine(name, op, err)"
// position, where err is usually nil.
func TestWrapNil(t *testing.T) {
	if err := kerr.WrapMachine("pc1", "deploy", nil); err != nil {
		t.Errorf("WrapMachine = %v, want nil", err)
	}
	if err := kerr.WrapLink("A", "deploy", nil); err != nil {
		t.Errorf("WrapLink = %v, want nil", err)
	}
	if err := kerr.WrapImage("kathara/base", nil); err != nil {
		t.Errorf("WrapImage = %v, want nil", err)
	}
	if err := kerr.WrapPath("/x", nil); err != nil {
		t.Errorf("WrapPath = %v, want nil", err)
	}
}

// TestZeroValueWrappersRender checks that a hand-built wrapper with no inner
// error still renders instead of panicking: nothing on a Python-reachable path
// may panic (PORT_SPEC §10).
func TestZeroValueWrappersRender(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"machine", &kerr.MachineError{Machine: "pc1"}},
		{"link", &kerr.LinkError{Link: "A"}},
		{"image", &kerr.ImageError{Image: "kathara/base"}},
		{"path", &kerr.PathError{Path: "/x"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Error() == "" {
				t.Error("Error() = empty")
			}
			if got := kerr.Code(tc.err); got != kerr.CodeInternalError {
				t.Errorf("Code = %q, want %q", got, kerr.CodeInternalError)
			}
		})
	}
}

// TestStdlibSentinels pins the ERROR_CODES.md §1.2 / §0.2 wrapping of the
// stdlib filesystem errors, including the deliberately inverted FileExists.
func TestStdlibSentinels(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		target error
	}{
		{"settings-not-found", kerr.NewSettingsNotFound("/x"), fs.ErrNotExist},
		{"kathara-not-found", kerr.ErrKatharaNotFound, fs.ErrNotExist},
		{"iptables-not-found", kerr.ErrIptablesNotFound, fs.ErrNotExist},
		{"hosttmp-not-found", kerr.NewHostTmpNotFound("tmp"), fs.ErrNotExist},
		{"path-not-exist", kerr.NewPathNotExist("/x"), fs.ErrNotExist},
		{"volume-permission", kerr.NewVolumePermission("/h", "/g", nil), fs.ErrPermission},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, tc.target) {
				t.Errorf("errors.Is(%q, %v) = false", tc.err, tc.target)
			}
		})
	}

	// FileExists is Python's inverted name; the Go error must never claim the
	// path exists.
	if errors.Is(kerr.NewPathNotExist("/x"), fs.ErrExist) {
		t.Error("FileExists wrapped fs.ErrExist, must be fs.ErrNotExist")
	}
}

// TestCauseStaysReachable checks that a wrapped third-party error survives the
// taxonomy, so backends can inspect it without re-parsing the message.
func TestCauseStaysReachable(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"daemon-connection", kerr.NewDaemonConnection(errCause)},
		{"cannot-open-conf", kerr.NewOSCannotOpenConf("lab.conf", errCause)},
		{"cannot-open-lab-dep", kerr.NewOSCannotOpenLabDep(errCause)},
		{"docker-api", kerr.NewDockerAPI(errCause)},
		{"kubernetes-api", kerr.NewKubernetesAPI(errCause)},
		{"wrap-syntax", kerr.WrapSyntax(errCause, "Invalid device name `PC1`.")},
		{"wrap-value", kerr.WrapValue(errCause, "Invalid `wait` value.")},
		{"wrap-os", kerr.WrapOS(errCause, "Cannot open lab.conf file.")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, errCause) {
				t.Error("cause is not reachable")
			}
		})
	}
}

// TestIsThroughJoinAndWrapping is the PORT_SPEC §4.3 promise that errors.Is
// still matches through errors.Join and through fmt.Errorf.
func TestIsThroughJoinAndWrapping(t *testing.T) {
	binary := kerr.NewMachineBinary("frr", "pc1")
	notRunning := kerr.NewMachineNotRunning("pc2")
	joined := errors.Join(binary, notRunning)

	if !errors.Is(joined, kerr.ErrMachineBinary) || !errors.Is(joined, kerr.ErrMachineNotRunning) {
		t.Error("errors.Is does not traverse the join")
	}

	var bin *kerr.BinaryError
	if !errors.As(joined, &bin) || bin.Binary != "frr" {
		t.Error("errors.As does not traverse the join")
	}

	nested := fmt.Errorf("deploying network scenario: %w", joined)
	if !errors.Is(nested, kerr.ErrMachineNotRunning) {
		t.Error("errors.Is does not traverse fmt.Errorf")
	}

	// BinaryError carries its own fields, so the only MachineError wrapper in
	// the join is the second element.
	var machineErr *kerr.MachineError
	if !errors.As(nested, &machineErr) || machineErr.Machine != "pc2" {
		t.Errorf("errors.As found %v, want the MachineError of pc2", machineErr)
	}
}

// TestJoined returns the elements of a join and refuses to shred the errors of
// this package, whose Unwrap() []error carries a class and a cause.
func TestJoined(t *testing.T) {
	first := kerr.NewMachineBinary("frr", "pc1")
	second := kerr.NewMachineNotRunning("pc2")

	got := kerr.Joined(errors.Join(first, second))
	if !slices.Equal(got, []error{first, second}) {
		t.Errorf("Joined = %v, want the two elements in order", got)
	}

	notJoins := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"plain", errCause},
		{"message-with-cause", kerr.NewDaemonConnection(errCause)},
		{"message-without-cause", kerr.ErrNoDevicesInScenario},
		{"settings-not-found", kerr.NewSettingsNotFound("/x")},
		{"data-bearing", kerr.NewMachineBinary("frr", "pc1")},
	}

	for _, tc := range notJoins {
		t.Run(tc.name, func(t *testing.T) {
			if got := kerr.Joined(tc.err); got != nil {
				t.Errorf("Joined = %v, want nil", got)
			}
		})
	}
}

// TestJoinedDoesNotAliasTheJoin is the regression for the §6.2 workflow: the
// caller sorts the batch into canonical order, and that must not reorder the
// join itself — Code reads the join's first element to pick the primary error
// of §6.3, i.e. the `code` of the JSON envelope.
func TestJoinedDoesNotAliasTheJoin(t *testing.T) {
	primary := kerr.NewMachineBinary("frr", "pc1")
	sibling := kerr.NewMachineNotRunning("pc2")
	joined := errors.Join(primary, sibling)

	batch := kerr.Joined(joined)
	slices.Reverse(batch)

	if got := kerr.Code(joined); got != kerr.CodeMachineBinary {
		t.Errorf("sorting the Joined result changed Code(join) to %q, want %q", got, kerr.CodeMachineBinary)
	}
	if again := kerr.Joined(joined); !slices.Equal(again, []error{primary, sibling}) {
		t.Errorf("sorting the Joined result reordered the join: %v", again)
	}
}

// TestOwnMultiUnwrapTypesAreMarked is the invariant Joined depends on: a type
// of this package that carries its class through Unwrap() []error must be
// marked so Joined refuses it. Were one to slip through, the ERROR_CODES.md
// §6.5 `errors` array would gain a phantom entry — the class sentinel, whose
// text is internal identity, rendered as a sibling error. Go cannot enumerate a
// package's types, so this walks every value the package can hand out: the
// whole §2 catalog plus one instance of every exported struct type.
func TestOwnMultiUnwrapTypesAreMarked(t *testing.T) {
	values := []error{
		&kerr.MachineError{Machine: "pc1", Op: "deploy", Err: errCause},
		&kerr.LinkError{Link: "A", Op: "deploy", Err: errCause},
		&kerr.ImageError{Image: "kathara/base", Err: errCause},
		&kerr.PathError{Path: "/x", Err: errCause},
		&kerr.BinaryError{Binary: "frr", Machine: "pc1"},
		&kerr.ImageArchError{Image: "kathara/base", Arch: "arm64"},
		&kerr.NonSeqInterfaceError{Iface: 1, Machine: "pc1"},
		&kerr.MacAddressError{MAC: "zz", Iface: 0, Machine: "pc1"},
		&kerr.CollisionDomainError{Machine: "pc1", Link: "A", Variant: kerr.CDVariantNotConnected},
		&kerr.OptionError{Machine: "pc1", Option: "cpus", Message: "x"},
		&kerr.MachineSetError{Machines: []string{"pc1"}},
		&kerr.SettingsInvalidError{Reason: "Not a valid JSON."},
		&kerr.SettingsNotFoundError{Path: "/x"},
		&kerr.HostArchError{Arch: "riscv64"},
		&kerr.FeatureNotAvailableError{Feature: kerr.FeatureLinfo},
	}
	for _, entry := range catalog {
		values = append(values, entry.err)
	}

	multi := 0
	for _, err := range values {
		if _, ok := err.(interface{ Unwrap() []error }); !ok {
			continue
		}
		multi++
		if got := kerr.Joined(err); got != nil {
			t.Errorf("%T carries its class in Unwrap() []error but Joined returned %v; "+
				"the type must be marked as not-a-join in kerrors", err, got)
		}
	}

	// Guard against the check going vacuous: the message type and
	// SettingsNotFoundError both use the multi-unwrap shape today.
	if multi < 2 {
		t.Errorf("only %d values implement Unwrap() []error; the test is not exercising the invariant", multi)
	}
}

// TestJoinedTreatsMultiWrapAsBatch pins the one shape that is not errors.Join
// yet reaches Joined as a batch: since Go 1.20 an fmt.Errorf with more than one
// %w also implements Unwrap() []error, and the stdlib makes the two
// indistinguishable. The port builds batches only with errors.Join
// (ERROR_CODES.md §6.2); this test records what a multi-%w would do — the
// formatted text is dropped and the elements become the §6.5 `errors` array —
// so the CLI layer cannot trip on it unknowingly.
func TestJoinedTreatsMultiWrapAsBatch(t *testing.T) {
	first := kerr.NewMachineBinary("frr", "pc1")
	second := kerr.NewMachineNotRunning("pc2")

	got := kerr.Joined(fmt.Errorf("deploying network scenario: %w; %w", first, second))
	if !slices.Equal(got, []error{first, second}) {
		t.Errorf("Joined(multi-%%w) = %v, want the two wrapped errors", got)
	}

	// A single %w keeps the classic Unwrap() error shape and is not a batch.
	if got := kerr.Joined(fmt.Errorf("deploying network scenario: %w", first)); got != nil {
		t.Errorf("Joined(single-%%w) = %v, want nil", got)
	}
}

// TestGenericBuilders covers the escape hatches the later phases use for sites
// that render their own text: the message is kept verbatim and the class is
// the one asked for.
func TestGenericBuilders(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
		msg  string
	}{
		{"settings-invalid", kerr.NewSettingsInvalid("Custom check failed."), kerr.CodeSettings,
			"Settings file is not valid: Custom check failed. Fix it or delete it before launching."},
		{"syntax", kerr.NewSyntax("In lab.conf - Line 3: `pc1[0]`."), kerr.CodeSyntax,
			"In lab.conf - Line 3: `pc1[0]`."},
		{"value", kerr.NewValue("Invalid `wait` value."), kerr.CodeValue, "Invalid `wait` value."},
		{"os", kerr.NewOS("lab.conf file is empty."), kerr.CodeOS, "lab.conf file is empty."},
		{"new", kerr.New(kerr.ErrPrivilege, "You must be root."), kerr.CodePrivilege, "You must be root."},
		{"wrap", kerr.Wrap(kerr.ErrConnection, errCause, "Cannot read Kubernetes configuration."),
			kerr.CodeConnection, "Cannot read Kubernetes configuration."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.msg {
				t.Errorf("message:\n got %q\nwant %q", got, tc.msg)
			}
			if got := kerr.Code(tc.err); got != tc.code {
				t.Errorf("code: got %q, want %q", got, tc.code)
			}
		})
	}
}

// TestSentinelTextIsIdentityOnly pins the ERROR_CODES.md §0.3 rule: a sentinel
// renders an internal identity string, never a Python message. Anything user
// visible starts with a capital letter and ends in punctuation; these do not.
func TestSentinelTextIsIdentityOnly(t *testing.T) {
	sentinels := []error{
		kerr.ErrInvocation, kerr.ErrSettings, kerr.ErrDaemonConnection, kerr.ErrMachineNotFound,
		kerr.ErrLinkNotFound, kerr.ErrDependencyLoop, kerr.ErrSyntax, kerr.ErrDockerAPI,
		kerr.ErrConfirmationRequired,
	}

	for _, sentinel := range sentinels {
		text := sentinel.Error()
		if text == "" {
			t.Errorf("sentinel %v has no identity text", sentinel)
		}
		if !strings.HasPrefix(text, "kathara: ") {
			t.Errorf("sentinel text %q does not start with the package prefix", text)
		}
		if strings.HasSuffix(text, ".") {
			t.Errorf("sentinel text %q reads like a user message", text)
		}
	}
}

// TestUnwrapReachesClassSentinel documents the shape ERROR_CODES.md §0.1
// freezes: a data-bearing type unwraps to its class sentinel.
func TestUnwrapReachesClassSentinel(t *testing.T) {
	if got := errors.Unwrap(kerr.NewMachineBinary("frr", "pc1")); got != kerr.ErrMachineBinary {
		t.Errorf("Unwrap = %v, want ErrMachineBinary", got)
	}
	if got := errors.Unwrap(kerr.NewHostArchitecture("riscv64")); got != kerr.ErrHostArchitecture {
		t.Errorf("Unwrap = %v, want ErrHostArchitecture", got)
	}
	if got := errors.Unwrap(kerr.NewFeatureNotAvailable(kerr.FeatureLinfo)); got != kerr.ErrNotSupported {
		t.Errorf("Unwrap = %v, want ErrNotSupported", got)
	}
}
