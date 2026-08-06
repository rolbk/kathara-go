package model

import "strconv"

// ExternalLink is a host interface attached to a collision domain by a lab.ext
// file (`model/ExternalLink.py`).
//
// The feature is DEFERRED to post-1.0 (PORT_SPEC §0.3): the `netns` helpers,
// the privilege escalation and the `ExtParser` that fills this are not in 1.0,
// so nothing constructs one and [Lab.AttachExternalLinks] — the only model-side
// consumer — answers `FeatureNotAvailable` (ERROR_CODES.md §5,
// [kerrors.FeatureLabExt]).
//
// The type exists so that [Link.External] keeps its shape across the deferral
// (NILABILITY.tsv:22) and so the post-1.0 `labfile/ext.go` has somewhere to put
// its result. Its two methods — `get_name_and_vlan` and `get_full_name`, which
// truncate the interface name to Linux's IFNAMSIZ — belong with that work and
// are deliberately not ported here: they have no caller in 1.0, and porting
// them would mean shipping untested code for a feature that errors out first.
type ExternalLink struct {
	// Interface is the host interface name.
	Interface string
	// VLAN is the 802.1Q tag; 0 means "no VLAN", because Python tests it for
	// truthiness (NILABILITY.tsv:197, `model/ExternalLink.py:32`).
	VLAN int
}

// String is `ExternalLink.__repr__` (`model/ExternalLink.py:45`), which
// [Link.String] interpolates. A zero VLAN renders as Python's `None`, the value
// every caller of the deferred parser would have left it at; the tag 0 itself
// is unreachable, since Python treats it as no tag everywhere it is read.
func (e ExternalLink) String() string {
	vlan := "None"
	if e.VLAN != 0 {
		vlan = strconv.Itoa(e.VLAN)
	}
	return "ExternalLink(" + e.Interface + ", " + vlan + ")"
}
