package model

import (
	"errors"
	"math/big"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
)

// machineCapabilities is `MACHINE_CAPABILITIES` (`model/Machine.py:27`), the
// Linux capabilities both backends give every device. Nothing in the model
// reads it; both managers do, and the constant belongs with the model file it
// is declared in.
var machineCapabilities = []string{"NET_ADMIN", "NET_RAW", "NET_BROADCAST", "NET_BIND_SERVICE", "SYS_ADMIN"}

// MachineCapabilities returns `MACHINE_CAPABILITIES` in source order.
func MachineCapabilities() []string { return append([]string(nil), machineCapabilities...) }

// allowedVolumeModes is `ALLOWED_VOLUME_MODES` (`model/Machine.py:28`).
// `rx` really is accepted — the man page mentions it, the tests never exercise
// it, and the code takes it (SYNTHESIS C-6).
var allowedVolumeModes = []string{"ro", "rw", "rx"}

// AllowedVolumeModes returns `ALLOWED_VOLUME_MODES` in source order.
func AllowedVolumeModes() []string { return append([]string(nil), allowedVolumeModes...) }

// The meta parsers, translated from Python's `re` dialect to RE2.
//
// Two translation rules apply throughout and both are load-bearing:
//
//   - Python's `\w` and `\d` are Unicode for a `str` pattern. `\w` becomes
//     `[\p{L}\p{N}_]` (RULINGS.md OQ-14a) and `\d` becomes `[\p{Nd}]`, so
//     `pc1[ulimit]=ключ=٣:٥` still parses to soft 3, hard 5 as it does on the
//     oracle. RE2's own `\w` and `\d` are ASCII and would reject both.
//   - Python's `$` matches at the end of the string *or* immediately before a
//     final newline, so every pattern anchored with `$` gets a `\n?` before it.
//     `add_meta("ulimit", "nofile=5\n")` parses on the oracle; `"nofile=5\nx"`
//     does not.
var (
	// sysctlRegex is `model/Machine.py:172`. The key must start with `net.` and
	// carry at least two further dot-separated labels — `net.foo=1` is rejected
	// with the namespace error (vector `labconf/sysctl_shallow_key`) — and the
	// value may be empty but may not contain another `=`.
	sysctlRegex = regexp.MustCompile(`^(net\.(?:[\p{L}\p{N}_-]+\.)+[\p{L}\p{N}_-]+)=([^=]*)\n?$`)

	// envRegex is `model/Machine.py:191`. The value class is `.*`, so a second
	// `=` lands in the value (`K=1=2` stores `1=2`) — the opposite of sysctl.
	envRegex = regexp.MustCompile(`^([\p{L}\p{N}_]+)=(.*)\n?$`)

	// ulimitRegex is `model/Machine.py:207`, `key=soft[:hard]`.
	ulimitRegex = regexp.MustCompile(`^([\p{L}\p{N}_]+)=(-?[\p{Nd}]+)(?::(-?[\p{Nd}]+))?\n?$`)
)

// allowedPortProtocols is the protocol set of `model/Machine.py:253`.
var allowedPortProtocols = []string{"tcp", "udp", "sctp"}

// PortKey is Python's `(host_port, protocol)` tuple, the key of `meta['ports']`
// (`model/Machine.py:257`).
type PortKey struct {
	// HostPort is the port published on the host. It defaults to 3000 when the
	// value names only a guest port (`model/Machine.py:248`).
	HostPort int
	// Protocol is `tcp`, `udp` or `sctp`, lower-cased.
	Protocol string
}

// String renders the key the way Python renders the tuple inside
// `Machine.__str__` — `(3000, 'tcp')`, single quotes included
// (`model/Machine.py:841`).
func (k PortKey) String() string {
	return "(" + strconv.Itoa(k.HostPort) + ", '" + k.Protocol + "')"
}

// Ulimit is one `meta['ulimits']` entry: Python's `{'soft': …, 'hard': …}`.
//
// Python's integers are arbitrary precision and these are not; a literal wider
// than an int64 clamps (see saturateInt64, DIVERGENCES.md). The comparisons and
// the error message that quotes the hard limit are done at full precision
// first, so the only thing a clamp can change is the stored number of a value
// no container runtime would accept.
type Ulimit struct {
	Soft int64
	Hard int64
}

// Volume is one `meta['volumes']` entry, keyed by the absolute host path.
type Volume struct {
	// GuestPath is the mount point inside the device, stored verbatim — it is
	// not stripped, so `add_meta("volume", "/h|/g\n")` keeps the newline
	// (oracle-verified).
	GuestPath string
	// Mode is one of [AllowedVolumeModes].
	Mode string
}

// Meta is the typed form of `Machine.meta` (PORT_SPEC §4.2, §0.2 #5).
//
// Python's `meta` is a `Dict[str, Any]` seeded with six always-present
// containers and grown with whatever `add_meta` is given. The typed form keeps
// three properties of that dict that the port is not allowed to lose:
//
//   - **presence is information.** Every optional field is either a pointer or
//     a [Scalar], both of which distinguish "unset" from a set falsy value.
//     `bridged` absent and `bridged` False are different — the second prints a
//     `Bridged Connection: False` line in [Machine.String] — and `ipv6` absent
//     falls back to the settings default while `ipv6` False overrides it.
//   - **the stored type is information.** lab.conf stores every meta as a
//     string, the API stores real bools and ints, and the accessors branch on
//     the difference. Hence [Scalar] rather than `string` on the five metas
//     that can arrive either way.
//   - **the containers are ordered.** Sysctls, envs, ports, ulimits and volumes
//     are applied to a container in insertion order, and the volume index in
//     the Kubernetes pod spec is the iteration position (ORDERING.tsv).
//
// Unknown names are not rejected: `add_meta` has a generic fallback, so any key
// at all can be stored. They land in [Meta.Extras] (PACKAGE_GRAPH.md §0
// ruling).
type Meta struct {
	// ExecCommands is `meta['exec_commands']`, in append order — the order the
	// commands run at boot. `add_meta("exec", …)` never de-duplicates and never
	// reports a previous value.
	ExecCommands []string

	// Sysctls is `meta['sysctls']`. A value that `str.isnumeric()` accepts is
	// stored as an int and everything else as a string, and the distinction
	// survives to the container config (`net.a.b=1` is 1, `net.a.b=abc` is
	// "abc").
	Sysctls *OrderedMap[string, Scalar]

	// Envs is `meta['envs']`. Values are always strings, even numeric ones —
	// unlike sysctls.
	Envs *OrderedMap[string, string]

	// Ports is `meta['ports']`: host port and protocol to guest port.
	Ports *OrderedMap[PortKey, int]

	// Ulimits is `meta['ulimits']`.
	Ulimits *OrderedMap[string, Ulimit]

	// Volumes is `meta['volumes']`, keyed by the *absolute* host path — Python
	// applies `os.path.abspath` against the process working directory at the
	// moment the meta is set (`model/Machine.py:284`).
	Volumes *OrderedMap[string, Volume]

	// Privileged is `meta['privileged']`, always a real bool: `add_meta` runs
	// the value through `strtobool` (`model/Machine.py:159`). nil is "not set",
	// which [Machine.IsPrivileged] reports as false.
	Privileged *bool

	// Bridged is `meta['bridged']`, same treatment.
	Bridged *bool

	// Image is `meta['image']`.
	Image Scalar
	// Mem is `meta['mem']`, raw: [Machine.GetMem] is what validates it.
	Mem Scalar
	// CPUs is `meta['cpus']`, raw: [Machine.GetCPU] is what validates it.
	CPUs Scalar
	// Shell is `meta['shell']`.
	Shell Scalar
	// NumTerms is `meta['num_terms']`, raw: [Machine.GetNumTerms] validates.
	NumTerms Scalar
	// IPv6 is `meta['ipv6']`, raw and genuinely polymorphic: a bool from the
	// API, a string from lab.conf, and [Machine.IsIPv6Enabled] branches on
	// which.
	IPv6 Scalar
	// Entrypoint is `meta['entrypoint']`.
	Entrypoint Scalar
	// Args is `meta['args']`: a string from lab.conf, a list from the CLI's
	// argparse REMAINDER.
	Args Scalar
	// BridgedIface is `meta['bridged_iface']`, the interface number the bridge
	// occupies. An int fills a slot in [Machine.Check]'s sequence; a string —
	// which is all lab.conf can produce — makes that check raise
	// [ErrBridgedIfaceType] (DIVERGENCES.md 1).
	BridgedIface Scalar

	// Extras holds every other meta name, in the order the names were first
	// set. `add_meta` accepts anything (`model/Machine.py:289`), so an API user
	// or a lab.conf option the parser does not filter ends up here rather than
	// being rejected.
	Extras *OrderedMap[string, Scalar]
}

// newMeta is the `self.meta = {...}` literal of `Machine.__init__`
// (`model/Machine.py:69-76`): the six containers exist from the start, every
// scalar is absent.
func newMeta() Meta {
	return Meta{
		ExecCommands: []string{},
		Sysctls:      NewOrderedMap[string, Scalar](),
		Envs:         NewOrderedMap[string, string](),
		Ports:        NewOrderedMap[PortKey, int](),
		Ulimits:      NewOrderedMap[string, Ulimit](),
		Volumes:      NewOrderedMap[string, Volume](),
		Extras:       NewOrderedMap[string, Scalar](),
	}
}

// MetaEntry is one scalar meta, named as lab.conf names it.
type MetaEntry struct {
	Name  string
	Value Scalar
}

// Scalars returns every scalar meta that is set, i.e. what `Machine.meta` holds
// besides the six containers, with `privileged` and `bridged` rendered as the
// bools Python stores.
//
// It exists for the serializers — the Layer B vector shape puts exactly these
// under `meta.extra` — and it is the only way to enumerate the typed fields
// without hard-coding the list at each caller.
//
// The order is this struct's declaration order followed by [Meta.Extras] in
// insertion order. Python's is one insertion order over the whole dict; the
// difference is unobservable, because the only consumer that renders them
// (`Machine.__repr__`, a debug log line) is not part of any contract and the
// vector JSON sorts its keys.
func (m *Meta) Scalars() []MetaEntry {
	out := make([]MetaEntry, 0, 11+m.Extras.Len())
	add := func(name string, v Scalar) {
		if v.IsSet() {
			out = append(out, MetaEntry{Name: name, Value: v})
		}
	}
	add("image", m.Image)
	add("mem", m.Mem)
	add("cpus", m.CPUs)
	add("shell", m.Shell)
	add("num_terms", m.NumTerms)
	add("ipv6", m.IPv6)
	if m.Privileged != nil {
		add("privileged", Bool(*m.Privileged))
	}
	if m.Bridged != nil {
		add("bridged", Bool(*m.Bridged))
	}
	add("entrypoint", m.Entrypoint)
	add("args", m.Args)
	add("bridged_iface", m.BridgedIface)
	for _, e := range m.Extras.Entries() {
		out = append(out, MetaEntry{Name: e.Key, Value: e.Value})
	}
	return out
}

// scalarField returns the address of the [Scalar] field a meta name owns, or
// nil when the name is not one of the typed scalars.
func (m *Meta) scalarField(name string) *Scalar {
	switch name {
	case "image":
		return &m.Image
	case "mem":
		return &m.Mem
	case "cpus":
		return &m.CPUs
	case "shell":
		return &m.Shell
	case "num_terms":
		return &m.NumTerms
	case "ipv6":
		return &m.IPv6
	case "entrypoint":
		return &m.Entrypoint
	case "args":
		return &m.Args
	case "bridged_iface":
		return &m.BridgedIface
	}
	return nil
}

// setScalar is the generic branch of `add_meta` (`model/Machine.py:289-291`):
// store the value as it came, return the previous one.
func (m *Meta) setScalar(name string, value Scalar) (any, bool) {
	if field := m.scalarField(name); field != nil {
		prev := *field
		*field = value
		return prev.Value(), prev.IsSet()
	}
	prev, existed := m.Extras.Set(name, value)
	return prev.Value(), existed
}

// AddMeta is `Machine.add_meta` (`model/Machine.py:144`): parse one meta
// assignment and store it, returning the value the key held before.
//
// The return is `(previous, existed)` and not `previous == nil`, because a
// previous value can itself be falsy and `LabParser` uses only presence to
// decide whether to warn about a duplicate (NILABILITY.tsv:8).
//
// The value is a string because that is what both callers have: lab.conf hands
// the parser a string, and `Lab.assign_meta_to_machine` is typed `meta_value:
// str`. Typed values reach the meta through [Machine.UpdateMeta] instead, which
// is the path `Machine(**kwargs)` takes.
//
// Errors: [kerrors.ErrMachineOption] for every value this rejects deliberately;
// [kerrors.ErrValue] for the two places Python crashes with a bare ValueError
// (a `strtobool` failure, and a sysctl value like `--5` that `isnumeric`
// accepts and `int()` does not — DIVERGENCES.md, vector
// `labconf/sysctl_double_dash_crash`).
func (m *Machine) AddMeta(name, value string) (any, bool, error) {
	switch name {
	case "privileged":
		return m.addBoolMeta(&m.Meta.Privileged, value)
	case "exec":
		// Appends and always reports "no previous value", even for a repeat:
		// the exec list is the boot command sequence, so a second value is a
		// second command and never an overwrite (`model/Machine.py:162-164`).
		m.Meta.ExecCommands = append(m.Meta.ExecCommands, value)
		return nil, false, nil
	case "bridged":
		return m.addBoolMeta(&m.Meta.Bridged, value)
	case "sysctl":
		return m.addSysctlMeta(value)
	case "env":
		return m.addEnvMeta(value)
	case "ulimit":
		return m.addUlimitMeta(value)
	case "port":
		return m.addPortMeta(value)
	case "volume":
		return m.addVolumeMeta(value)
	case "exec_commands", "sysctls", "envs", "ports", "ulimits", "volumes":
		return m.addContainerNameMeta(name, value)
	}

	prev, existed := m.Meta.setScalar(name, Str(value))
	return prev, existed, nil
}

// addContainerNameMeta is `add_meta` called with the *plural* name of one of the
// six containers `Machine.__init__` seeds.
//
// Python has no case for these names, so they fall into the generic branch
// (`model/Machine.py:289-291`) and OVERWRITE the container: after
// `pc1[sysctls]=x` — which `LabParser` passes straight through, its `arg` class
// being `\w+` — `meta['sysctls']` is the string `"x"`, `str(machine)` dies with
// `AttributeError: 'str' object has no attribute 'items'` and the next
// `pc1[sysctl]=…` line dies with `TypeError: 'str' object does not support item
// assignment` (all oracle-verified).
//
// The typed [Meta] cannot hold a string in a container field, so the port keeps
// the container intact and stores the value in [Meta.Extras] under the same
// name. What IS reproduced is the return: the container was always present, so
// the first such line reports a previous value and `LabParser` prints its
// duplicate-meta warning, and a second one reports the string the first stored.
// DIVERGENCES.md 42 records the residue — 3.8.3 crashes at the first use of the
// poisoned container, the port carries on with the container it started with.
func (m *Machine) addContainerNameMeta(name, value string) (any, bool, error) {
	prev, existed := m.Meta.Extras.Set(name, Str(value))
	if existed {
		return prev.Value(), true, nil
	}
	return m.Meta.containerValue(name), true, nil
}

// containerValue is the container `add_meta` would have replaced, as the
// previous value Python returns for it.
func (m *Meta) containerValue(name string) any {
	switch name {
	case "exec_commands":
		return m.ExecCommands
	case "sysctls":
		return m.Sysctls
	case "envs":
		return m.Envs
	case "ports":
		return m.Ports
	case "ulimits":
		return m.Ulimits
	case "volumes":
		return m.Volumes
	}
	return nil
}

// addBoolMeta is the `privileged` and `bridged` branches, which are the same
// code twice in Python (`model/Machine.py:157-169`).
//
// The `strtobool` failure escapes as a bare ValueError — it is NOT wrapped in a
// MachineOptionError, unlike the `ipv6` path, so `pc1[privileged]=maybe` dies
// with no file, no line and no device name (vector `labconf/strtobool_invalid`).
func (m *Machine) addBoolMeta(field **bool, value string) (any, bool, error) {
	parsed, err := util.StrToBool(value)
	if err != nil {
		return nil, false, kerrors.WrapValue(err, err.Error())
	}

	var prev any
	existed := *field != nil
	if existed {
		prev = **field
	}
	*field = &parsed
	return prev, existed, nil
}

// addSysctlMeta is `model/Machine.py:171-188`.
func (m *Machine) addSysctlMeta(value string) (any, bool, error) {
	matches := sysctlRegex.FindStringSubmatch(value)
	if matches == nil {
		return nil, false, kerrors.NewOptionSysctl(m.Name, value)
	}

	key := pyStrip(matches[1])
	val := pyStrip(matches[2])

	prev, existed := m.Meta.Sysctls.Get(key)

	// `int(val) if val.lstrip('-').isnumeric() else val`. The lstrip removes
	// EVERY leading dash before the test, so `--5` reaches int() and raises —
	// a bare, uncaught ValueError, which is the behaviour to reproduce.
	stored := Str(val)
	if pyIsNumeric(strings.TrimLeft(val, "-")) {
		parsed, err := pyBigInt(val)
		if err != nil {
			if errors.Is(err, util.ErrPyIntSyntax) {
				failure := util.PyIntFailure(util.ErrPyIntSyntax, val)
				return nil, false, kerrors.WrapValue(failure, failure.Error())
			}
			return nil, false, err
		}
		stored = Int(saturateInt64(parsed))
	}

	m.Meta.Sysctls.Set(key, stored)
	return prev.Value(), existed, nil
}

// addEnvMeta is `model/Machine.py:190-203`.
func (m *Machine) addEnvMeta(value string) (any, bool, error) {
	matches := envRegex.FindStringSubmatch(value)
	if matches == nil {
		return nil, false, kerrors.NewOptionEnv(m.Name, value)
	}

	key := pyStrip(matches[1])
	val := pyStrip(matches[2])

	prev, existed := m.Meta.Envs.Set(key, val)
	if !existed {
		return nil, false, nil
	}
	return prev, true, nil
}

// addUlimitMeta is `model/Machine.py:205-239`.
//
// The three error messages name the *option* and not the device: `add_meta`
// interpolates its own `name` parameter, which is the literal string "ulimit"
// (DIVERGENCES.md 2, ERROR_CODES.md §0.2). kerrors renders that; the machine
// name is passed only for the JSON field.
func (m *Machine) addUlimitMeta(value string) (any, bool, error) {
	matches := ulimitRegex.FindStringSubmatch(value)
	if matches == nil {
		return nil, false, kerrors.NewOptionUlimit(m.Name, value)
	}

	key := pyStrip(matches[1])
	// The regex guarantees an optional sign followed by decimal digits, so
	// neither parse can fail; the width can exceed a Go int, which is why both
	// are big.
	soft, err := pyBigInt(pyStrip(matches[2]))
	if err != nil {
		return nil, false, err
	}
	hard := soft
	if matches[3] != "" {
		if hard, err = pyBigInt(pyStrip(matches[3])); err != nil {
			return nil, false, err
		}
	}

	minusOne := big.NewInt(-1)
	if soft.Cmp(minusOne) < 0 || hard.Cmp(minusOne) < 0 {
		return nil, false, kerrors.NewOptionUlimitRange(m.Name, value)
	}
	if soft.Cmp(minusOne) == 0 && hard.Cmp(minusOne) != 0 {
		// The rendered hard limit is Python's `str(int(group))`, so `-1:007`
		// reports 7 and `-1:٣` reports 3.
		return nil, false, kerrors.NewOptionUlimitSoftHard(m.Name, value, hard.String())
	}
	// A finite soft limit under an unlimited hard limit is left alone; any
	// other soft limit above the hard one is silently clamped down to it.
	if !(soft.Cmp(minusOne) != 0 && hard.Cmp(minusOne) == 0) && soft.Cmp(hard) > 0 {
		soft = hard
	}

	prev, existed := m.Meta.Ulimits.Set(key, Ulimit{
		Soft: saturateInt64(soft),
		Hard: saturateInt64(hard),
	})
	if !existed {
		return nil, false, nil
	}
	return prev, true, nil
}

// addPortMeta is `model/Machine.py:241-264`.
//
// Two of its failures are uncaught tuple unpacks — a second `/` or a second `:`
// raises `ValueError: too many values to unpack (expected 2)` with no device
// name and no file (vectors `labconf/port_two_slashes`,
// `labconf/port_two_colons`) — while a non-numeric port is wrapped in a
// MachineOptionError. Both are reproduced.
func (m *Machine) addPortMeta(value string) (any, bool, error) {
	ports, protocol := value, "tcp"
	if strings.Contains(value, "/") {
		parts := strings.Split(value, "/")
		if len(parts) != 2 {
			return nil, false, errTooManyValuesToUnpack
		}
		ports, protocol = parts[0], parts[1]
	}

	hostPort := "3000"
	guestPort := ports
	if strings.Contains(ports, ":") {
		parts := strings.Split(ports, ":")
		if len(parts) != 2 {
			return nil, false, errTooManyValuesToUnpack
		}
		hostPort, guestPort = parts[0], parts[1]
	}

	// strings.ToLower rather than Python's full `str.lower()`: the folded value
	// is compared against three ASCII words and is never interpolated into a
	// message, and no string folds onto "tcp", "udp" or "sctp" under one
	// mapping and not the other.
	protocol = strings.ToLower(protocol)
	if !slices.Contains(allowedPortProtocols, protocol) {
		return nil, false, kerrors.NewOptionPortProtocol(m.Name)
	}

	host, err := pyBigInt(hostPort)
	if err != nil {
		return nil, false, kerrors.NewOptionPortValue(m.Name)
	}
	guest, err := pyBigInt(guestPort)
	if err != nil {
		return nil, false, kerrors.NewOptionPortValue(m.Name)
	}

	key := PortKey{HostPort: saturateInt(host), Protocol: protocol}
	prev, existed := m.Meta.Ports.Set(key, saturateInt(guest))
	if !existed {
		return nil, false, nil
	}
	return prev, true, nil
}

// addVolumeMeta is `model/Machine.py:266-287`.
func (m *Machine) addVolumeMeta(value string) (any, bool, error) {
	// `list(filter(lambda x: x, value.split('|')))`: empty segments vanish
	// before the count is taken, so `/a||/b` and `|/a|/b` are both two-part
	// volumes (vector `labconf/volume_empty_segments`).
	var values []string
	for _, part := range strings.Split(value, "|") {
		if part != "" {
			values = append(values, part)
		}
	}

	var hostPath, guestPath, mode string
	switch len(values) {
	case 3:
		hostPath, guestPath, mode = values[0], values[1], values[2]
	case 2:
		hostPath, guestPath, mode = values[0], values[1], "ro"
	default:
		return nil, false, kerrors.NewOptionVolumeFormat(m.Name, value)
	}

	if !slices.Contains(allowedVolumeModes, mode) {
		return nil, false, kerrors.NewOptionVolumeMode(m.Name, mode, hostPath)
	}

	prev, existed := m.Meta.Volumes.Set(absPath(hostPath), Volume{GuestPath: guestPath, Mode: mode})
	if !existed {
		return nil, false, nil
	}
	return prev, true, nil
}

// absPath is `os.path.abspath`: join against the process working directory,
// then normalise. It is deliberately not [util.GetAbsolutePath], which is
// `utils.get_absolute_path` and additionally resolves symlinks — the volume key
// is the un-resolved path in Python, and it is what the bind mount is created
// from.
//
// A failure to read the working directory leaves the path as given, which is
// what Python would do only if `os.getcwd()` itself failed (it raises there;
// the difference is unreachable outside a deleted CWD).
//
// [filepath.Abs] is `ntpath.abspath` enough on Windows, but on POSIX it is one
// case short of `posixpath.abspath`: `posixpath.normpath` PRESERVES a leading
// `//` when there are exactly two slashes — POSIX leaves that prefix
// implementation-defined — and collapses three or more to one, while
// [filepath.Clean] collapses every run. The difference is observable in the
// volume keys, which is where the bind mounts are created from: the oracle
// records `//data` for `--volume //data|/g`, and [filepath.Abs] alone gives
// `/data`. Oracle-verified: `//data` → `//data`, `//a//b` → `//a/b`,
// `//data/../e` → `//e`, `//` → `//`, `///data` → `/data`.
//
// Only an absolute argument can carry the prefix: a relative one is joined
// onto `os.getcwd()` first, and no working directory starts with `//`.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if runtime.GOOS != "windows" && strings.HasPrefix(p, "//") && !strings.HasPrefix(p, "///") {
		return "/" + abs
	}
	return abs
}

// errTooManyValuesToUnpack is the bare ValueError of the two port unpacks. The
// message is CPython's own, which the vector corpus records verbatim.
var errTooManyValuesToUnpack = kerrors.NewValue("too many values to unpack (expected 2)")

// MetaOptions is `Machine.update_meta`'s `args` dict (`model/Machine.py:293`),
// i.e. the `**kwargs` of `Machine(...)`, `Lab.new_machine` and
// `Lab.get_or_new_machine`, and the flag set of `vstart`.
//
// Every field is a pointer or a slice, because Python's gate is `'x' in args
// and args['x'] is not None`: nil is "the caller did not pass it", and a set
// falsy value IS applied — `num_terms=0` and `shell=""` both land in the meta
// (oracle-verified).
//
// The scalar fields are strings because that is what argparse produces for
// every one of them (`cli/command/VstartCommand.py:50,85,90`), and because
// lab.conf stores strings too: keeping them strings is what makes a device
// built through the API and a device built from a file behave identically in
// the accessors.
type MetaOptions struct {
	// Privileged is applied only when true: `args['privileged'] is not None and
	// args['privileged']`. A false value leaves the meta *unset*, which is not
	// the same as setting it to false.
	Privileged *bool
	// ExecCommands are appended in slice order.
	ExecCommands []string
	// Mem is the `mem` meta, raw.
	Mem *string
	// CPUs is the `cpus` meta, raw.
	CPUs *string
	// Image is the `image` meta.
	Image *string
	// Bridged is applied only when true, like Privileged.
	Bridged *bool
	// Ports are `port` metas, parsed in slice order.
	Ports []string
	// NumTerms is the `num_terms` meta, raw.
	NumTerms *string
	// Sysctls are `sysctl` metas, parsed in slice order.
	Sysctls []string
	// Envs are `env` metas, parsed in slice order.
	Envs []string
	// Ulimits are `ulimit` metas, parsed in slice order.
	Ulimits []string
	// IPv6 is the `ipv6` meta. Unlike Privileged and Bridged it has no truthy
	// gate, so a false here IS recorded and overrides the settings default.
	IPv6 *bool
	// Shell is the `shell` meta.
	Shell *string
	// Entrypoint is the `entrypoint` meta.
	Entrypoint *string
	// Args is the `args` meta, stored as the list it arrives as.
	Args []string
	// Volumes are `volume` metas, parsed in slice order.
	Volumes []string
}

// UpdateMeta is `Machine.update_meta` (`model/Machine.py:293`).
//
// The statement order below is Python's, unchanged, and it is observable: when
// two options are both invalid, the one that appears first here is the error
// the user sees (ORDERING.tsv `model/Machine.py:293`). The oracle confirms it —
// `Machine(lab, "pc1", sysctls=["bad"], envs=["bad"])` reports the sysctl.
func (m *Machine) UpdateMeta(opts MetaOptions) error {
	if opts.Privileged != nil && *opts.Privileged {
		if _, _, err := m.AddMeta("privileged", "True"); err != nil {
			return err
		}
	}

	for _, command := range opts.ExecCommands {
		if _, _, err := m.AddMeta("exec", command); err != nil {
			return err
		}
	}

	if opts.Mem != nil {
		m.Meta.Mem = Str(*opts.Mem)
	}

	if opts.CPUs != nil {
		m.Meta.CPUs = Str(*opts.CPUs)
	}

	if opts.Image != nil {
		m.Meta.Image = Str(*opts.Image)
	}

	if opts.Bridged != nil && *opts.Bridged {
		if _, _, err := m.AddMeta("bridged", "True"); err != nil {
			return err
		}
	}

	for _, port := range opts.Ports {
		if _, _, err := m.AddMeta("port", port); err != nil {
			return err
		}
	}

	if opts.NumTerms != nil {
		m.Meta.NumTerms = Str(*opts.NumTerms)
	}

	for _, sysctl := range opts.Sysctls {
		if _, _, err := m.AddMeta("sysctl", sysctl); err != nil {
			return err
		}
	}

	for _, env := range opts.Envs {
		if _, _, err := m.AddMeta("env", env); err != nil {
			return err
		}
	}

	for _, ulimit := range opts.Ulimits {
		if _, _, err := m.AddMeta("ulimit", ulimit); err != nil {
			return err
		}
	}

	if opts.IPv6 != nil {
		// A real bool, not the string `add_meta` would store: this is the one
		// meta whose type differs between the API and lab.conf, and
		// `is_ipv6_enabled` can tell (DIVERGENCES.md 3).
		m.Meta.IPv6 = Bool(*opts.IPv6)
	}

	if opts.Shell != nil {
		m.Meta.Shell = Str(*opts.Shell)
	}

	if opts.Entrypoint != nil {
		m.Meta.Entrypoint = Str(*opts.Entrypoint)
	}

	if opts.Args != nil {
		m.Meta.Args = Strings(opts.Args)
	}

	for _, volume := range opts.Volumes {
		if _, _, err := m.AddMeta("volume", volume); err != nil {
			return err
		}
	}

	return nil
}
