package kerrors

// The stable JSON codes of docs/port/ERROR_CODES.md §1. They are the public
// contract of the JSON CLI (JSON_CLI_CONTRACT.md §5.2): 33 codes for the Python
// exception classes (1:1 and exhaustive, spec §4.3), 8 for the user-reachable
// builtin exceptions, 2 third-party passthroughs and 3 port-new codes.
//
// Six codes are RESERVED: they are never emitted by 1.0 but keep the class →
// code mapping 1:1 and forward-compatible.
const (
	// §1.1 — Kathará exception classes (exceptions.py).

	// CodeClassNotFound is RESERVED: cobra reports unknown commands (§7).
	CodeClassNotFound = "ClassNotFound"
	// CodeHTTPConnection is RESERVED: the only raisers live in webhooks/,
	// which is deferred, and both 3.8.3 consumers swallow the exception.
	CodeHTTPConnection = "HTTPConnection"
	// CodeInstantiation is RESERVED: the Go port has no singletons.
	CodeInstantiation             = "Instantiation"
	CodeInvocation                = "Invocation"
	CodeSettings                  = "Settings"
	CodeSettingsNotFound          = "SettingsNotFound"
	CodeDockerDaemonConnection    = "DockerDaemonConnection"
	CodeNotSupported              = "NotSupported"
	CodePrivilege                 = "Privilege"
	CodeHostArchitecture          = "HostArchitecture"
	CodeLabAlreadyExists          = "LabAlreadyExists"
	CodeLabNotFound               = "LabNotFound"
	CodeEmptyLab                  = "EmptyLab"
	CodeMachineDependency         = "MachineDependency"
	CodeMountDenied               = "MountDenied"
	CodeMachineAlreadyExists      = "MachineAlreadyExists"
	CodeNonSequentialMachineIface = "NonSequentialMachineInterface"
	CodeMachineOption             = "MachineOption"
	CodeMachineCollisionDomain    = "MachineCollisionDomain"
	CodeMachineNotFound           = "MachineNotFound"
	CodeMachineNotRunning         = "MachineNotRunning"
	CodeMachineNotReady           = "MachineNotReady"
	CodeMachineBinary             = "MachineBinary"
	CodeInterfaceMacAddress       = "InterfaceMacAddress"
	CodeLinkNotFound              = "LinkNotFound"
	CodeLinkAlreadyExists         = "LinkAlreadyExists"
	// CodeInterfaceNotFound is RESERVED: the sole raiser, os/Networking.py:38,
	// is deferred (only get_iptables_version is carved into backend/docker).
	CodeInterfaceNotFound = "InterfaceNotFound"
	// CodeTest is RESERVED-DEAD: TestError is never instantiated in 3.8.3.
	CodeTest = "Test"
	// CodeMachineSignatureNotFound is RESERVED-DEAD: never raised in 3.8.3.
	CodeMachineSignatureNotFound = "MachineSignatureNotFound"
	CodeInvalidImageArchitecture = "InvalidImageArchitecture"
	CodeDockerImageNotFound      = "DockerImageNotFound"
	CodeDockerPlugin             = "DockerPlugin"
	CodeKubernetesConfigMap      = "KubernetesConfigMap"

	// §1.2 — builtin exceptions that reach users, mapped by call-site semantics.

	CodeSyntax        = "Syntax"
	CodeValue         = "Value"
	CodeOS            = "OS"
	CodeFileNotFound  = "FileNotFound"
	CodeFileExists    = "FileExists"
	CodeNotADirectory = "NotADirectory"
	CodePermission    = "Permission"
	CodeConnection    = "Connection"

	// §1.3 — third-party passthrough (Python re-raises these untranslated).

	CodeDockerAPI     = "DockerAPI"
	CodeKubernetesAPI = "KubernetesAPI"

	// §1.4 — port-new codes.

	CodeFeatureNotAvailable  = "FeatureNotAvailable"
	CodeInternalError        = "InternalError"
	CodeConfirmationRequired = "ConfirmationRequired"
)

// AllCodes lists every code of the registry, in ERROR_CODES.md §1 order.
// RESERVED codes are included: the registry is the contract, not the emitted
// subset.
var AllCodes = []string{
	CodeClassNotFound,
	CodeHTTPConnection,
	CodeInstantiation,
	CodeInvocation,
	CodeSettings,
	CodeSettingsNotFound,
	CodeDockerDaemonConnection,
	CodeNotSupported,
	CodePrivilege,
	CodeInterfaceNotFound,
	CodeHostArchitecture,
	CodeLabAlreadyExists,
	CodeLabNotFound,
	CodeEmptyLab,
	CodeMachineDependency,
	CodeMountDenied,
	CodeMachineAlreadyExists,
	CodeNonSequentialMachineIface,
	CodeMachineOption,
	CodeMachineCollisionDomain,
	CodeMachineNotFound,
	CodeMachineNotRunning,
	CodeMachineNotReady,
	CodeMachineBinary,
	CodeInterfaceMacAddress,
	CodeLinkNotFound,
	CodeLinkAlreadyExists,
	CodeTest,
	CodeMachineSignatureNotFound,
	CodeInvalidImageArchitecture,
	CodeDockerImageNotFound,
	CodeDockerPlugin,
	CodeKubernetesConfigMap,
	CodeSyntax,
	CodeValue,
	CodeOS,
	CodeFileNotFound,
	CodeFileExists,
	CodeNotADirectory,
	CodePermission,
	CodeConnection,
	CodeDockerAPI,
	CodeKubernetesAPI,
	CodeFeatureNotAvailable,
	CodeInternalError,
	CodeConfirmationRequired,
}

// Coder is implemented by errors that name their own JSON code.
//
// Every sentinel of this package implements it, so errors built here are
// classified by walking the Unwrap chain. Types declared outside the package
// implement it to join the registry without kerrors importing them — that is
// how labfile.ParseError reports Syntax and Value (ERROR_CODES.md §0.3).
//
// The method is not called Code because labfile.ParseError already has a Code
// field.
type Coder interface {
	ErrorCode() string
}

// Code reports the stable JSON code of err (ERROR_CODES.md §1).
//
// It walks the error tree depth-first, left to right, and returns the code of
// the first node that carries one; anything unmapped reaching the CLI boundary
// is CodeInternalError, the exhaustive fallback of §1.4. Code(nil) is "".
//
// Joined errors (errors.Join, ERROR_CODES.md §6) therefore report the code of
// their first coded element, which for a canonically ordered batch is the
// primary error of §6.3. Errors of this package that carry both a class and an
// underlying cause always list the class first, so a cause never shadows its
// class.
func Code(err error) string {
	if err == nil {
		return ""
	}
	if code, ok := codeOf(err); ok {
		return code
	}
	return CodeInternalError
}

// codeOf is Code without the InternalError fallback, so that recursion over the
// branches of a join can tell "no code here" from "coded as InternalError".
func codeOf(err error) (string, bool) {
	for err != nil {
		if c, ok := err.(Coder); ok {
			if code := c.ErrorCode(); code != "" {
				return code, true
			}
		}

		switch x := err.(type) {
		case interface{ Unwrap() error }:
			err = x.Unwrap()
		case interface{ Unwrap() []error }:
			for _, branch := range x.Unwrap() {
				if code, ok := codeOf(branch); ok {
					return code, true
				}
			}
			return "", false
		default:
			return "", false
		}
	}

	return "", false
}

// humanLabels holds the codes whose human label is not code + "Error"
// (ERROR_CODES.md §0.1, §1.3, §1.4).
var humanLabels = map[string]string{
	// Python re-raises the SDK exceptions untranslated, so the user sees the
	// third-party class names (§1.3).
	CodeDockerAPI:     "APIError",
	CodeKubernetesAPI: "ApiException",
	// Port-new codes: no Python class exists, so §1.4 spells the label out
	// per row and none of the three takes the Error suffix.
	CodeFeatureNotAvailable:  "FeatureNotAvailable",
	CodeInternalError:        "InternalError",
	CodeConfirmationRequired: "ConfirmationRequired",
}

// HumanLabel reports the label human mode prints for code, i.e. the {label} of
// `CRITICAL ({label}) {message}` (ERROR_CODES.md §0.1).
//
// For the Kathará classes and the builtins it is the Python class name, which
// is code + "Error" (MachineNotFound → MachineNotFoundError, OS → OSError); the
// exceptions are listed in humanLabels. HumanLabel("") is "".
//
// FeatureNotAvailable is one of those exceptions: it renders as
// FeatureNotAvailable, per the ERROR_CODES.md §1.4 row. JSON_CLI_CONTRACT.md
// §5.6 says FeatureNotAvailableError in passing; that document's scope line
// hands human-mode rendering to ERROR_CODES.md and cites it as normative, and
// no Python class name can break the tie because Python 3.8.3 never emits this
// code. See RULINGS.md (FeatureNotAvailable human label).
func HumanLabel(code string) string {
	if code == "" {
		return ""
	}
	if label, ok := humanLabels[code]; ok {
		return label
	}
	return code + "Error"
}
