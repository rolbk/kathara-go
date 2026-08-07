package labfile

import (
	"errors"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/internal/util"
	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// DefaultConfName is `LabParser.parse`'s default `conf_name`, i.e. the file
// `lstart` reads when `--config` is not given.
const DefaultConfName = "lab.conf"

// labMetadataPrefixes is `[f"{x}=" for x in LAB_METADATA]`, the six scenario
// keys as the raw line has to start for the metadata branch to be taken.
var labMetadataPrefixes = func() []string {
	prefixes := model.LabMetadata()
	for i, name := range prefixes {
		prefixes[i] = name + "="
	}
	return prefixes
}()

// ParseLab is `LabParser.parse` (`parser/netkit/LabParser.py:14`): read
// `<path>/<confName>` and build the network scenario it describes.
//
// confName is passed through literally, as Python passes it to
// `os.path.join`, and is interpolated into every file-level message — so
// `lstart --config custom.conf` on an empty file reports `custom.conf file is
// empty.` and not the default name. There is no "" spelling of the default;
// callers pass [DefaultConfName].
//
// `check_integrity` runs INSIDE the parse (`LabParser.py:92`), which is why a
// successfully parsed lab.conf can never produce a device with a hole in its
// interface numbering — and why a sparse device is a parse-time error rather
// than a deploy-time one (SYNTHESIS C-2, contradicting the PORT_SPEC §4.1
// struct comment).
//
// Errors, in the order they can occur:
//
//   - [kerrors.ErrOS] for the three file-level failures (missing, empty,
//     unopenable);
//   - [UnicodeDecodeError] for a line that is not UTF-8;
//   - [ParseError] for a malformed line or a reserved device name;
//   - whatever `model` raises for a rejected value — a
//     [kerrors.ErrMachineOption], a [kerrors.ErrMachineCollisionDomain], a
//     [kerrors.ErrInterfaceMacAddress], the bare [kerrors.ErrValue] of a
//     `strtobool` failure, or the [model.PyRuntimeError] a `bridged_iface`
//     produces;
//   - [kerrors.ErrNonSequentialMachineInterface] from the closing integrity
//     check.
func ParseLab(path, confName string, defaults model.Defaults) (*model.Lab, error) {
	confPath := joinPath(path, confName)

	// `os.path.exists` is false for anything that cannot be stat'ed, a
	// permission failure on the directory included.
	info, err := os.Stat(confPath)
	if err != nil {
		return nil, kerrors.NewOSNoConfInDirectory(confName)
	}
	if info.Size() == 0 {
		return nil, kerrors.NewOSConfEmpty(confName)
	}

	// `open(...)` then `mmap(...)`, with a bare `except Exception` over both.
	// A *directory* passes the two checks above — it exists and `st_size` is
	// its block size — and dies here, which is the third file-level message
	// (vector `labconf/conf_name_is_directory`).
	data, err := os.ReadFile(confPath)
	if err != nil {
		return nil, kerrors.NewOSCannotOpenConf(confName, err)
	}

	lab, err := model.NewLabFromPath(path, defaults)
	if err != nil {
		return nil, err
	}

	for i, raw := range readLines(data) {
		lineNumber := i + 1

		line, err := decodeUTF8(raw)
		if err != nil {
			return nil, err
		}

		stripped := pyStrip(line)
		if device, ok := matchDeviceLine(stripped); ok {
			if err := applyDeviceLine(lab, confName, lineNumber, device); err != nil {
				return nil, err
			}
			continue
		}

		// The `#` test is on the RAW line, so an indented comment is a syntax
		// error here — while lab.dep, which tests the stripped line, accepts
		// one (README SURPRISE 9).
		if strings.HasPrefix(line, "#") || stripped == "" {
			continue
		}

		if err := applyLabMetadata(lab, confName, lineNumber, line); err != nil {
			return nil, err
		}
	}

	if err := lab.CheckIntegrity(); err != nil {
		return nil, err
	}

	return lab, nil
}

// deviceLine is the three capture groups of the lab.conf device-line pattern:
// `pc1[eth0]=value`, where the middle group decides everything.
type deviceLine struct {
	key   string
	arg   string
	value string
}

// applyDeviceLine is `LabParser.py:50-78`: the body that runs once a line has
// matched the device pattern.
func applyDeviceLine(lab *model.Lab, confName string, lineNumber int, d deviceLine) error {
	// Both groups are stripped in Python. Neither class can contain
	// whitespace, so both calls are no-ops — kept because the port is a
	// statement-for-statement one and a future class change would need them.
	key := pyStrip(d.key)
	arg := pyStrip(d.arg)
	// Dead code on this path: the value class already excludes quotes.
	value := stripQuotes(d.value)

	if util.IsReservedMachineName(key) {
		return newLabReservedName(confName, lineNumber, key)
	}

	number, err := interfaceNumber(arg)
	if err != nil {
		// `except ValueError:` — the arg is not a number, so it names a meta.
		//
		// RULINGS.md OQ-14a: the dispatch is the integer parse and NOTHING
		// else. Python's `except ValueError` would also swallow a ValueError
		// raised inside the interface branch and silently reinterpret the line
		// as a meta assignment; nothing on that branch raises one today, and
		// routing an arbitrary error here would turn a future one into a
		// wrong-but-silent parse (README SURPRISE 26).
		_, existed, err := lab.AssignMetaToMachine(key, arg, value)
		if err != nil {
			return err
		}
		if existed {
			// `if ... is not None`. Every previous value `add_meta` can report
			// is non-None — the six containers included, which is why
			// `pc1[sysctls]=x` warns on its first occurrence — so presence is
			// the same test (NILABILITY.tsv:8).
			slog.Warn("In " + confName + " - Line " + strconv.Itoa(lineNumber) +
				": Device `" + key + "` already has a value assigned to meta `" + arg +
				"`. Previous value has been overwritten with `" + value + "`.")
		}
		return nil
	}

	cdName, mac, err := util.ParseCDMACAddress(value)
	if err != nil {
		// `except SyntaxError as e: raise SyntaxError(f"In ... : {str(e)}")` —
		// the inner message is re-wrapped, keeping its own trailing period.
		return newLabSyntax(confName, lineNumber, err.Error())
	}

	if !isCollisionDomainName(cdName) {
		// The message quotes the WHOLE value, not the collision-domain half,
		// which is what makes a swallowed comment surface as ``Collision domain
		// `A # comment` contains non-alphanumeric characters.``
		return newLabSyntax(confName, lineNumber,
			"Collision domain `"+value+"` contains non-alphanumeric characters.")
	}

	_, _, err = lab.ConnectMachineToLink(key, cdName, model.AddInterfaceOptions{
		Number: &number,
		MAC:    mac,
	})
	return err
}

// applyLabMetadata is `LabParser.py:82-87`: the branch for a line that is not a
// device line, not a comment and not blank.
func applyLabMetadata(lab *model.Lab, confName string, lineNumber int, line string) error {
	// `any([line.startswith(f"{x}=") for x in LAB_METADATA])` on the RAW line,
	// so an indented `LAB_NAME=` is rejected and the trailing newline is still
	// attached to the value below.
	known := false
	for _, prefix := range labMetadataPrefixes {
		if strings.HasPrefix(line, prefix) {
			known = true
			break
		}
	}
	if !known {
		// The raw line goes into the message INCLUDING its terminator, so the
		// text carries an embedded newline — and the CR too on a CRLF file
		// (README SURPRISE 10). Trimming it here changes the bytes.
		return newLabSyntax(confName, lineNumber, "`"+line+"`.")
	}

	// `(key, value) = line.split("=")`. A value containing a second `=` — a URL
	// with a query string, say — crashes with a bare ValueError carrying no
	// file and no line number (DIVERGENCES.md 5). The prefix test above
	// guarantees at least one `=`, so the short-sequence half is unreachable.
	parts := strings.Split(line, "=")
	if len(parts) != 2 {
		return errTooManyValues
	}

	// `key.replace("LAB_", "").lower()`, then `setattr`. key is one of the six
	// ASCII names the prefix test admitted, so the fold is ASCII.
	name := strings.ToLower(strings.ReplaceAll(parts[0], "LAB_", ""))
	value := pyStrip(stripQuotes(parts[1]))

	switch name {
	case "name":
		// The property setter, which also recomputes Lab.hash — the identity
		// every container of the scenario is then created under.
		lab.SetName(value)
	case "description":
		lab.Description = value
	case "version":
		lab.Version = value
	case "author":
		lab.Author = value
	case "email":
		lab.Email = value
	case "web":
		lab.Web = value
	}
	return nil
}

// interfaceNumber is `int(arg)`, the interface-versus-meta dispatch.
//
// [util.PyInt] is CPython's `int()`: Unicode decimal digits from any script and
// PEP 515 underscores strictly between digits, so `pc1[0_1]` is interface 1 and
// `pc1[٣]` is interface 3, while `pc1[_0]`, `pc1[0_]` and the superscript
// `pc1[²]` are metas (RULINGS.md OQ-14a). `strconv.Atoi` would reclassify all
// three of the first group.
//
// An out-of-range literal is still a number and stays on the interface path
// ([util.ErrPyIntRange]); Python keeps all twenty digits of
// `pc1[99999999999999999999]=A` and the port saturates, which reaches the same
// `Interface \`0\` missing` from `check_integrity` (DIVERGENCES.md 45, vector
// `labconf/interface_number_overflow`). The sign can only be positive here —
// the arg class is `\w+`, which has no `-` — but the negative branch is spelled
// out because [util.PyInt] is a general function.
//
// The saturation is NOT invisible once a device carries two out-of-range
// numbers: they collapse onto one slot, so `pc1[99999999999999999999]=A` next
// to `pc1[88888888888888888888]=B` is a collision here and a hole in the
// numbering there, and a genuinely repeated literal reports the saturated value
// where Python reports the digits it read. Both residues are recorded in
// DIVERGENCES.md 45 and pinned by `TestInterfaceNumberSaturationIsObservable`;
// closing them needs an arbitrary-precision interface key in `model`.
func interfaceNumber(arg string) (int, error) {
	number, err := util.PyInt(arg)
	if err == nil {
		return number, nil
	}
	if errors.Is(err, util.ErrPyIntRange) {
		if strings.ContainsRune(arg, '-') {
			return math.MinInt, nil
		}
		return math.MaxInt, nil
	}
	return 0, err
}

// isCollisionDomainName is `re.search(r"^\w+$", cd_name)` (`LabParser.py:67`).
//
// Collision domains are far more permissive than device names: any run of
// Unicode word characters, so `UPPER`, `_leading`, `123`, `shared` and a fully
// non-ASCII name are all legal (README SURPRISE 16). The trailing-newline
// tolerance is Python's `$`, which also matches immediately before one.
func isCollisionDomainName(name string) bool {
	name = strings.TrimSuffix(name, "\n")
	if name == "" {
		return false
	}
	for _, r := range name {
		if !isWordRune(r) {
			return false
		}
	}
	return true
}

// joinPath is `os.path.join(dir, name)` for the one property that differs from
// [filepath.Join]: an absolute name replaces the directory instead of being
// appended to it. Only `--config` can supply one.
func joinPath(dir, name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return filepath.Join(dir, name)
}
