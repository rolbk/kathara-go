package main

import (
	"crypto/md5" // #nosec G401 -- reproduces Kathara's utils.generate_urlsafe_hash, not a security primitive
	"encoding/base64"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
)

// Tokens substituted into every recorded string. Chosen so that no legitimate
// Kathara output can contain them.
const (
	TokLabDir      = "<LABDIR>"
	TokLabHash     = "<LABHASH>"
	TokVlabHash    = "<VLABHASH>"
	TokUser        = "<USER>"
	TokHome        = "<HOME>"
	TokMAC         = "<MAC>"
	TokContainerID = "<CID>"
	TokDockerIP    = "<DOCKERIP>"
	TokVolume      = "<VOL>"
	TokDuration    = "<DUR>"
	TokTimestamp   = "<TS>"
	TokStat        = "<STAT>"
	TokKathara     = "<KATHARA>"
)

// volatileColumns are the `list` table columns whose value changes between two
// runs of the same command: `DockerMachineStats.update` samples them off the
// live container (manager/docker/stats/DockerMachineStats.py:56-110).
var volatileColumns = map[string]bool{
	"PIDS": true, "CPU USAGE": true, "MEM USAGE": true,
	"MEM PERCENT": true, "NET USAGE": true,
}

var (
	reANSICSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	reANSIOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	reANSIOne = regexp.MustCompile("\x1b[@-Z\\\\-_]")

	reMAC   = regexp.MustCompile(`\b([0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b`)
	reHex64 = regexp.MustCompile(`\b[0-9a-f]{64}\b`)

	// Docker's default address pools.
	reDockerIP = regexp.MustCompile(`\b(?:172\.(?:1[6-9]|2[0-9]|3[01])|192\.168)\.[0-9]{1,3}\.[0-9]{1,3}\b`)

	// rich progress-bar glyphs and spinner frames.
	reProgressBar = regexp.MustCompile("[━╸╹╺╻]")
	reSpinner     = regexp.MustCompile(`[\x{2800}-\x{28FF}]`)

	// docker pull layer progress.
	reLayerProgress = regexp.MustCompile(`^[0-9a-f]{12}: `)
	rePullNoise     = regexp.MustCompile(`^(Digest: sha256:|Status: (Downloaded|Image is up to date)|Pull complete|Downloading|Extracting|Verifying Checksum|Waiting|Already exists)`)

	// The elapsed-time column rich prints next to a progress bar, and the
	// `TIMESTAMP: <datetime.now()>` title `create_lab_table` puts on the
	// `list` table (cli/ui/utils.py:68).
	reElapsed   = regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}\b`)
	reTimestamp = regexp.MustCompile(`TIMESTAMP: \d{4}-\d{2}-\d{2} [0-9:.]+`)
)

// StripANSI removes CSI, OSC and single-character escape sequences.
func StripANSI(s string) string {
	s = reANSIOSC.ReplaceAllString(s, "")
	s = reANSICSI.ReplaceAllString(s, "")
	s = reANSIOne.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, "\r", "")
}

type replacement struct{ from, to string }

// Normalizer removes every known source of run-to-run variation.
type Normalizer struct {
	lits []replacement
	// keepMACs holds the MAC addresses a flow configured explicitly. Those are
	// deterministic — they reach the daemon as the `kathara.mac_addr` driver
	// opt — so scrubbing them would delete the assertion that `--eth N:CD/MAC`
	// actually wired the address through.
	keepMACs map[string]bool
}

func NewNormalizer() *Normalizer { return &Normalizer{keepMACs: map[string]bool{}} }

// KeepMAC marks a MAC as explicitly configured, and therefore not volatile.
func (n *Normalizer) KeepMAC(mac string) {
	if mac != "" {
		n.keepMACs[strings.ToLower(mac)] = true
	}
}

// AddLiteral registers a literal substitution, applied longest-first.
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

// Text normalizes a free-form string.
func (n *Normalizer) Text(s string) string {
	return n.substitute(StripANSI(s))
}

func (n *Normalizer) substitute(s string) string {
	for _, r := range n.lits {
		s = strings.ReplaceAll(s, r.from, r.to)
	}
	s = reMAC.ReplaceAllStringFunc(s, func(m string) string {
		low := strings.ToLower(m)
		if n.keepMACs[low] || low == "00:00:00:00:00:00" || low == "ff:ff:ff:ff:ff:ff" {
			return low
		}
		return TokMAC
	})
	s = reHex64.ReplaceAllString(s, TokContainerID)
	s = reDockerIP.ReplaceAllString(s, TokDockerIP)
	// The table title is tokenized before the generic elapsed-time rule, which
	// would otherwise eat its clock component and leave the date behind.
	s = reTimestamp.ReplaceAllString(s, "TIMESTAMP: "+TokTimestamp)
	s = reElapsed.ReplaceAllString(s, TokDuration)
	return s
}

// reLogLevel matches the padded level column RichHandler renders, which is what
// tools/goldenharness NORMALIZATION.md §6.3 keys the unwrap off.
var reLogLevel = regexp.MustCompile(`^(CRITICAL|ERROR|WARNING|INFO|DEBUG)\s`)

func unwrapLogRecords(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	inRecord := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if inRecord && strings.HasPrefix(trimmed, "         ") && strings.TrimSpace(trimmed) != "" {
			out[len(out)-1] += " " + strings.TrimSpace(trimmed)
			continue
		}
		inRecord = reLogLevel.MatchString(trimmed)
		out = append(out, trimmed)
	}
	return strings.Join(out, "\n")
}

// Lines normalizes a stream into comparable lines: log records are unwrapped,
// progress-bar frames, spinner frames and docker-pull chatter are dropped,
// trailing whitespace is trimmed, and runs of blank lines are collapsed to one.
func (n *Normalizer) Lines(s string) []string {
	var out []string
	blank := false
	for _, raw := range strings.Split(n.substitute(unwrapLogRecords(StripANSI(s))), "\n") {
		line := reProgressBar.ReplaceAllString(raw, "")
		line = reSpinner.ReplaceAllString(line, "")
		line = strings.TrimRight(line, " \t")
		if reLayerProgress.MatchString(strings.TrimSpace(line)) || rePullNoise.MatchString(strings.TrimSpace(line)) {
			continue
		}
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	// Drop a single trailing blank produced by the final newline.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return maskTable(out)
}

// reBoxRun collapses a run of horizontal box-drawing glyphs to one, so that a
// table's *style* stays asserted while its column widths do not.
var reBoxRun = regexp.MustCompile(`[─═━]+`)

// maskTable rewrites the `list` table into a cell grid.
func maskTable(lines []string) []string {
	var header []string
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "TIMESTAMP: "+TokTimestamp) {
			// The title is centred, so its indent is a function of the title's
			// rendered length, which the tokenization has just changed.
			out = append(out, strings.TrimSpace(line))
			continue
		}
		if strings.Count(line, "│") < 3 {
			out = append(out, reBoxRunOnlyIfTableBorder(line))
			continue
		}
		cells := splitRow(line)
		if header == nil {
			header = cells
			out = append(out, "ROW "+strings.Join(cells, " | "))
			continue
		}
		masked := make([]string, len(cells))
		for i, c := range cells {
			if i < len(header) && volatileColumns[header[i]] {
				masked[i] = TokStat
				continue
			}
			masked[i] = c
		}
		out = append(out, "ROW "+strings.Join(masked, " | "))
	}
	return out
}

// reBoxRunOnlyIfTableBorder collapses horizontal runs only on lines that carry
// a table junction glyph, so panel borders — whose width is a function of the
// console width alone, and therefore stable — stay byte-exact.
func reBoxRunOnlyIfTableBorder(line string) string {
	if !strings.ContainsAny(line, "┬┼┴╤╪╧╥╫╨") {
		return line
	}
	return reBoxRun.ReplaceAllString(line, "─")
}

func splitRow(line string) []string {
	parts := strings.Split(line, "│")
	if len(parts) > 2 {
		parts = parts[1 : len(parts)-1]
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

// GenerateURLSafeHash is Kathara's utils.generate_urlsafe_hash: non-ASCII
// dropped, urlsafe base64 of the md5 with the two pad characters sliced off,
// then '-' and '_' deleted outright.
func GenerateURLSafeHash(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r <= 0x7F {
			b.WriteRune(r)
		}
	}
	sum := md5.Sum([]byte(b.String())) // #nosec G401
	enc := base64.URLEncoding.EncodeToString(sum[:])
	enc = enc[:len(enc)-2]
	enc = strings.ReplaceAll(enc, "-", "")
	return strings.ReplaceAll(enc, "_", "")
}

var reSlugStrip = regexp.MustCompile(`[^\w\s-]`)
var reSlugDash = regexp.MustCompile(`[-\s]+`)

// Slug is Kathara's utils.slug: ASCII fold, strip, lowercase, collapse runs of
// dashes and whitespace. The hyphen survives, which is why a container name
// reads `root-<hosthash>` and not `root_<hosthash>`.
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

// UserSlug re-derives Kathara's per-user container-name component.
func UserSlug() string {
	name := os.Getenv("USER")
	if u, err := user.Current(); err == nil && u.Username != "" {
		name = u.Username
	}
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	return Slug(name + "-" + GenerateURLSafeHash(host))
}
