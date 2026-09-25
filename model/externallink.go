package model

import "strconv"

// ExternalLink is a host interface attached to a collision domain by a lab.ext
// file (`model/ExternalLink.py`).
type ExternalLink struct {
	// Interface is the host interface name.
	Interface string

	VLAN int
}

// NameAndVLAN follows ExternalLink.get_name_and_vlan: it truncates the base
// to 15 Unicode characters including the VLAN suffix. The kernel may still
// reject a Unicode name whose UTF-8 encoding exceeds 15 bytes.
func (e ExternalLink) NameAndVLAN() (string, int) {
	if e.VLAN == 0 {
		return e.Interface, 0
	}
	suffix := "." + strconv.Itoa(e.VLAN)
	limit := 15 - len(suffix)
	if limit < 0 {
		limit = 0
	}
	runes := []rune(e.Interface)
	if len(runes) > limit {
		return string(runes[:limit]), e.VLAN
	}
	return e.Interface, e.VLAN
}

// FullName is the host-side interface name after optional VLAN truncation.
func (e ExternalLink) FullName() string {
	base, vlan := e.NameAndVLAN()
	if vlan == 0 {
		return base
	}
	return base + "." + strconv.Itoa(vlan)
}

// String is `ExternalLink.__repr__` (`model/ExternalLink.py:45`), which
// [Link.String] interpolates. A zero VLAN renders as Python's `None`.
func (e ExternalLink) String() string {
	vlan := "None"
	if e.VLAN != 0 {
		vlan = strconv.Itoa(e.VLAN)
	}
	return "ExternalLink(" + e.Interface + ", " + vlan + ")"
}
