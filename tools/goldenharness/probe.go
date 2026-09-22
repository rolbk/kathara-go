package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// fsTreeScript walks one in-container directory and prints a stable,
// tab-separated listing: kind, path, permission bits, owner uid, owner gid,
// and either a sha256 (files) or a link target (symlinks). find output is
// sorted in the C locale inside the container so the listing never depends on
// readdir order.
const fsTreeScript = `root="$1"
[ -d "$root" ] || { echo "__MISSING__"; exit 0; }
cd "$root" || exit 3
find . -mindepth 1 | LC_ALL=C sort | while IFS= read -r f; do
  m="$(stat -c '%a	%u	%g' "$f" 2>/dev/null)"
  case "$m" in *"	"*"	"*) ;; *) m="		" ;; esac
  if [ -L "$f" ]; then printf 'l\t%s\t%s\t%s\n' "$f" "$m" "$(readlink "$f")";
  elif [ -d "$f" ]; then printf 'd\t%s\t%s\t\n' "$f" "$m";
  else printf 'f\t%s\t%s\t%s\n' "$f" "$m" "$(sha256sum "$f" 2>/dev/null | cut -d' ' -f1)"; fi
done`

// fileProbeScript reports existence, sha256 and content of one path.
const fileProbeScript = `p="$1"
if [ -e "$p" ]; then
  printf '__EXISTS__\n'
  sha256sum "$p" 2>/dev/null | cut -d' ' -f1
  printf '__CONTENT__\n'
  cat "$p" 2>/dev/null
else
  printf '__ABSENT__\n'
fi`

// Prober collects the in-container half of a snapshot.
type Prober struct {
	docker *Docker
	norm   *Normalizer
}

// NewProber returns a prober bound to a Docker driver and a normalizer.
func NewProber(d *Docker, n *Normalizer) *Prober { return &Prober{docker: d, norm: n} }

// Probe runs the requested probe set against one container.
func (p *Prober) Probe(ctx context.Context, device, containerID string, sc *Scenario) DeviceProbes {
	out := DeviceProbes{Device: device}
	errs := map[string]string{}

	record := func(name string, res CmdResult) (string, bool) {
		if res.Err != nil {
			errs[name] = res.Err.Error()
			return "", false
		}
		if res.ExitCode != 0 {
			errs[name] = fmt.Sprintf("exit %d: %s", res.ExitCode,
				strings.TrimSpace(p.norm.Text(res.Stderr)))
			return "", false
		}
		return res.Stdout, true
	}

	// Interfaces whose MAC the kernel derived from another interface's random
	// address; their address is recorded as a token that asserts presence but
	// not identity (NORMALIZATION.md section 3, `volatile_macs`).
	derived := sc.VolatileMACSet(device)

	if sc.HasProbe(ProbeLink) {
		if s, ok := record(ProbeLink, p.docker.Exec(ctx, containerID, "ip", "-br", "link")); ok {
			out.IPBrLink = p.normalizeBrLink(s, derived)
		}
	}

	if sc.HasProbe(ProbeAddr) {
		if s, ok := record(ProbeAddr, p.docker.Exec(ctx, containerID, "ip", "-j", "addr")); ok {
			addrs, err := p.normalizeAddrJSON(s, derived)
			if err != nil {
				errs[ProbeAddr] = err.Error()
			} else {
				out.IPAddr = addrs
			}
		}
	}

	if sc.HasProbe(ProbeRoute) {
		if s, ok := record(ProbeRoute, p.docker.Exec(ctx, containerID, "ip", "route")); ok {
			lines := p.norm.FileLines(s)
			sort.Strings(lines)
			out.IPRoute = lines
		}
	}

	if sc.HasProbe(ProbeHosts) {
		if s, ok := record(ProbeHosts, p.docker.Exec(ctx, containerID, "cat", "/etc/hosts")); ok {
			out.EtcHosts = p.norm.HostsLines(s)
		}
	}

	if sc.HasProbe(ProbeHostname) {
		if s, ok := record(ProbeHostname, p.docker.Exec(ctx, containerID, "hostname")); ok {
			out.Hostname = strings.TrimSpace(p.norm.Text(s))
		}
	}

	if sc.HasProbe(ProbeFSTree) {
		out.FSTrees = map[string][]FSEntry{}
		for _, root := range sc.FSRootSet() {
			name := ProbeFSTree + ":" + root
			res := p.docker.Exec(ctx, containerID, "sh", "-c", fsTreeScript, "sh", root)
			s, ok := record(name, res)
			if !ok {
				continue
			}
			if strings.Contains(s, "__MISSING__") {
				continue
			}
			out.FSTrees[root] = p.parseFSTree(s)
		}
		if len(out.FSTrees) == 0 {
			out.FSTrees = nil
		}
	}

	if sc.HasProbe(ProbeStartupLog) {
		out.StartupLog = map[string][]string{}
		for _, path := range []string{"/var/log/startup.log", "/var/log/shared.log"} {
			res := p.docker.Exec(ctx, containerID, "sh", "-c", fileProbeScript, "sh", path)
			s, ok := record(ProbeStartupLog+":"+path, res)
			if !ok {
				continue
			}
			fp := p.parseFileProbe(path, s)
			if fp.Exists {
				out.StartupLog[path] = fp.Content
			}
		}
		if len(out.StartupLog) == 0 {
			out.StartupLog = nil
		}
	}

	if sc.HasProbe(ProbeFiles) {
		for _, path := range sc.DeviceFiles {
			res := p.docker.Exec(ctx, containerID, "sh", "-c", fileProbeScript, "sh", path)
			s, ok := record(ProbeFiles+":"+path, res)
			if !ok {
				continue
			}
			out.Files = append(out.Files, p.parseFileProbe(path, s))
		}
		sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	}

	if len(errs) > 0 {
		out.Errors = errs
	}
	return out
}

// normalizeBrLink scrubs the MAC column of `ip -br link` unless the address was
// pinned by the lab, tokenizes the veth peer-ifindex suffix, then sorts the
// lines. Interface order in the kernel dump follows ifindex, which is
// host-global, and so is the peer index rendered as `eth1@if451`.
func (p *Prober) normalizeBrLink(s string, derived map[string]bool) []string {
	raw := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range raw {
		fields := strings.Fields(l)
		if len(fields) == 0 {
			continue
		}
		// `ip -br link` renders a cross-namespace veth as `eth1@if451`; the
		// manifest names the interface, not the peer suffix.
		name, _, _ := strings.Cut(fields[0], "@")
		if derived[name] {
			raw[i] = maskMACFields(fields)
		}
	}
	lines := p.norm.FileLines(strings.Join(raw, "\n"))
	for i, l := range lines {
		lines[i] = p.norm.ScrubVethPeerIfIndex(strings.Join(strings.Fields(l), " "))
	}
	sort.Strings(lines)
	return lines
}

// maskMACFields replaces every MAC-shaped field of one `ip -br link` row with
// TokDerivedMAC and rejoins the row.
func maskMACFields(fields []string) string {
	out := make([]string, len(fields))
	for i, f := range fields {
		if reMAC.MatchString(f) {
			out[i] = TokDerivedMAC
			continue
		}
		out[i] = f
	}
	return strings.Join(out, " ")
}

// normalizeAddrJSON decodes `ip -j addr` and strips the volatile keys. The
// `address` of an interface in `derived` is replaced before the scrub, for the
// reason given on normalizeBrLink.
func (p *Prober) normalizeAddrJSON(s string, derived map[string]bool) ([]map[string]any, error) {
	var doc []any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, fmt.Errorf("decode ip -j addr: %w", err)
	}
	if len(derived) > 0 {
		for _, e := range doc {
			m, ok := e.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["ifname"].(string)
			if !derived[name] {
				continue
			}
			if _, has := m["address"]; has {
				m["address"] = TokDerivedMAC
			}
		}
	}
	scrubbed, ok := p.norm.ScrubAddrJSON(doc).([]any)
	if !ok {
		return nil, fmt.Errorf("unexpected ip -j addr shape")
	}
	out := make([]map[string]any, 0, len(scrubbed))
	for _, e := range scrubbed {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("unexpected ip -j addr element shape")
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return fmt.Sprint(out[i]["ifname"]) < fmt.Sprint(out[j]["ifname"])
	})
	return out, nil
}

func (p *Prober) parseFSTree(s string) []FSEntry {
	var out []FSEntry
	for _, line := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if line == "" {
			continue
		}
		// kind \t path \t mode \t uid \t gid \t (sha256 | link target)
		parts := strings.SplitN(line, "\t", 6)
		if len(parts) < 5 {
			continue
		}
		e := FSEntry{
			Kind: parts[0],
			Path: p.norm.Text(parts[1]),
			Mode: parts[2],
			UID:  parts[3],
			GID:  parts[4],
		}
		if len(parts) == 6 {
			v := p.norm.Text(strings.TrimSpace(parts[5]))
			switch e.Kind {
			case "l":
				e.Target = v
			case "f":
				e.SHA256 = v
			}
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if out == nil {
		// An empty directory is a fact worth recording as [], not as null.
		out = []FSEntry{}
	}
	return out
}

func (p *Prober) parseFileProbe(path, s string) FileProbeRecord {
	rec := FileProbeRecord{Path: path}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if strings.HasPrefix(s, "__ABSENT__") {
		return rec
	}
	rec.Exists = true
	rest := strings.TrimPrefix(s, "__EXISTS__\n")
	head, body, found := strings.Cut(rest, "__CONTENT__\n")
	if !found {
		return rec
	}
	rec.SHA256 = strings.TrimSpace(head)
	rec.Content = p.norm.FileLines(body)
	return rec
}
