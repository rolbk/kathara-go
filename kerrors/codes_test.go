package kerrors_test

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	kerr "github.com/KatharaFramework/kathara-go/kerrors"
)

func TestRegistrySize(t *testing.T) {
	if len(kerr.AllCodes) != 46 {
		t.Errorf("len(AllCodes) = %d, want 46", len(kerr.AllCodes))
	}

	seen := make(map[string]bool, len(kerr.AllCodes))
	for _, code := range kerr.AllCodes {
		if code == "" {
			t.Error("empty code in the registry")
		}
		if seen[code] {
			t.Errorf("duplicate code %q", code)
		}
		seen[code] = true
	}
}

// TestSentinelCodes checks that every class sentinel reports the code of its
// registry row: the JSON envelope derives `code` from the sentinel.
func TestSentinelCodes(t *testing.T) {
	cases := []struct {
		sentinel error
		code     string
	}{
		{kerr.ErrHTTPConnection, kerr.CodeHTTPConnection},
		{kerr.ErrInvocation, kerr.CodeInvocation},
		{kerr.ErrSettings, kerr.CodeSettings},
		{kerr.ErrSettingsNotFound, kerr.CodeSettingsNotFound},
		{kerr.ErrDaemonConnection, kerr.CodeDockerDaemonConnection},
		{kerr.ErrNotSupported, kerr.CodeNotSupported},
		{kerr.ErrPrivilege, kerr.CodePrivilege},
		{kerr.ErrInterfaceNotFound, kerr.CodeInterfaceNotFound},
		{kerr.ErrHostArchitecture, kerr.CodeHostArchitecture},
		{kerr.ErrLabAlreadyExists, kerr.CodeLabAlreadyExists},
		{kerr.ErrLabNotFound, kerr.CodeLabNotFound},
		{kerr.ErrEmptyLab, kerr.CodeEmptyLab},
		{kerr.ErrDependencyLoop, kerr.CodeMachineDependency},
		{kerr.ErrMountDenied, kerr.CodeMountDenied},
		{kerr.ErrMachineAlreadyExists, kerr.CodeMachineAlreadyExists},
		{kerr.ErrNonSequentialMachineInterface, kerr.CodeNonSequentialMachineIface},
		{kerr.ErrMachineOption, kerr.CodeMachineOption},
		{kerr.ErrMachineCollisionDomain, kerr.CodeMachineCollisionDomain},
		{kerr.ErrMachineNotFound, kerr.CodeMachineNotFound},
		{kerr.ErrMachineNotRunning, kerr.CodeMachineNotRunning},
		{kerr.ErrMachineNotReady, kerr.CodeMachineNotReady},
		{kerr.ErrMachineBinary, kerr.CodeMachineBinary},
		{kerr.ErrInterfaceMacAddress, kerr.CodeInterfaceMacAddress},
		{kerr.ErrLinkNotFound, kerr.CodeLinkNotFound},
		{kerr.ErrLinkAlreadyExists, kerr.CodeLinkAlreadyExists},
		{kerr.ErrInvalidImageArchitecture, kerr.CodeInvalidImageArchitecture},
		{kerr.ErrDockerImageNotFound, kerr.CodeDockerImageNotFound},
		{kerr.ErrDockerPlugin, kerr.CodeDockerPlugin},
		{kerr.ErrKubernetesConfigMap, kerr.CodeKubernetesConfigMap},
		{kerr.ErrSyntax, kerr.CodeSyntax},
		{kerr.ErrValue, kerr.CodeValue},
		{kerr.ErrOS, kerr.CodeOS},
		{kerr.ErrFileNotFound, kerr.CodeFileNotFound},
		{kerr.ErrFileExists, kerr.CodeFileExists},
		{kerr.ErrNotADirectory, kerr.CodeNotADirectory},
		{kerr.ErrPermission, kerr.CodePermission},
		{kerr.ErrConnection, kerr.CodeConnection},
		{kerr.ErrDockerAPI, kerr.CodeDockerAPI},
		{kerr.ErrKubernetesAPI, kerr.CodeKubernetesAPI},
		{kerr.ErrConfirmationRequired, kerr.CodeConfirmationRequired},
	}

	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			if got := kerr.Code(tc.sentinel); got != tc.code {
				t.Errorf("Code = %q, want %q", got, tc.code)
			}
		})
	}

	// The sentinels are distinct values: no two classes share one.
	seen := make(map[error]bool, len(cases))
	for _, tc := range cases {
		if seen[tc.sentinel] {
			t.Errorf("sentinel reused for %q", tc.code)
		}
		seen[tc.sentinel] = true
	}
}

// TestHumanLabels pins the human-mode label of every code: the Python class
// name, which human mode prints as CRITICAL ({label}) {message}.
func TestHumanLabels(t *testing.T) {
	want := map[string]string{
		kerr.CodeClassNotFound:             "ClassNotFoundError",
		kerr.CodeHTTPConnection:            "HTTPConnectionError",
		kerr.CodeInstantiation:             "InstantiationError",
		kerr.CodeInvocation:                "InvocationError",
		kerr.CodeSettings:                  "SettingsError",
		kerr.CodeSettingsNotFound:          "SettingsNotFoundError",
		kerr.CodeDockerDaemonConnection:    "DockerDaemonConnectionError",
		kerr.CodeNotSupported:              "NotSupportedError",
		kerr.CodePrivilege:                 "PrivilegeError",
		kerr.CodeInterfaceNotFound:         "InterfaceNotFoundError",
		kerr.CodeHostArchitecture:          "HostArchitectureError",
		kerr.CodeLabAlreadyExists:          "LabAlreadyExistsError",
		kerr.CodeLabNotFound:               "LabNotFoundError",
		kerr.CodeEmptyLab:                  "EmptyLabError",
		kerr.CodeMachineDependency:         "MachineDependencyError",
		kerr.CodeMountDenied:               "MountDeniedError",
		kerr.CodeMachineAlreadyExists:      "MachineAlreadyExistsError",
		kerr.CodeNonSequentialMachineIface: "NonSequentialMachineInterfaceError",
		kerr.CodeMachineOption:             "MachineOptionError",
		kerr.CodeMachineCollisionDomain:    "MachineCollisionDomainError",
		kerr.CodeMachineNotFound:           "MachineNotFoundError",
		kerr.CodeMachineNotRunning:         "MachineNotRunningError",
		kerr.CodeMachineNotReady:           "MachineNotReadyError",
		kerr.CodeMachineBinary:             "MachineBinaryError",
		kerr.CodeInterfaceMacAddress:       "InterfaceMacAddressError",
		kerr.CodeLinkNotFound:              "LinkNotFoundError",
		kerr.CodeLinkAlreadyExists:         "LinkAlreadyExistsError",
		kerr.CodeTest:                      "TestError",
		kerr.CodeMachineSignatureNotFound:  "MachineSignatureNotFoundError",
		kerr.CodeInvalidImageArchitecture:  "InvalidImageArchitectureError",
		kerr.CodeDockerImageNotFound:       "DockerImageNotFoundError",
		kerr.CodeDockerPlugin:              "DockerPluginError",
		kerr.CodeKubernetesConfigMap:       "KubernetesConfigMapError",
		kerr.CodeSyntax:                    "SyntaxError",
		kerr.CodeValue:                     "ValueError",

		kerr.CodeOS:            "OSError",
		kerr.CodeFileNotFound:  "FileNotFoundError",
		kerr.CodeFileExists:    "FileExistsError",
		kerr.CodeNotADirectory: "NotADirectoryError",
		kerr.CodePermission:    "PermissionError",
		kerr.CodeConnection:    "ConnectionError",
		kerr.CodeDockerAPI:     "APIError",
		kerr.CodeKubernetesAPI: "ApiException",

		kerr.CodeFeatureNotAvailable:  "FeatureNotAvailable",
		kerr.CodeInternalError:        "InternalError",
		kerr.CodeConfirmationRequired: "ConfirmationRequired",
	}

	for _, code := range kerr.AllCodes {
		t.Run(code, func(t *testing.T) {
			expected, ok := want[code]
			if !ok {
				t.Fatalf("no expected human label for %q", code)
			}
			if got := kerr.HumanLabel(code); got != expected {
				t.Errorf("HumanLabel = %q, want %q", got, expected)
			}
		})
	}

	// The other direction: a code constant added to the registry table above but
	// left out of AllCodes would otherwise pass unnoticed.
	if len(want) != len(kerr.AllCodes) {
		t.Errorf("labelled %d codes, AllCodes has %d", len(want), len(kerr.AllCodes))
	}
	for code := range want {
		if !slices.Contains(kerr.AllCodes, code) {
			t.Errorf("code %q is labelled but missing from AllCodes", code)
		}
	}

	if got := kerr.HumanLabel(""); got != "" {
		t.Errorf(`HumanLabel("") = %q, want ""`, got)
	}
}

func TestHumanLabelsAreInjective(t *testing.T) {
	byLabel := make(map[string]string, len(kerr.AllCodes))
	for _, code := range kerr.AllCodes {
		label := kerr.HumanLabel(code)
		if previous, ok := byLabel[label]; ok {
			t.Errorf("human label %q is shared by codes %q and %q", label, previous, code)
		}
		byLabel[label] = code
	}
}

func TestCodeFallback(t *testing.T) {
	if got := kerr.Code(nil); got != "" {
		t.Errorf("Code(nil) = %q, want empty", got)
	}
	if got := kerr.Code(io.EOF); got != kerr.CodeInternalError {
		t.Errorf("Code(io.EOF) = %q, want %q", got, kerr.CodeInternalError)
	}
	if got := kerr.Code(fmt.Errorf("reading lab.conf: %w", io.EOF)); got != kerr.CodeInternalError {
		t.Errorf("Code(wrapped io.EOF) = %q, want %q", got, kerr.CodeInternalError)
	}
}

// TestCodeThroughWrapping checks that context added by callers does not hide
// the code.
func TestCodeThroughWrapping(t *testing.T) {
	err := fmt.Errorf("deploying device: %w", kerr.NewMachineBinary("frr", "pc1"))
	if got := kerr.Code(err); got != kerr.CodeMachineBinary {
		t.Errorf("Code = %q, want %q", got, kerr.CodeMachineBinary)
	}

	nested := fmt.Errorf("lstart: %w", err)
	if got := kerr.Code(nested); got != kerr.CodeMachineBinary {
		t.Errorf("Code (twice wrapped) = %q, want %q", got, kerr.CodeMachineBinary)
	}
}

func TestCodeThroughJoin(t *testing.T) {
	primary := kerr.NewMachineBinary("frr", "pc1")
	sibling := kerr.NewDockerAPI(errCause)

	if got := kerr.Code(errors.Join(primary, sibling)); got != kerr.CodeMachineBinary {
		t.Errorf("Code = %q, want %q", got, kerr.CodeMachineBinary)
	}
	if got := kerr.Code(errors.Join(sibling, primary)); got != kerr.CodeDockerAPI {
		t.Errorf("Code (reversed) = %q, want %q", got, kerr.CodeDockerAPI)
	}
	// An element with no code of its own does not stop the walk.
	if got := kerr.Code(errors.Join(io.EOF, primary)); got != kerr.CodeMachineBinary {
		t.Errorf("Code (uncoded first) = %q, want %q", got, kerr.CodeMachineBinary)
	}
}

// TestCodeDoesNotSeeCauseFirst checks that a wrapped third-party error never
// shadows the class of the taxonomy error carrying it.
func TestCodeDoesNotSeeCauseFirst(t *testing.T) {
	inner := kerr.NewDockerAPI(errCause)
	err := kerr.NewOSCannotOpenConf("lab.conf", inner)

	if got := kerr.Code(err); got != kerr.CodeOS {
		t.Errorf("Code = %q, want %q", got, kerr.CodeOS)
	}
	if !errors.Is(err, kerr.ErrDockerAPI) {
		t.Error("the cause became unreachable")
	}
}

type parseError struct {
	File string
	Line int
	Msg  string
	Code string
}

func (e *parseError) Error() string { return e.Msg }

func (e *parseError) ErrorCode() string { return e.Code }

func (e *parseError) Unwrap() error {
	if e.Code == kerr.CodeValue {
		return kerr.ErrValue
	}
	return kerr.ErrSyntax
}

// TestCoderHook checks that an out-of-package type carries its own code and
// still answers errors.Is for the class sentinel it unwraps to.
func TestCoderHook(t *testing.T) {
	syntax := &parseError{File: "lab.conf", Line: 3, Msg: "In lab.conf - Line 3: `pc1[0]`.", Code: kerr.CodeSyntax}
	if got := kerr.Code(syntax); got != kerr.CodeSyntax {
		t.Errorf("Code = %q, want %q", got, kerr.CodeSyntax)
	}
	if !errors.Is(syntax, kerr.ErrSyntax) {
		t.Error("errors.Is(ErrSyntax) = false")
	}

	value := &parseError{
		File: "lab.conf",
		Line: 1,
		Msg:  "In lab.conf - Line 1: `shared` is a reserved name, you can not use it for a device.",
		Code: kerr.CodeValue,
	}
	if got := kerr.Code(value); got != kerr.CodeValue {
		t.Errorf("Code = %q, want %q", got, kerr.CodeValue)
	}
	if got := kerr.Code(fmt.Errorf("parsing: %w", value)); got != kerr.CodeValue {
		t.Errorf("Code (wrapped) = %q, want %q", got, kerr.CodeValue)
	}

	// An empty code falls through to the class sentinel of the Unwrap chain.
	fallthroughErr := &parseError{Msg: "boom"}
	if got := kerr.Code(fallthroughErr); got != kerr.CodeSyntax {
		t.Errorf("Code (empty ErrorCode) = %q, want %q", got, kerr.CodeSyntax)
	}
}

// TestCoderPrecedence checks that a type reporting its own code wins over the
// class it wraps: FeatureNotAvailable wraps NotSupported but must not be
// rendered as NotSupported.
func TestCoderPrecedence(t *testing.T) {
	err := kerr.NewFeatureNotAvailable(kerr.FeatureLabExt)

	if got := kerr.Code(err); got != kerr.CodeFeatureNotAvailable {
		t.Errorf("Code = %q, want %q", got, kerr.CodeFeatureNotAvailable)
	}
	if !errors.Is(err, kerr.ErrNotSupported) {
		t.Error("errors.Is(ErrNotSupported) = false")
	}
}
