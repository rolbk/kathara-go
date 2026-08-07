// This file is PACKAGE_GRAPH.md D-1: the taxonomy lives in `kerrors` and this
// package re-exports it.
//
// PORT_SPEC §2 puts the errors in `kathara`, and they cannot be here. This
// package must import `model` — half the [Manager] signatures name a
// [model.Machine] or a [model.Lab] — while `model` must raise taxonomy errors
// from its own validation. That is a compile-blocking cycle, so the taxonomy
// sits in a leaf package below both, and these aliases keep the PORT_SPEC §4.3
// surface (`kathara.ErrLabNotFound`, `kathara.MachineError`, …) exactly where
// the spec put it. An alias is the same object, not a copy: errors.Is and
// errors.As see through it in both directions, and
// `errors.Is(err, kathara.ErrLabNotFound)` and
// `errors.Is(err, kerrors.ErrLabNotFound)` are the same question.
//
// What is *not* re-exported: the eighty-odd `New…`/`Wrap…` constructors. They
// are for the code that raises, which means the backends and `cmd/kathara`,
// and those import `kerrors` directly — it is a public package, not an
// internal one. Callers of this package inspect errors; they do not build
// them.

package kathara

import "github.com/KatharaFramework/kathara-go/kerrors"

// The stable string codes of ERROR_CODES.md, one per Python exception class
// plus the three the port adds. They are what the JSON CLI's `error.code`
// field carries (JSON_CLI_CONTRACT.md §5.2) and what the §7 Python client maps
// back onto exception classes, so the mapping is 1:1 and exhaustive.
//
// Six are RESERVED and never emitted: ClassNotFound, HTTPConnection,
// Instantiation, InterfaceNotFound, Test and MachineSignatureNotFound. They
// keep their codes so the table stays a bijection. Two of the six have a
// sentinel aliased below anyway — ErrHTTPConnection and ErrInterfaceNotFound,
// whose only Python raisers are the deferred `webhooks/` and
// `os/Networking.py:38` — and a sentinel that exists is still not a code that
// 1.0 emits.
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

// The feature tokens of a `FeatureNotAvailable` error — the closed set of
// PORT_SPEC §0.3 deferrals (ERROR_CODES.md §5).
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

// The taxonomy sentinels: one per Python exception class, plus the fixed-text
// errors whose message has no interpolated part. Match them with errors.Is —
// including through the errors.Join of a fan-out's partial failures, which is
// how a lab-wide operation reports several at once (PORT_SPEC §4.3).
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

// AllCodes is every code of ERROR_CODES.md, in table order. It is the same
// slice `kerrors` exports, so treat it as read-only.
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

// Code is `kerrors.Code`: the stable string code of an error, or
// [CodeInternalError] for anything the taxonomy does not name — the exhaustive
// fallback of ERROR_CODES.md §1.2. Code(nil) is "".
func Code(err error) string { return kerrors.Code(err) }

// HumanLabel is `kerrors.HumanLabel`: the Python exception class name a code
// renders as in human mode, which is the `({label}) {message}` line
// ERROR_CODES.md §0.1 freezes.
func HumanLabel(code string) string { return kerrors.HumanLabel(code) }

// Joined is `kerrors.Joined`: the branches of an errors.Join tree, flattened,
// for a renderer that has to name every failure of a fan-out rather than only
// the first.
func Joined(err error) []error { return kerrors.Joined(err) }
