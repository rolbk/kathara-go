package model

import "strconv"

// ExternalLink is a host interface attached to a collision domain by a lab.ext
// file (`model/ExternalLink.py`).
type ExternalLink struct {
	// Interface is the host interface name.
	Interface string

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
