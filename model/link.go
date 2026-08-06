package model

import "strings"

// BridgeLinkName is `BRIDGE_LINK_NAME` (`model/Link.py:8`), the reserved
// collision-domain name the managers give the host bridge.
const BridgeLinkName = "kathara_host_bridge"

// Link is a Kathará collision domain (`model/Link.py`).
//
// It is a plain record plus one back-reference set: `machines` is maintained by
// [Machine.AddInterface], [Machine.RemoveInterface] and [Lab.RemoveMachine],
// never by the Link itself, exactly as in Python.
type Link struct {
	// Lab is the scenario the collision domain belongs to. It is never nil for
	// a Link built by [Lab.NewLink] or [Lab.GetOrNewLink].
	Lab *Lab

	// Name is the collision-domain name. lab.conf allows far more here than in
	// a device name — Unicode `^\w+$`, so `UPPER`, `_leading`, `123` and
	// `shared` are all legal (labfile vectors `cd_name_charset`,
	// `cd_named_shared`) — and the model itself validates nothing.
	Name string

	// External is `Link.external`, the host interfaces attached to this
	// collision domain by a lab.ext file.
	//
	// The feature is deferred to post-1.0 (PORT_SPEC §0.3), so nothing in 1.0
	// fills this: [Lab.AttachExternalLinks] answers
	// [kerrors.NewFeatureNotAvailable]. The field exists because
	// NILABILITY.tsv:22 requires the shape to survive the deferral.
	External []ExternalLink

	// APIObject is the backend handle, nil until the collision domain is
	// deployed. For [BridgeLinkName] it can legitimately stay nil even when
	// deployed (NILABILITY.tsv:21).
	APIObject any

	machines *OrderedMap[string, *Machine]
}

func newLink(lab *Lab, name string) *Link {
	return &Link{
		Lab:      lab,
		Name:     name,
		machines: NewOrderedMap[string, *Machine](),
	}
}

// Machines returns the devices attached to this collision domain, in attach
// order (ORDERING.tsv `model/Link.py:28`): the managers iterate it when wiring,
// and it is the DEVICES column of the topology listings.
func (l *Link) Machines() []*Machine { return l.machines.Values() }

// MachineNames returns the attached device names, in attach order.
func (l *Link) MachineNames() []string { return l.machines.Keys() }

// HasMachine reports whether the named device is attached. It is the
// `self.name in link.machines` guard of [Machine.AddInterface] and
// [Machine.RemoveInterface].
func (l *Link) HasMachine(name string) bool { return l.machines.Has(name) }

// GetMachine returns the attached device with this name.
func (l *Link) GetMachine(name string) (*Machine, bool) { return l.machines.Get(name) }

// String is `Link.__repr__` (`model/Link.py:31`), which is what a `%s` of a
// Link produces in Python — the class has no `__str__`.
func (l *Link) String() string {
	externals := make([]string, 0, len(l.External))
	for _, e := range l.External {
		externals = append(externals, e.String())
	}
	return "Link(" + l.Name + ", [" + strings.Join(externals, ", ") + "])"
}
