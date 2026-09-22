// What is *not* re-exported: the eighty-odd `New…`/`Wrap…` constructors. They
// are for the code that raises, which means the backends and `cmd/kathara`,
// and those import `kerrors` directly — it is a public package, not an
// internal one. Callers of this package inspect errors; they do not build
// them.

package kathara

import "github.com/KatharaFramework/kathara-go/kerrors"

const (
	CodeClassNotFound             = kerrors.CodeClassNotFound
	CodeConfirmationRequired      = kerrors.CodeConfirmationRequired
	CodeConnection                = kerrors.CodeConnection
	CodeDockerAPI                 = kerrors.CodeDockerAPI
	CodeDockerDaemonConnection    = kerrors.CodeDockerDaemonConnection
	CodeDockerImageNotFound       = kerrors.CodeDockerImageNotFound
	CodeDockerPlugin              = kerrors.CodeDockerPlugin
	CodeEmptyLab                  = kerrors.CodeEmptyLab
	CodeFeatureNotAvailable       = kerrors.CodeFeatureNotAvailable
	CodeFileExists                = kerrors.CodeFileExists
	CodeFileNotFound              = kerrors.CodeFileNotFound
	CodeHTTPConnection            = kerrors.CodeHTTPConnection
	CodeHostArchitecture          = kerrors.CodeHostArchitecture
	CodeInstantiation             = kerrors.CodeInstantiation
	CodeInterfaceMacAddress       = kerrors.CodeInterfaceMacAddress
	CodeInterfaceNotFound         = kerrors.CodeInterfaceNotFound
	CodeInternalError             = kerrors.CodeInternalError
	CodeInvalidImageArchitecture  = kerrors.CodeInvalidImageArchitecture
	CodeInvocation                = kerrors.CodeInvocation
	CodeKubernetesAPI             = kerrors.CodeKubernetesAPI
	CodeKubernetesConfigMap       = kerrors.CodeKubernetesConfigMap
	CodeLabAlreadyExists          = kerrors.CodeLabAlreadyExists
	CodeLabNotFound               = kerrors.CodeLabNotFound
	CodeLinkAlreadyExists         = kerrors.CodeLinkAlreadyExists
	CodeLinkNotFound              = kerrors.CodeLinkNotFound
	CodeMachineAlreadyExists      = kerrors.CodeMachineAlreadyExists
	CodeMachineBinary             = kerrors.CodeMachineBinary
	CodeMachineCollisionDomain    = kerrors.CodeMachineCollisionDomain
	CodeMachineDependency         = kerrors.CodeMachineDependency
	CodeMachineNotFound           = kerrors.CodeMachineNotFound
	CodeMachineNotReady           = kerrors.CodeMachineNotReady
	CodeMachineNotRunning         = kerrors.CodeMachineNotRunning
	CodeMachineOption             = kerrors.CodeMachineOption
	CodeMachineSignatureNotFound  = kerrors.CodeMachineSignatureNotFound
	CodeMountDenied               = kerrors.CodeMountDenied
	CodeNonSequentialMachineIface = kerrors.CodeNonSequentialMachineIface
	CodeNotADirectory             = kerrors.CodeNotADirectory
	CodeNotSupported              = kerrors.CodeNotSupported
	CodeOS                        = kerrors.CodeOS
	CodePermission                = kerrors.CodePermission
	CodePrivilege                 = kerrors.CodePrivilege
	CodeSettings                  = kerrors.CodeSettings
	CodeSettingsNotFound          = kerrors.CodeSettingsNotFound
	CodeSyntax                    = kerrors.CodeSyntax
	CodeTest                      = kerrors.CodeTest
	CodeValue                     = kerrors.CodeValue
)

const (
	FeatureLabExt        = kerrors.FeatureLabExt
	FeatureLinfo         = kerrors.FeatureLinfo
	FeatureStatsSampling = kerrors.FeatureStatsSampling
	FeatureWebhooks      = kerrors.FeatureWebhooks
)

// The five shapes of `MachineCollisionDomainError`, distinguished by
// [CollisionDomainError.Variant] because Python raises the same class with five
// different message templates.
const (
	CDVariantAlreadyConnected        = kerrors.CDVariantAlreadyConnected
	CDVariantInterfaceTaken          = kerrors.CDVariantInterfaceTaken
	CDVariantManagerAlreadyConnected = kerrors.CDVariantManagerAlreadyConnected
	CDVariantManagerNotConnected     = kerrors.CDVariantManagerNotConnected
	CDVariantNotConnected            = kerrors.CDVariantNotConnected
)

var (
	ErrConfirmationRequired          = kerrors.ErrConfirmationRequired
	ErrConnection                    = kerrors.ErrConnection
	ErrDaemonConnection              = kerrors.ErrDaemonConnection
	ErrDependencyLoop                = kerrors.ErrDependencyLoop
	ErrDeviceNameOrObject            = kerrors.ErrDeviceNameOrObject
	ErrDockerAPI                     = kerrors.ErrDockerAPI
	ErrDockerImageNotFound           = kerrors.ErrDockerImageNotFound
	ErrDockerPlugin                  = kerrors.ErrDockerPlugin
	ErrEmptyLab                      = kerrors.ErrEmptyLab
	ErrFileExists                    = kerrors.ErrFileExists
	ErrFileNotFound                  = kerrors.ErrFileNotFound
	ErrHTTPConnection                = kerrors.ErrHTTPConnection
	ErrHostArchitecture              = kerrors.ErrHostArchitecture
	ErrHostDriveNotShared            = kerrors.ErrHostDriveNotShared
	ErrInconsistentState             = kerrors.ErrInconsistentState
	ErrInterfaceMacAddress           = kerrors.ErrInterfaceMacAddress
	ErrInterfaceNotFound             = kerrors.ErrInterfaceNotFound
	ErrInvalidImageArchitecture      = kerrors.ErrInvalidImageArchitecture
	ErrInvalidWaitValue              = kerrors.ErrInvalidWaitValue
	ErrInvocation                    = kerrors.ErrInvocation
	ErrIptablesNotFound              = kerrors.ErrIptablesNotFound
	ErrKatharaNotFound               = kerrors.ErrKatharaNotFound
	ErrKubeConfigUnreadable          = kerrors.ErrKubeConfigUnreadable
	ErrKubernetesAPI                 = kerrors.ErrKubernetesAPI
	ErrKubernetesConfigMap           = kerrors.ErrKubernetesConfigMap
	ErrLabAlreadyExists              = kerrors.ErrLabAlreadyExists
	ErrLabDepLoop                    = kerrors.ErrLabDepLoop
	ErrLabHashOrName                 = kerrors.ErrLabHashOrName
	ErrLabNotFound                   = kerrors.ErrLabNotFound
	ErrLabTerminating                = kerrors.ErrLabTerminating
	ErrLinkAlreadyExists             = kerrors.ErrLinkAlreadyExists
	ErrLinkNotFound                  = kerrors.ErrLinkNotFound
	ErrMachineAlreadyExists          = kerrors.ErrMachineAlreadyExists
	ErrMachineBinary                 = kerrors.ErrMachineBinary
	ErrMachineCollisionDomain        = kerrors.ErrMachineCollisionDomain
	ErrMachineNotFound               = kerrors.ErrMachineNotFound
	ErrMachineNotReady               = kerrors.ErrMachineNotReady
	ErrMachineNotRunning             = kerrors.ErrMachineNotRunning
	ErrMachineOption                 = kerrors.ErrMachineOption
	ErrMountDenied                   = kerrors.ErrMountDenied
	ErrNoDevicesInScenario           = kerrors.ErrNoDevicesInScenario
	ErrNoFilesystem                  = kerrors.ErrNoFilesystem
	ErrNoFilesystemCreate            = kerrors.ErrNoFilesystemCreate
	ErrNonSequentialMachineInterface = kerrors.ErrNonSequentialMachineInterface
	ErrNotADirectory                 = kerrors.ErrNotADirectory
	ErrNotSupported                  = kerrors.ErrNotSupported
	ErrOS                            = kerrors.ErrOS
	ErrPermission                    = kerrors.ErrPermission
	ErrPluginNotEnabled              = kerrors.ErrPluginNotEnabled
	ErrPluginNotFound                = kerrors.ErrPluginNotFound
	ErrPrivilege                     = kerrors.ErrPrivilege
	ErrPrivilegeDevicePrivileged     = kerrors.ErrPrivilegeDevicePrivileged
	ErrPrivilegeLabPrivileged        = kerrors.ErrPrivilegeLabPrivileged
	ErrPrivilegeLinkStats            = kerrors.ErrPrivilegeLinkStats
	ErrPrivilegeListAllUsers         = kerrors.ErrPrivilegeListAllUsers
	ErrPrivilegeMachineStats         = kerrors.ErrPrivilegeMachineStats
	ErrPrivilegeWipeAllUsers         = kerrors.ErrPrivilegeWipeAllUsers
	ErrSelectOrExcludeDevices        = kerrors.ErrSelectOrExcludeDevices
	ErrSelectedOrExcludedLinks       = kerrors.ErrSelectedOrExcludedLinks
	ErrSelectedOrExcludedMachines    = kerrors.ErrSelectedOrExcludedMachines
	ErrSettings                      = kerrors.ErrSettings
	ErrSettingsDebugLevel            = kerrors.ErrSettingsDebugLevel
	ErrSettingsDevicePrefix          = kerrors.ErrSettingsDevicePrefix
	ErrSettingsInvalidJSON           = kerrors.ErrSettingsInvalidJSON
	ErrSettingsManagerType           = kerrors.ErrSettingsManagerType
	ErrSettingsNetworksPrefix        = kerrors.ErrSettingsNetworksPrefix
	ErrSettingsNotFound              = kerrors.ErrSettingsNotFound
	ErrSharedSymlink                 = kerrors.ErrSharedSymlink
	ErrStreamReadPermissions         = kerrors.ErrStreamReadPermissions
	ErrSyntax                        = kerrors.ErrSyntax
	ErrUpdateRunningDevice           = kerrors.ErrUpdateRunningDevice
	ErrUpdateRunningLab              = kerrors.ErrUpdateRunningLab
	ErrValue                         = kerrors.ErrValue
	ErrWipeConfirmationRequired      = kerrors.ErrWipeConfirmationRequired
)

var AllCodes = kerrors.AllCodes

// The structured errors: the ones carrying a field the JSON envelope names,
// reachable with errors.As. Each unwraps to its class sentinel, so both
// spellings work on one value.
type (
	BinaryError              = kerrors.BinaryError
	Coder                    = kerrors.Coder
	CollisionDomainError     = kerrors.CollisionDomainError
	FeatureNotAvailableError = kerrors.FeatureNotAvailableError
	HostArchError            = kerrors.HostArchError
	ImageArchError           = kerrors.ImageArchError
	ImageError               = kerrors.ImageError
	LinkError                = kerrors.LinkError
	MacAddressError          = kerrors.MacAddressError
	MachineError             = kerrors.MachineError
	MachineSetError          = kerrors.MachineSetError
	NonSeqInterfaceError     = kerrors.NonSeqInterfaceError
	OptionError              = kerrors.OptionError
	PathError                = kerrors.PathError
	SettingsInvalidError     = kerrors.SettingsInvalidError
	SettingsNotFoundError    = kerrors.SettingsNotFoundError
)

func Code(err error) string { return kerrors.Code(err) }

func HumanLabel(code string) string { return kerrors.HumanLabel(code) }

// Joined is `kerrors.Joined`: the branches of an errors.Join tree, flattened,
// for a renderer that has to name every failure of a fan-out rather than only
// the first.
func Joined(err error) []error { return kerrors.Joined(err) }
