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
//
//	^(?P<key>\w+):\s?(?P<deps>(\w+ ?)+)$
//
// One optional whitespace character after the colon and single spaces between
// device names. That is the entire grammar, and it is why `pc1:  pc2` (two
// spaces), `pc1 : pc2` (a space before the colon), `pc1: pc2,pc3` (a comma) and
// `pc1: pc2 # note` (a trailing comment) are all syntax errors.
//
// `\w` and `\s` are the Unicode classes of a Python `str` pattern, restated
// here because RE2's are ASCII: lab.dep device names may be non-ASCII, unlike
// lab.conf's (vector `labdep/unicode_names`).
//
// The pattern is applied to an already-stripped line, so no `\n?` tail is
// needed before `$` — the difference from Python's `$` cannot be reached.
var depLineRegex = regexp.MustCompile(
	`^([\p{L}\p{N}_]+):` + pySpaceClass + `?((?:[\p{L}\p{N}_]+ ?)+)$`)

// pySpaceClass is `\s` in a Python `str` pattern: CPython's
// `Py_UNICODE_ISSPACE`, which is [unicode.IsSpace] plus U+001C-U+001F.
const pySpaceClass = `[\t-\r\x1c-\x1f \x{85}\x{a0}\x{1680}\x{2000}-\x{200a}` +
	`\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}]`

// ParseDep is `DepParser.parse` (`parser/netkit/DepParser.py:15`): read
// `<path>/lab.dep` and flatten it into the order the devices must boot in.
//
// The three empty cases are distinguished, which Python can only do because it
// returns None (RULINGS.md, README SURPRISE 20):
//
//   - no lab.dep at all → nil, no error;
//   - a zero-byte lab.dep → nil, plus the `lab.dep file is empty. Ignoring...`
//     warning;
//   - a lab.dep with nothing but comments → an EMPTY, non-nil slice.
//
// Every caller in 3.8.3 tests truthiness, so all three behave alike today; the
// distinction is preserved so that a caller which needs it has it.
//
// A duplicate left-hand side REPLACES the previous dependency list and keeps its
// original position, exactly as `dependencies[key] = deps` does.
//
// Errors: [kerrors.ErrOS] when the file exists but cannot be read,
// [UnicodeDecodeError] for a line that is not UTF-8, [ParseError] for a line
// that does not match the grammar, and [kerrors.ErrDependencyLoop] for a cycle.
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
		// (README SURPRISE 9).
		line = pyStrip(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		matches := depLineRegex.FindStringSubmatch(line)
		if matches == nil {
			return nil, newDepSyntax(lineNumber)
		}

		// `matches.group("deps").split(" ")`. The strip ran BEFORE the match,
		// so the deps group can never end with a space and the empty-string
		// device name that parser-settings.md §1.2 predicts is unreachable
		// (README SURPRISE 2, vector `labdep/trailing_space_no_phantom`).
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
