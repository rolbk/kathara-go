package labfile

import (
	"os"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ExtName is the external-links file's fixed name.
const ExtName = "lab.ext"

// CheckExt reports whether the scenario directory declares external collision
// domains, which 1.0 does not support.
//
// `ExtParser` is DEFERRED post-1.0 (PORT_SPEC §0.3, PACKAGE_GRAPH.md row
// `parser/netkit/ExtParser.py`), so there is nothing to parse: the presence of
// the file is itself the answer, and it is
// [kerrors.NewFeatureNotAvailable]([kerrors.FeatureLabExt]) — `lab.ext external
// links are not supported in this release. Use Kathará 3.8.x.`
//
// ERROR_CODES.md §5 requires this to be checked BEFORE any root or platform
// check, superseding the `You must be root in order to use lab.ext file.` and
// `lab.ext is only available on Linux systems.` of `LstartCommand.py:203,205`.
// A missing file is not an error and not a result — the scenario simply has no
// external links, which is what 3.8.3's `None` return means.
//
// Presence is `os.path.exists`, which is false for anything that cannot be
// stat'ed.
func CheckExt(path string) error {
	if _, err := os.Stat(joinPath(path, ExtName)); err != nil {
		return nil
	}
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
}
