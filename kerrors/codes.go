package kerrors

const (
	CodeClassNotFound = "ClassNotFound"
	// CodeHTTPConnection is RESERVED: the only raisers live in webhooks/,
	// which is deferred, and both 3.8.3 consumers swallow the exception.
	CodeHTTPConnection = "HTTPConnection"
	// CodeInstantiation is RESERVED: the Go implementation has no singletons.
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

	CodeSyntax        = "Syntax"
	CodeValue         = "Value"
	CodeOS            = "OS"
	CodeFileNotFound  = "FileNotFound"
	CodeFileExists    = "FileExists"
	CodeNotADirectory = "NotADirectory"
	CodePermission    = "Permission"
	CodeConnection    = "Connection"

	CodeDockerAPI     = "DockerAPI"
	CodeKubernetesAPI = "KubernetesAPI"

	CodeFeatureNotAvailable  = "FeatureNotAvailable"
	CodeInternalError        = "InternalError"
	CodeConfirmationRequired = "ConfirmationRequired"
)

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
type Coder interface {
	ErrorCode() string
}

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

var humanLabels = map[string]string{
	CodeDockerAPI:            "APIError",
	CodeKubernetesAPI:        "ApiException",
	CodeFeatureNotAvailable:  "FeatureNotAvailable",
	CodeInternalError:        "InternalError",
	CodeConfirmationRequired: "ConfirmationRequired",
}

// HumanLabel reports the label human mode prints for code, i.e.
func HumanLabel(code string) string {
	if code == "" {
		return ""
	}
	if label, ok := humanLabels[code]; ok {
		return label
	}
	return code + "Error"
}
