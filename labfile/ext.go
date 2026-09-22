package labfile

import (
	"os"

	"github.com/KatharaFramework/kathara-go/kerrors"
)

// ExtName is the external-links file's fixed name.
const ExtName = "lab.ext"

// CheckExt reports whether the scenario directory declares external collision
// domains, which 1.0 does not support.
func CheckExt(path string) error {
	if _, err := os.Stat(joinPath(path, ExtName)); err != nil {
		return nil
	}
	return kerrors.NewFeatureNotAvailable(kerrors.FeatureLabExt)
}
