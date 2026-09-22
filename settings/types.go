// This file implements `types.py`, which holds exactly one type.

package settings

// SharedCollisionDomains is `types.SharedCollisionDomainsOption`, the value of
// the `shared_cds` key: how far a collision domain reaches outside the network
// scenario that declared it.
type SharedCollisionDomains int

const (
	// NotShared keeps collision domains private to one network scenario.
	NotShared SharedCollisionDomains = 1
	// SharedBetweenLabs shares them across the network scenarios of one user.
	SharedBetweenLabs SharedCollisionDomains = 2
	// SharedBetweenUsers shares them across users too.
	SharedBetweenUsers SharedCollisionDomains = 3
)

// ToString is `SharedCollisionDomainsOption.to_string`, the label the settings
// screen shows for the current value.
func (o SharedCollisionDomains) ToString() string {
	switch o {
	case NotShared:
		return "Not Shared"
	case SharedBetweenLabs:
		return "Share collision domains between network scenarios"
	case SharedBetweenUsers:
		return "Share collision domains between users"
	}
	return ""
}
