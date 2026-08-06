// This file is the port of `types.py`, which holds exactly one type. It lands
// here rather than in the error package the spec's §3.1 row 1 pairs it with:
// it is a frozen settings-schema value consumed by `settings` and
// `backend/docker`, and `kathara` sits *above* `settings` in the import graph,
// so housing it there would invert an edge (PACKAGE_GRAPH.md D-2).

package settings

// SharedCollisionDomains is `types.SharedCollisionDomainsOption`, the value of
// the `shared_cds` key: how far a collision domain reaches outside the network
// scenario that declared it.
//
// It is an `IntEnum` in Python, which is why it serializes as a bare JSON int
// and not as a name — `json.dumps` sees an `int` subclass and writes `1`. The
// numbering is part of the frozen schema; do not renumber it.
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
//
// Python's staticmethod is a chain of `if`s with no `else`, so it returns
// `None` for any value outside 1-3; the empty string is that None. The value
// *can* be outside 1-3 — nothing validates `shared_cds` on load, and neither
// does Python — so this is a reachable answer and not a defensive default.
//
// It is deliberately not spelled `String()`: a [fmt.Stringer] that answers ""
// would make an out-of-range `shared_cds` print as nothing wherever the value
// reaches a format verb, and the on-disk form is the integer anyway.
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
