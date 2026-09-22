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
	// its block size — and fails here, which is the third file-level message
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
		// one (compatibility note 9 in the vector README).
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
	// whitespace, so both calls are no-ops — kept because this implementation is a
	// statement-for-statement one and a future class change would need them.
	key := pyStrip(d.key)
	arg := pyStrip(d.arg)
	// This branch is unreachable on this path because the value class already
	// excludes quotes.
	value := stripQuotes(d.value)

	if util.IsReservedMachineName(key) {
		return newLabReservedName(confName, lineNumber, key)
	}

	number, err := interfaceNumber(arg)
	if err != nil {
		// `except ValueError:` — the arg is not a number, so it names a meta.
		_, existed, err := lab.AssignMetaToMachine(key, arg, value)
		if err != nil {
			return err
		}
		if existed {

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
		// (compatibility note 10 in the vector README). Trimming it here changes
		// the bytes.
		return newLabSyntax(confName, lineNumber, "`"+line+"`.")
	}

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
