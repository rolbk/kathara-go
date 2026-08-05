// Package kerrors is the Kathará error taxonomy (PORT_SPEC §4.3, §0.2 #6),
// frozen by docs/port/ERROR_CODES.md.
//
// Every Python exception class of exceptions.py maps 1:1 onto a code of
// codes.go, and every class that Go 1.0 can raise has exactly one representation
// here: a class sentinel matched with errors.Is, plus a struct type matched
// with errors.As when the error carries data that a JSON field needs. The four
// classes ERROR_CODES.md §1.1 gives the Go representation "none" —
// ClassNotFoundError, InstantiationError, TestError and
// MachineSignatureNotFoundError — keep a reserved code and get no sentinel.
// Struct types unwrap to their class sentinel, so both spellings work on one
// value:
//
//	err := kerrors.NewMachineBinary("frr", "pc1")
//	errors.Is(err, kerrors.ErrMachineBinary) // true
//	var bin *kerrors.BinaryError
//	errors.As(err, &bin)                     // true, bin.Binary == "frr"
//
// The messages are the Python format strings byte-for-byte, typos, missing
// periods and trailing spaces included (ERROR_CODES.md §0.2, DIVERGENCES.md).
// They are built here, never at the call sites, so that the CLI, the JSON
// envelope and the Python client all render the same bytes. Sentinel text is
// identity only and is never shown to a user.
//
// The package is a stdlib-only leaf: nothing in the module is below it
// (PACKAGE_GRAPH.md §1.1). Package kathara re-exports every name of this file
// as an alias, so the spec-§4.3 public surface holds (PACKAGE_GRAPH.md D-1).
package kerrors

import (
	"io/fs"
	"slices"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Core machinery
// ---------------------------------------------------------------------------

// classError is a class sentinel. It carries its JSON code, so classification
// never needs a lookup table keyed by an error value (which would panic for
// error types that are not comparable).
type classError struct {
	code string
	text string
}

func (e *classError) Error() string     { return e.text }
func (e *classError) ErrorCode() string { return e.code }

func newClass(code, text string) error { return &classError{code: code, text: text} }

// message is an error rendering one frozen Python message. It belongs to a
// class sentinel and optionally carries the underlying cause; both are reachable
// through errors.Is and errors.As.
type message struct {
	msg   string
	class error
	cause error
}

func (e *message) Error() string { return e.msg }

// Unwrap lists the class first so that Code never sees the cause shadow the
// class of the error.
func (e *message) Unwrap() []error {
	if e.cause == nil {
		return []error{e.class}
	}
	return []error{e.class, e.cause}
}

// New returns an error rendering msg exactly, classified under the class
// sentinel of this package.
//
// Call sites should prefer the named constructors of messages.go, which hold
// the frozen templates; New exists for the sites a later phase adds and for
// packages that must build a taxonomy error from text they already rendered.
func New(class error, msg string) error {
	return &message{msg: msg, class: class}
}

// Wrap is New carrying an underlying cause, which stays reachable with
// errors.Is and errors.As while the rendered message remains msg alone: the
// Python messages never quote the wrapped error unless the template says so.
func Wrap(class error, cause error, msg string) error {
	return &message{msg: msg, class: class, cause: cause}
}

// notJoin marks the types of this package whose Unwrap() []error carries a
// class sentinel (and optionally a cause) rather than a batch of sibling
// errors. Joined consults the marker instead of a list of concrete types, so a
// type added later cannot be shredded into the `errors` array by omission: it
// either implements the marker or does not implement Unwrap() []error at all.
// TestOwnMultiUnwrapTypesAreMarked pins that.
type notJoin interface{ notAJoin() }

func (e *message) notAJoin()               {}
func (e *SettingsNotFoundError) notAJoin() {}

// Joined returns the elements of a multi-error, in order, or nil when err
// carries no batch.
//
// It exists because errors of this package also implement Unwrap() []error to
// carry their class and cause: expanding those as if they were a join would
// emit the class sentinel as a sibling error. Use this, never a bare type
// assertion, to build the `errors` array of ERROR_CODES.md §6.5.
//
// A batch is anything outside this package with Unwrap() []error. That is
// errors.Join, which is how the port builds batches (§6.2), but also an
// fmt.Errorf with more than one %w — the stdlib gives both the same shape and
// they are indistinguishable from outside. Do not build multi-%w errors on a
// path that reaches the CLI boundary: such an error would be reported as a
// batch, and its formatted text would be replaced by its elements.
// TestJoinedTreatsMultiWrapAsBatch pins the behaviour.
//
// The result is a fresh slice. errors.Join hands out its own backing array, and
// the caller of Joined is the one that sorts the batch into the canonical order
// of ERROR_CODES.md §6.2 — sorting the returned slice in place would reorder the
// join itself and silently change which element Code reports, i.e. the primary
// error of §6.3 and the `code` of the JSON envelope.
func Joined(err error) []error {
	if err == nil {
		return nil
	}
	if _, ok := err.(notJoin); ok {
		return nil
	}

	multi, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return nil
	}
	return slices.Clone(multi.Unwrap())
}

// ---------------------------------------------------------------------------
// Context wrappers (ERROR_CODES.md §0.3): they attach a structured JSON field
// and render the message of the error they wrap unchanged.
// ---------------------------------------------------------------------------

// MachineError attaches the JSON field "machine" to a device-scoped error.
// Op is free-form context for the Go API and the debug-level error chain; it
// never reaches the rendered message.
type MachineError struct {
	Machine string
	Op      string
	Err     error
}

func (e *MachineError) Error() string {
	if e.Err == nil {
		return "kathara: error on device `" + e.Machine + "`"
	}
	return e.Err.Error()
}

func (e *MachineError) Unwrap() error { return e.Err }

// WrapMachine attaches machine (and the operation name op, which may be empty)
// to err. It returns nil when err is nil.
func WrapMachine(machine, op string, err error) error {
	if err == nil {
		return nil
	}
	return &MachineError{Machine: machine, Op: op, Err: err}
}

// LinkError attaches the JSON field "link" to a collision-domain-scoped error.
type LinkError struct {
	Link string
	Op   string
	Err  error
}

func (e *LinkError) Error() string {
	if e.Err == nil {
		return "kathara: error on collision domain `" + e.Link + "`"
	}
	return e.Err.Error()
}

func (e *LinkError) Unwrap() error { return e.Err }

// WrapLink attaches link (and the operation name op, which may be empty) to
// err. It returns nil when err is nil.
func WrapLink(link, op string, err error) error {
	if err == nil {
		return nil
	}
	return &LinkError{Link: link, Op: op, Err: err}
}

// ImageError attaches the JSON field "image" to an image-scoped error.
type ImageError struct {
	Image string
	Err   error
}

func (e *ImageError) Error() string {
	if e.Err == nil {
		return "kathara: error on image `" + e.Image + "`"
	}
	return e.Err.Error()
}

func (e *ImageError) Unwrap() error { return e.Err }

// WrapImage attaches image to err. It returns nil when err is nil.
func WrapImage(image string, err error) error {
	if err == nil {
		return nil
	}
	return &ImageError{Image: image, Err: err}
}

// PathError attaches the JSON field "path" to the filesystem-validation errors
// of utils.check_directory_permissions (FileExists, NotADirectory).
type PathError struct {
	Path string
	Err  error
}

func (e *PathError) Error() string {
	if e.Err == nil {
		return "kathara: error on path `" + e.Path + "`"
	}
	return e.Err.Error()
}

func (e *PathError) Unwrap() error { return e.Err }

// WrapPath attaches path to err. It returns nil when err is nil.
func WrapPath(path string, err error) error {
	if err == nil {
		return nil
	}
	return &PathError{Path: path, Err: err}
}

// ---------------------------------------------------------------------------
// Class sentinels — 40 in all: one per Python exception class that has a Go
// representation (ERROR_CODES.md §1.1, i.e. 29 of the 33; the four "none" rows
// are code-only), one per user-reachable builtin (§1.2), one per third-party
// passthrough (§1.3), plus the JSON-mode ConfirmationRequired (§1.4).
// FeatureNotAvailable and InternalError have no sentinel: the first reports its
// code from its struct, the second is the unmapped-error fallback.
//
// The texts of the seven sentinels named by PORT_SPEC §4.3 are verbatim from
// the spec. No sentinel text is ever user-visible.
// ---------------------------------------------------------------------------

var (
	// ErrHTTPConnection is RESERVED: raised only by the deferred webhooks/.
	ErrHTTPConnection = newClass(CodeHTTPConnection, "kathara: HTTP connection error")

	// ErrInvocation is an API misuse: mutually exclusive or missing arguments.
	ErrInvocation = newClass(CodeInvocation, "kathara: invalid invocation")

	// ErrSettings is an invalid settings file; see SettingsInvalidError.
	ErrSettings = newClass(CodeSettings, "kathara: settings file is not valid")

	// ErrSettingsNotFound is a missing settings file; see SettingsNotFoundError.
	ErrSettingsNotFound = newClass(CodeSettingsNotFound, "kathara: settings file not found")

	// ErrDaemonConnection is DockerDaemonConnectionError (spec §4.3 name).
	ErrDaemonConnection = newClass(CodeDockerDaemonConnection, "kathara: cannot connect to the container daemon")

	// ErrNotSupported is an operation the selected backend refuses.
	ErrNotSupported = newClass(CodeNotSupported, "kathara: not supported")

	// ErrPrivilege is a missing-root check.
	ErrPrivilege = newClass(CodePrivilege, "kathara: root privileges required")

	// ErrInterfaceNotFound is RESERVED: its sole raiser, os/Networking.py:38,
	// is deferred.
	ErrInterfaceNotFound = newClass(CodeInterfaceNotFound, "kathara: interface not found")

	// ErrHostArchitecture is an unsupported host architecture; see HostArchError.
	ErrHostArchitecture = newClass(CodeHostArchitecture, "kathara: unsupported host architecture")

	// ErrLabAlreadyExists is a network scenario that is still terminating.
	ErrLabAlreadyExists = newClass(CodeLabAlreadyExists, "kathara: network scenario already exists")

	// ErrLabNotFound is an object not associated to a network scenario.
	ErrLabNotFound = newClass(CodeLabNotFound, "kathara: network scenario not found")

	// ErrEmptyLab is a network scenario with no devices.
	ErrEmptyLab = newClass(CodeEmptyLab, "kathara: network scenario has no devices")

	// ErrDependencyLoop is MachineDependencyError (spec §4.3 name); the JSON
	// code stays MachineDependency for the 1:1 class mapping.
	ErrDependencyLoop = newClass(CodeMachineDependency, "kathara: dependency loop in lab.dep")

	// ErrMountDenied is a refused volume mount.
	ErrMountDenied = newClass(CodeMountDenied, "kathara: volume mount denied")

	// ErrMachineAlreadyExists is a duplicate device name.
	ErrMachineAlreadyExists = newClass(CodeMachineAlreadyExists, "kathara: device already exists")

	// ErrNonSequentialMachineInterface is a hole in the interface numbering;
	// see NonSeqInterfaceError.
	ErrNonSequentialMachineInterface = newClass(CodeNonSequentialMachineIface, "kathara: non-sequential device interface")

	// ErrMachineOption is an invalid device option; see OptionError.
	ErrMachineOption = newClass(CodeMachineOption, "kathara: invalid device option")

	// ErrMachineCollisionDomain is a device/collision-domain conflict; see
	// CollisionDomainError.
	ErrMachineCollisionDomain = newClass(CodeMachineCollisionDomain, "kathara: device collision domain conflict")

	// ErrMachineNotFound is a device missing from the scenario or the backend.
	ErrMachineNotFound = newClass(CodeMachineNotFound, "kathara: device not found")

	// ErrMachineNotRunning is a device that is not deployed.
	ErrMachineNotRunning = newClass(CodeMachineNotRunning, "kathara: device not running")

	// ErrMachineNotReady is a deployed device that is not ready yet.
	ErrMachineNotReady = newClass(CodeMachineNotReady, "kathara: device not ready")

	// ErrMachineBinary is a binary missing inside a device; see BinaryError.
	ErrMachineBinary = newClass(CodeMachineBinary, "kathara: binary not found in device")

	// ErrInterfaceMacAddress is an invalid MAC address; see MacAddressError.
	ErrInterfaceMacAddress = newClass(CodeInterfaceMacAddress, "kathara: invalid interface MAC address")

	// ErrLinkNotFound is a collision domain missing from the scenario or the
	// backend.
	ErrLinkNotFound = newClass(CodeLinkNotFound, "kathara: collision domain not found")

	// ErrLinkAlreadyExists is a duplicate collision domain name.
	ErrLinkAlreadyExists = newClass(CodeLinkAlreadyExists, "kathara: collision domain already exists")

	// ErrInvalidImageArchitecture is an image/host architecture mismatch; see
	// ImageArchError. The Python class is-a ValueError.
	ErrInvalidImageArchitecture = newClass(CodeInvalidImageArchitecture, "kathara: incompatible image architecture")

	// ErrDockerImageNotFound is an image available neither locally nor on the
	// registry.
	ErrDockerImageNotFound = newClass(CodeDockerImageNotFound, "kathara: docker image not found")

	// ErrDockerPlugin is a missing, disabled or inconsistent network plugin.
	ErrDockerPlugin = newClass(CodeDockerPlugin, "kathara: docker network plugin error")

	// ErrKubernetesConfigMap is a device folder too large to upload.
	ErrKubernetesConfigMap = newClass(CodeKubernetesConfigMap, "kathara: kubernetes config map error")

	// ErrSyntax is Python's SyntaxError at the device-name, --eth and
	// parse_cd_mac_address sites; the lab-file sites use labfile.ParseError,
	// which unwraps to this sentinel.
	ErrSyntax = newClass(CodeSyntax, "kathara: syntax error")

	// ErrValue is Python's ValueError at the sites of ERROR_CODES.md §2.
	ErrValue = newClass(CodeValue, "kathara: invalid value")

	// ErrOS is Python's OSError, which is also its IOError: the parser
	// raise IOError(...) sites observe the class name OSError.
	ErrOS = newClass(CodeOS, "kathara: os error")

	// ErrFileNotFound is Python's FileNotFoundError. Errors carrying it also
	// wrap fs.ErrNotExist.
	ErrFileNotFound = newClass(CodeFileNotFound, "kathara: file not found")

	// ErrFileExists is Python's FileExistsError, which utils.py raises when the
	// path does NOT exist. The class label and message keep parity; errors
	// carrying it wrap fs.ErrNotExist, never fs.ErrExist (ERROR_CODES.md §0.2).
	ErrFileExists = newClass(CodeFileExists, "kathara: path does not exist")

	// ErrNotADirectory is Python's NotADirectoryError.
	ErrNotADirectory = newClass(CodeNotADirectory, "kathara: not a directory")

	// ErrPermission is Python's PermissionError. Errors carrying it also wrap
	// fs.ErrPermission.
	ErrPermission = newClass(CodePermission, "kathara: permission denied")

	// ErrConnection is Python's ConnectionError.
	ErrConnection = newClass(CodeConnection, "kathara: connection error")

	// ErrDockerAPI is a Docker daemon error no translation rule matched
	// (ERROR_CODES.md §1.3, §3); human label APIError.
	ErrDockerAPI = newClass(CodeDockerAPI, "kathara: docker api error")

	// ErrKubernetesAPI is a Kubernetes API error no translation rule matched;
	// human label ApiException.
	ErrKubernetesAPI = newClass(CodeKubernetesAPI, "kathara: kubernetes api error")

	// ErrConfirmationRequired is the json/jsonl-only refusal to wipe without
	// --force (JSON_CLI_CONTRACT.md §1.5). It can never be raised in human
	// mode, where the interactive prompt is unchanged.
	ErrConfirmationRequired = newClass(CodeConfirmationRequired, "kathara: confirmation required")
)

// ---------------------------------------------------------------------------
// Data-bearing class types (ERROR_CODES.md §0.3, §0.4): they render their own
// message and unwrap to their class sentinel.
// ---------------------------------------------------------------------------

// BinaryError is MachineBinaryError: a binary missing inside a device.
// JSON fields "binary" and "machine"; both are part of the lab-checker
// compatibility contract (ERROR_CODES.md §4).
type BinaryError struct {
	Binary  string
	Machine string
}

// Error is exceptions.py:116.
func (e *BinaryError) Error() string {
	return "Binary `" + e.Binary + "` not found in device `" + e.Machine + "`."
}

func (e *BinaryError) Unwrap() error { return ErrMachineBinary }

// ImageArchError is InvalidImageArchitectureError: an image incompatible with
// the host architecture. JSON fields "image" and "arch".
type ImageArchError struct {
	Image string
	Arch  string
}

// Error is exceptions.py:160 — no trailing period, as in Python.
func (e *ImageArchError) Error() string {
	return "Docker Image `" + e.Image + "` is not compatible with your host architecture `" + e.Arch + "`"
}

func (e *ImageArchError) Unwrap() error { return ErrInvalidImageArchitecture }

// NonSeqInterfaceError is NonSequentialMachineInterfaceError: a hole in the
// interface numbering found by Machine.check(). JSON fields "iface" and
// "machine".
type NonSeqInterfaceError struct {
	Iface   int
	Machine string
}

// Error is exceptions.py:83.
func (e *NonSeqInterfaceError) Error() string {
	return "Interface `" + strconv.Itoa(e.Iface) + "` missing on device `" + e.Machine + "`."
}

func (e *NonSeqInterfaceError) Unwrap() error { return ErrNonSequentialMachineInterface }

// MacAddressError is InterfaceMacAddressError: a MAC address that does not
// match the MAC regex. JSON fields "mac", "iface" and "machine".
type MacAddressError struct {
	MAC     string
	Iface   int
	Machine string
}

// Error is exceptions.py:123 — the MAC is not backquoted, as in Python.
func (e *MacAddressError) Error() string {
	return "MAC address " + e.MAC + " on interface `" + strconv.Itoa(e.Iface) +
		"` of device `" + e.Machine + "` is invalid."
}

func (e *MacAddressError) Unwrap() error { return ErrInterfaceMacAddress }

// Variants of MachineCollisionDomainError (ERROR_CODES.md §2), numbered by
// raise site. Variants 4 and 5 render exactly like 2 and 3; they are kept
// apart because the sites differ (model/Machine.py vs manager/docker).
const (
	// CDVariantInterfaceTaken is model/Machine.py:106.
	CDVariantInterfaceTaken = 1
	// CDVariantAlreadyConnected is model/Machine.py:109.
	CDVariantAlreadyConnected = 2
	// CDVariantNotConnected is model/Machine.py:132.
	CDVariantNotConnected = 3
	// CDVariantManagerAlreadyConnected is manager/docker/DockerManager.py:209.
	CDVariantManagerAlreadyConnected = 4
	// CDVariantManagerNotConnected is manager/docker/DockerManager.py:259.
	CDVariantManagerNotConnected = 5
)

// CollisionDomainError is MachineCollisionDomainError, one of the four classes
// kathara-lab-checker catches by name (ERROR_CODES.md §4). JSON fields
// "machine" and "link", or "machine" and "iface" for variant 1.
type CollisionDomainError struct {
	Machine string
	Link    string
	Iface   int
	Variant int
}

// Error renders the variant recorded at construction; an unknown variant falls
// back to the "already connected" wording, which is the only one reachable
// without a variant (a zero value cannot be produced by the constructors).
func (e *CollisionDomainError) Error() string {
	switch e.Variant {
	case CDVariantInterfaceTaken:
		return "Interface " + strconv.Itoa(e.Iface) + " already set on device `" + e.Machine + "`."
	case CDVariantNotConnected, CDVariantManagerNotConnected:
		return "Device `" + e.Machine + "` is not connected to collision domain `" + e.Link + "`."
	default:
		return "Device `" + e.Machine + "` is already connected to collision domain `" + e.Link + "`."
	}
}

func (e *CollisionDomainError) Unwrap() error { return ErrMachineCollisionDomain }

// OptionError is MachineOptionError: an invalid value for a device option.
// JSON fields "machine" and "option" (the option name being parsed). Message
// holds the rendered template, which differs per option and per failure.
type OptionError struct {
	Machine string
	Option  string
	Message string
}

func (e *OptionError) Error() string { return e.Message }

func (e *OptionError) Unwrap() error { return ErrMachineOption }

// MachineSetError is the plural MachineNotFoundError variant, raised when a
// --machine selection names devices the network scenario does not have. It is a
// class type rather than a MachineError wrap because the JSON field is the
// array "machines" (JSON_CLI_CONTRACT.md §5.4).
type MachineSetError struct {
	// Machines is the JSON "machines" array. NewMachineNotFoundSet stores it
	// sorted bytewise ascending, matching the message; Error sorts again so a
	// hand-built value still renders in the canonical order of §0.2.
	Machines []string
}

// Error is DockerManager.py:151,155; KubernetesManager.py:111,115, with the
// Python set rendered in canonical order.
func (e *MachineSetError) Error() string {
	return "The following devices are not in the network scenario: " + pythonSet(e.Machines) + "."
}

func (e *MachineSetError) Unwrap() error { return ErrMachineNotFound }

// SettingsInvalidError is SettingsError: Reason is the per-check sentence the
// Python wrapper interpolates.
type SettingsInvalidError struct {
	Reason string
}

// Error is exceptions.py:21.
func (e *SettingsInvalidError) Error() string {
	return "Settings file is not valid: " + e.Reason + " Fix it or delete it before launching."
}

func (e *SettingsInvalidError) Unwrap() error { return ErrSettings }

// SettingsNotFoundError is SettingsNotFoundError: the settings file is missing.
// JSON field "path".
type SettingsNotFoundError struct {
	Path string
}

// Error is exceptions.py:26.
func (e *SettingsNotFoundError) Error() string {
	return "Settings file not found in path `" + e.Path + "`."
}

// Unwrap also yields fs.ErrNotExist, so callers that heal a missing settings
// file the way Python's __main__ does can test for it directly.
func (e *SettingsNotFoundError) Unwrap() []error {
	return []error{ErrSettingsNotFound, fs.ErrNotExist}
}

// HostArchError is HostArchitectureError: an architecture utils.get_architecture
// does not map. JSON field "arch".
type HostArchError struct {
	Arch string
}

// Error is exceptions.py:50.
func (e *HostArchError) Error() string {
	return "Not implemented for host architecture `" + e.Arch + "`."
}

func (e *HostArchError) Unwrap() error { return ErrHostArchitecture }

// Feature tokens of FeatureNotAvailableError: the closed set of 1.0
// (ERROR_CODES.md §5, JSON_CLI_CONTRACT.md §5.6).
const (
	// FeatureLabExt covers lab.ext and external collision domains.
	FeatureLabExt = "lab.ext"
	// FeatureLinfo is the whole linfo command.
	FeatureLinfo = "linfo"
	// FeatureStatsSampling is the resource-sampling half of the stats API;
	// the inventory fields keep working.
	FeatureStatsSampling = "stats-sampling"
	// FeatureWebhooks is the Docker Hub / GitHub image and version lookups.
	FeatureWebhooks = "webhooks"
)

// featureMessages holds the frozen message of each deferred feature.
var featureMessages = map[string]string{
	FeatureLabExt:        "lab.ext external links are not supported in this release. Use Kathará 3.8.x.",
	FeatureLinfo:         "The linfo command is not supported in this release. Use Kathará 3.8.x.",
	FeatureStatsSampling: "Resource statistics sampling is not supported in this release. Use Kathará 3.8.x.",
	FeatureWebhooks:      "Docker Hub image listing is not supported in this release. Use Kathará 3.8.x.",
}

// FeatureNotAvailableError is the port-new code for the features deferred to
// post-1.0. JSON field "feature". It reports its own code because it wraps
// ErrNotSupported: the Python client raises NotSupportedError for it, with the
// same message verbatim (ERROR_CODES.md §1.4, §5).
type FeatureNotAvailableError struct {
	Feature string
}

// Error is the frozen message of the feature; a token outside the closed set
// renders in the same shape.
func (e *FeatureNotAvailableError) Error() string {
	if msg, ok := featureMessages[e.Feature]; ok {
		return msg
	}
	return "`" + e.Feature + "` is not supported in this release. Use Kathará 3.8.x."
}

func (e *FeatureNotAvailableError) ErrorCode() string { return CodeFeatureNotAvailable }

func (e *FeatureNotAvailableError) Unwrap() error { return ErrNotSupported }

// ---------------------------------------------------------------------------
// Helpers shared by the message catalog
// ---------------------------------------------------------------------------

// pythonSet renders names the way Python renders a set inside an f-string,
// with the elements sorted bytewise ascending instead of in Python's
// nondeterministic iteration order (ERROR_CODES.md §0.2, JSON_CLI_CONTRACT.md
// §5.3). The empty set renders as Python's set(), which the raise sites of
// DockerManager.py:151 cannot reach.
//
// Quoting is unconditionally single, per the frozen ruling ("elements
// single-quoted, comma-space separated, braces", ERROR_CODES.md §0.2). Note that
// the names reaching this variant are NOT device names validated by
// ^[a-z0-9_]{1,30}$ (model/Machine.py:62): they are exactly the argv words that
// did not match a device, taken from an unvalidated nargs='*' argparse argument
// (LstartCommand.py:139,231) and set-differenced at DockerManager.py:151. CPython
// would therefore apply full repr quoting to them — {"it's"} for a name holding
// an apostrophe, backslash doubling, \xNN for control characters. The frozen
// ruling pins the single-quote form, so the port keeps it and the residual gap
// is recorded in PROPOSED-DIVERGENCES.md rather than fixed here.
func pythonSet(names []string) string {
	if len(names) == 0 {
		return "set()"
	}

	sorted := sortedCopy(names)

	var b strings.Builder
	b.WriteString("{")
	for i, name := range sorted {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("'")
		b.WriteString(name)
		b.WriteString("'")
	}
	b.WriteString("}")

	return b.String()
}

// sortedCopy is the canonical order of a name collection: bytewise ascending,
// on a copy, so a caller's slice is never reordered under it.
func sortedCopy(names []string) []string {
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	return sorted
}

// causeText is str(e) of a wrapped third-party error, with a nil cause
// rendering as the empty string Python could never produce.
func causeText(cause error) string {
	if cause == nil {
		return ""
	}
	return cause.Error()
}

// The two types that report a code without going through a class sentinel.
var (
	_ Coder = (*classError)(nil)
	_ Coder = (*FeatureNotAvailableError)(nil)
)
