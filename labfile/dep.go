package labfile

import (
	"log/slog"
	"os"
	"regexp"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// DepName is the dependency file's fixed name. Unlike lab.conf's, it is not
// configurable — and `Cannot open lab.dep file.` hard-codes it too.
const DepName = "lab.dep"

// depLineRegex is `DepParser.py:56`:
var depLineRegex = regexp.MustCompile(
	`^([\p{L}\p{N}_]+):` + pySpaceClass + `?((?:[\p{L}\p{N}_]+ ?)+)$`)

// pySpaceClass is `\s` in a Python `str` pattern: CPython's
// `Py_UNICODE_ISSPACE`, which is [unicode.IsSpace] plus U+001C-U+001F.
const pySpaceClass = `[\t-\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}` +
	`\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`

// ParseDep is `DepParser.parse` (`parser/netkit/DepParser.py:15`): read
// `<path>/lab.dep` and flatten it into the order the devices must boot in.
func ParseDep(path string) ([]string, error) {
	depPath := joinPath(path, DepName)

	info, err := os.Stat(depPath)
	if err != nil {
		return nil, nil
	}
	if info.Size() == 0 {
		slog.Warn("lab.dep file is empty. Ignoring...")
		return nil, nil
	}

	data, err := os.ReadFile(depPath)
	if err != nil {
		return nil, kerrors.NewOSCannotOpenLabDep(err)
	}

	dependencies := model.NewOrderedMap[string, []string]()

	for i, raw := range readLines(data) {
		lineNumber := i + 1

		line, err := decodeUTF8(raw)
		if err != nil {
			return nil, err
		}

		// The `#` test is on the STRIPPED line here, so an indented comment is
		// legal in lab.dep and illegal in lab.conf. Two files, two conventions
		// (compatibility note 9 in the vector README).
		line = pyStrip(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		matches := depLineRegex.FindStringSubmatch(line)
		if matches == nil {
			return nil, newDepSyntax(lineNumber)
		}

		parts := strings.Split(matches[2], " ")
		deps := make([]string, 0, len(parts))
		for _, dep := range parts {
			deps = append(deps, pyStrip(dep))
		}
		dependencies.Set(pyStrip(matches[1]), deps)
	}

	if HasLoop(dependencies) {
		return nil, kerrors.ErrLabDepLoop
	}

	return Flatten(dependencies), nil
}
