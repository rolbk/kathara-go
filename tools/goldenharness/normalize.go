package main

import (
	"crypto/md5" // #nosec G401 -- reproduces Kathara's utils.generate_urlsafe_hash, not a security primitive
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Tokens substituted into every recorded string. They are chosen so that no
// legitimate Kathara output can contain them.
const (
	TokLabDir      = "<LABDIR>"
	TokLabHash     = "<LABHASH>"
	TokUser        = "<USER>"
	TokHome        = "<HOME>"
	TokMAC         = "<MAC>"
	TokLinkLocal6  = "<LINKLOCAL6>"
	TokAnonVol     = "<ANONVOL>"
	TokAnonVolPath = "<ANONVOL_PATH>"
	TokDockerIP    = "<DOCKERIP>"
	TokDockerIP6   = "<DOCKERIP6>"
	TokContainerID = "<CID>"
	TokShortID     = "<CID12>"
)

var (
	// ANSI CSI and OSC sequences. Rich disables colour when stdout is not a
	// TTY, but the binary under test may not, so strip unconditionally.
	reANSICSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	reANSIOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	reANSIOne = regexp.MustCompile("\x1b[@-Z\\\\-_]")

	// A MAC-shaped token. The all-zero and broadcast addresses are never
	// scrubbed: they are constants, not identity. A match adjacent to a ':'
	// is the interior of a longer colon-hex run (an IPv6 address such as
	// 2001:db8:aa:bb:cc:dd:ee:ff) and is skipped: a real MAC is always
	// delimited by spaces, quotes or line boundaries.
	reMAC = regexp.MustCompile(`\b([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b`)

	// EUI-64-derived IPv6 link-local addresses. The kernel's default
	// addr_gen_mode derives fe80::(b0^02)b1:b2ff:feb3:b4b5 from the interface
	// MAC, so the ff:fe infix identifies exactly the MAC-derived (i.e. random
	// on the default path) addresses. A statically configured link-local such
	// as basic-ipv6's `fe80::1` does not match and stays a byte-exact
	// assertion.
	reLinkLocal6 = regexp.MustCompile(`\bfe80::[0-9a-fA-F]{1,4}:[0-9a-fA-F]{0,2}ff:fe[0-9a-fA-F]{2}:[0-9a-fA-F]{1,4}\b`)

	// Docker's default address pools, used for the bridged path. The address a
	// bridged device receives depends on allocation order across the daemon.
	reDockerIP = regexp.MustCompile(`\b(?:172\.(?:1[6-9]|2[0-9]|3[01])|192\.168)\.[0-9]{1,3}\.[0-9]{1,3}\b`)

	// Rich progress bars. U+2501 and U+2578..U+257B are the bar glyphs; the
	// panel borders use a different block (U+2500, U+2502, corners) and stay.
	reProgressBar = regexp.MustCompile("[━╸╹╺╻]")

	// Rich spinner frames. The braille block U+2800..U+28FF is only ever
	// produced by rich's spinner column, and which frame is on screen is a
	// function of elapsed time, so the glyph differs between two runs of the
	// same scenario.
	reSpinner = regexp.MustCompile(`[\x{2800}-\x{28FF}]`)

	// docker pull layer progress.
	reLayerProgress = regexp.MustCompile(`^[0-9a-f]{12}: `)
	rePullNoise     = regexp.MustCompile(`^(Digest: sha256:|Status: (Downloaded|Image is up to date)|Pull complete|Downloading|Extracting|Verifying Checksum|Waiting|Already exists)`)

	reHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// replacement is one ordered literal substitution.
type replacement struct {
	from string
	to   string
}

// Normalizer removes every known source of run-to-run variation from recorded
// text. Every rule is justified in NORMALIZATION.md.
type Normalizer struct {
	lits     []replacement
	keepMACs map[string]bool
	// keepLL6 holds the EUI-64 link-local addresses derived from explicitly
	// pinned MACs. Those are deterministic and survive scrubbing.
	keepLL6 map[string]bool
}

// NewNormalizer builds a normalizer. Literal replacements are applied
// longest-first so that a container name is tokenized before the lab hash it
// embeds.
func NewNormalizer() *Normalizer {
	return &Normalizer{keepMACs: map[string]bool{}, keepLL6: map[string]bool{}}
}

// AddLiteral registers a literal substitution. Empty or one-character sources
// are ignored to avoid catastrophic over-replacement.
func (n *Normalizer) AddLiteral(from, to string) {
	if len(from) < 2 {
		return
	}
	for _, r := range n.lits {
		if r.from == from {
			return
		}
	}
	n.lits = append(n.lits, replacement{from: from, to: to})
	sort.SliceStable(n.lits, func(i, j int) bool { return len(n.lits[i].from) > len(n.lits[j].from) })
}

// KeepMAC marks a MAC address as deterministic (explicitly configured through
// the kathara.mac_addr driver opt) so that it survives scrubbing. The EUI-64
// link-local address the kernel derives from it is equally deterministic and
// is kept as well.
func (n *Normalizer) KeepMAC(mac string) {
	if mac == "" {
		return
	}
	low := strings.ToLower(mac)
	n.keepMACs[low] = true
	if ll := eui64LinkLocal(low); ll != "" {
		n.keepLL6[ll] = true
	}
}

// eui64LinkLocal derives the RFC 4291 link-local address for a MAC, in the
// canonical RFC 5952 text form iproute2 prints. Returns "" on a malformed MAC.
func eui64LinkLocal(mac string) string {
	hw, err := net.ParseMAC(mac)
	if err != nil || len(hw) != 6 {
		return ""
	}
	ip := net.IP{0xfe, 0x80, 0, 0, 0, 0, 0, 0,
		hw[0] ^ 0x02, hw[1], hw[2], 0xff, 0xfe, hw[3], hw[4], hw[5]}
	return ip.String()
}

// ExplicitMACs returns the MACs registered with KeepMAC, sorted.
func (n *Normalizer) ExplicitMACs() []string {
	out := make([]string, 0, len(n.keepMACs))
	for m := range n.keepMACs {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Text normalizes a free-form string: ANSI stripped, literals tokenized,
// volatile identifiers scrubbed.
func (n *Normalizer) Text(s string) string {
	return n.substitute(StripANSI(s))
}

// substitute applies the literal tokens and the MAC / link-local scrubbing.
// Callers that need to inspect the rendered layout first (Lines and its
// log-record unwrapping) strip ANSI themselves and call this afterwards.
func (n *Normalizer) substitute(s string) string {
	for _, r := range n.lits {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	s = n.scrubMACs(s)
	s = n.scrubLinkLocal6(s)
	return s
}

// scrubMACs replaces every MAC that is neither a constant nor explicitly
// configured. See the MAC ruling recorded in NORMALIZATION.md. Matches
// directly adjacent to a ':' are the interior of a longer colon-hex run — an
// IPv6 address, never a MAC — and are left alone.
func (n *Normalizer) scrubMACs(s string) string {
	var b strings.Builder
	last := 0
	for _, loc := range reMAC.FindAllStringIndex(s, -1) {
		start, end := loc[0], loc[1]
		b.WriteString(s[last:start])
		last = end
		m := s[start:end]
		if (start > 0 && s[start-1] == ':') || (end < len(s) && s[end] == ':') {
			b.WriteString(m)
			continue
		}
		low := strings.ToLower(m)
		switch {
		case low == "00:00:00:00:00:00" || low == "ff:ff:ff:ff:ff:ff":
			b.WriteString(m)
		case n.keepMACs[low]:
			b.WriteString(low)
		default:
			b.WriteString(TokMAC)
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

// scrubLinkLocal6 replaces EUI-64-derived link-local addresses, except the
// ones derived from an explicitly pinned MAC, which are deterministic.
func (n *Normalizer) scrubLinkLocal6(s string) string {
	return reLinkLocal6.ReplaceAllStringFunc(s, func(m string) string {
		if n.keepLL6[strings.ToLower(m)] {
			return strings.ToLower(m)
		}
		return TokLinkLocal6
	})
}

// Lines normalizes a captured stream into a stable []string: CRLF folded, log
// records unwrapped to logical lines, progress bars and pull progress dropped,
// trailing whitespace trimmed, trailing blank lines removed. Unwrapping runs
// before token substitution: a host path wrapped across lines by rich could
// otherwise never match its literal.
func (n *Normalizer) Lines(s string) []string {
	s = StripANSI(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = UnwrapLogRecords(s, consoleWidth)
	s = n.substitute(s)
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		if reProgressBar.MatchString(line) || reSpinner.MatchString(line) {
			continue
		}
		if reLayerProgress.MatchString(line) || rePullNoise.MatchString(line) {
			continue
		}
		out = append(out, strings.TrimRightFunc(line, unicode.IsSpace))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// consoleWidth is the console width every child process runs under
// (COLUMNS=80, pinned in deterministicEnv). UnwrapLogRecords needs it to tell
// a hard-wrapped row from a word-folded one.
const consoleWidth = 80

var (
	// A rich log-record first line: the level column RichHandler renders is
	// padded to eight characters plus one separator space. Matching the exact
	// padded forms keeps lines like "ERROR: foo" from a startup script out.
	reLogLevel = regexp.MustCompile(`^(?:CRITICAL |ERROR    |WARNING  |INFO     |DEBUG    )`)
	// A continuation row: indented by exactly the level-column width.
	reLogCont = regexp.MustCompile(`^ {9}\S`)
)

// UnwrapLogRecords joins the wrapped continuation rows of rich log records
// back into one logical line per record. The wrap point of a record depends on
// the rendered length of everything in it — including host paths, which are
// only tokenized later — so the wrapped form is host-specific even though the
// message is not.
//
// The separator at each break is decided by how rich wraps: it folds at
// spaces, and hard-chops only a word that cannot fit within the fold width
// (console width minus the 9-column level gutter) on a row of its own. A break
// is therefore a chop — rejoined with no separator — only when the previous
// row is filled to the console width exactly *and* the fragments on either
// side of the break form a single word longer than the fold width. Every other
// break is a fold or an embedded newline, rejoined with a single space. The
// one remaining ambiguity is a fold that lands exactly on the console width
// with a next word longer than the fold width minus the row's last word: not
// observed in any Kathara message, and a misjoin there would surface as a
// visible golden diff, not as a silently deleted assertion.
func UnwrapLogRecords(s string, width int) string {
	foldWidth := width - 9
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if !reLogLevel.MatchString(line) {
			out = append(out, line)
			continue
		}
		lastRow := strings.TrimRightFunc(line, unicode.IsSpace)
		logical := lastRow
		for i+1 < len(lines) && reLogCont.MatchString(lines[i+1]) {
			i++
			content := strings.TrimRightFunc(lines[i][9:], unicode.IsSpace)
			sep := " "
			if len([]rune(lastRow)) >= width {
				tail := lastRow
				if idx := strings.LastIndex(lastRow, " "); idx >= 0 {
					tail = lastRow[idx+1:]
				}
				head := content
				if idx := strings.Index(content, " "); idx >= 0 {
					head = content[:idx]
				}
				if len([]rune(tail))+len([]rune(head)) > foldWidth {
					sep = ""
				}
			}
			logical += sep + content
			lastRow = "         " + content
		}
		out = append(out, logical)
	}
	return strings.Join(out, "\n")
}

// FileLines normalizes file content read from a device or from the host.
func (n *Normalizer) FileLines(s string) []string {
	s = n.Text(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, strings.TrimRightFunc(line, unicode.IsSpace))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// HostsLines normalizes /etc/hosts: on top of the usual rules, addresses from
// Docker's default pools are tokenized because the bridged device's address
// depends on daemon-wide allocation order.
func (n *Normalizer) HostsLines(s string) []string {
	lines := n.FileLines(s)
	for i, l := range lines {
		lines[i] = reDockerIP.ReplaceAllString(l, TokDockerIP)
	}
	return lines
}

// StripANSI removes CSI, OSC and single-character escape sequences.
func StripANSI(s string) string {
	s = reANSIOSC.ReplaceAllString(s, "")
	s = reANSICSI.ReplaceAllString(s, "")
	return reANSIOne.ReplaceAllString(s, "")
}

// volatileAddrKeys are dropped from `ip -j addr` output. Every one of them is
// either host-global (interface indices), namespace-scoped or time-derived.
var volatileAddrKeys = map[string]bool{
	"ifindex":             true,
	"link_index":          true,
	"link_netnsid":        true,
	"altnames":            true,
	"alt_names":           true,
	"valid_life_time":     true,
	"preferred_life_time": true,
	"parentbus":           true,
	"parentdev":           true,
}

// sortedAddrArrayKeys names the `ip -j addr` arrays whose observed order is
// not stable. Everything else (notably "flags") keeps kernel order, which is
// deterministic and therefore worth asserting.
var sortedAddrArrayKeys = map[string]bool{"addr_info": true}

// ScrubAddrJSON normalizes the decoded `ip -j addr` document: volatile keys
// removed, MACs and link-local addresses scrubbed, unstable arrays sorted.
func (n *Normalizer) ScrubAddrJSON(v any) any {
	return n.scrubAddrValue("", v)
}

func (n *Normalizer) scrubAddrValue(key string, v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if volatileAddrKeys[k] {
				continue
			}
			out[k] = n.scrubAddrValue(k, val)
		}
		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, n.scrubAddrValue(key, e))
		}
		if sortedAddrArrayKeys[key] {
			sortByCanonicalJSON(out)
		}
		return out
	case string:
		return n.Text(t)
	default:
		return v
	}
}

// sortByCanonicalJSON gives arrays a deterministic order without needing to
// know their element shape.
func sortByCanonicalJSON(a []any) {
	keys := make([]string, len(a))
	for i, e := range a {
		b, err := json.Marshal(e)
		if err != nil {
			keys[i] = ""
			continue
		}
		keys[i] = string(b)
	}
	idx := make([]int, len(a))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return keys[idx[i]] < keys[idx[j]] })
	orig := append([]any(nil), a...)
	for i, j := range idx {
		a[i] = orig[j]
	}
}

// SplitSortCSV splits a comma-joined driver-opt value and sorts it. Kathara
// builds com.docker.network.endpoint.sysctls by joining a Python set, whose
// iteration order is hash-randomized per process (ORDERING.tsv, "!!" row
// DockerMachine.py:470).
func SplitSortCSV(s string) []string {
	if s == "" {
		return []string{}
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// IsAnonymousVolumeName reports whether a Docker mount name is a daemon
// generated anonymous-volume id.
func IsAnonymousVolumeName(s string) bool { return reHex64.MatchString(s) }

// GenerateURLSafeHash reimplements Kathara's utils.generate_urlsafe_hash so
// the harness can re-derive lab hashes and container names independently of
// the binary under test.
func GenerateURLSafeHash(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r <= 0x7F {
			b.WriteRune(r)
		}
	}
	sum := md5.Sum([]byte(b.String())) // #nosec G401
	enc := base64.URLEncoding.EncodeToString(sum[:])
	enc = enc[:len(enc)-2] // Python slices off the two '=' pad characters
	enc = strings.ReplaceAll(enc, "-", "")
	return strings.ReplaceAll(enc, "_", "")
}

var reSlugStrip = regexp.MustCompile(`[^\w\s-]`)
var reSlugDash = regexp.MustCompile(`[-\s]+`)

// Slug reimplements Kathara's utils.slug (ASCII fold, strip, lowercase,
// collapse runs of dashes and whitespace).
func Slug(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r <= 0x7F {
			b.WriteRune(r)
		}
	}
	s := reSlugStrip.ReplaceAllString(b.String(), "")
	s = strings.ToLower(strings.TrimSpace(s))
	return reSlugDash.ReplaceAllString(s, "-")
}
