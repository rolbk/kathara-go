package labfile

import (
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/KatharaFramework/kathara-go/kerrors"
	"github.com/KatharaFramework/kathara-go/model"
)

// ExtName is the external-links file's fixed name.
const ExtName = "lab.ext"

// extLineRegex is ExtParser.parse's link/interface/VLAN grammar. Python's \w
// includes Unicode letters and numbers, not merely ASCII identifiers.
var extLineRegex = regexp.MustCompile(`^([\p{L}\p{N}_]+)` + pySpaceClass +
	`+([\p{L}\p{N}_]+)(\.\d+)?$`)

// ParseExt reads the optional lab.ext file. A nil map means it is absent or
// empty; an empty non-nil map means it contained only comments/blank lines.
func ParseExt(path string) (map[string][]model.ExternalLink, error) {
	extPath := joinPath(path, ExtName)
	info, err := os.Stat(extPath)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, kerrors.WrapOS(err, "Cannot open lab.ext file.")
	}
	if info.Size() == 0 {
		slog.Warn("lab.ext file is empty. Ignoring...")
		return nil, nil
	}
	data, err := os.ReadFile(extPath)
	if err != nil {
		return nil, kerrors.WrapOS(err, "Cannot open lab.ext file.")
	}

	external := make(map[string][]model.ExternalLink)
	for i, raw := range readLines(data) {
		line, err := decodeUTF8(raw)
		if err != nil {
			return nil, err
		}
		trimmed := pyStrip(line)
		matches := extLineRegex.FindStringSubmatch(trimmed)
		if matches != nil {
			vlan := 0
			if matches[3] != "" {
				vlan, err = strconv.Atoi(strings.TrimPrefix(matches[3], "."))
				if err != nil {
					return nil, kerrors.WrapValue(err, err.Error())
				}
				// The original parser admits zero but rejects 4095 and above.
				if vlan >= 4095 {
					return nil, kerrors.NewValue("In file lab.ext, line " + strconv.Itoa(i+1) +
						": VLAN ID must be in range [1, 4094].")
				}
			}
			external[matches[1]] = append(external[matches[1]], model.ExternalLink{
				Interface: matches[2], VLAN: vlan,
			})
			continue
		}
		// Unlike lab.dep, an indented comment is not a comment here: the
		// original checks startswith('#') on the unstripped line.
		if strings.HasPrefix(line, "#") || trimmed == "" {
			continue
		}
		return nil, kerrors.NewSyntax("In file lab.ext - Line " + strconv.Itoa(i+1) + ".")
	}
	return external, nil
}

// CheckExt validates the optional file without attaching anything to a lab.
func CheckExt(path string) error {
	_, err := ParseExt(path)
	return err
}
