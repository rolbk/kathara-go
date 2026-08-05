package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// fsTreeScript walks one in-container directory and prints a stable,
// tab-separated listing: kind, path, and either a sha256 (files) or a link
// target (symlinks). find output is sorted in the C locale inside the
// container so the listing never depends on readdir order.
const fsTreeScript = `root="$1"
[ -d "$root" ] || { echo "__MISSING__"; exit 0; }
cd "$root" || exit 3
find . -mindepth 1 | LC_ALL=C sort | while IFS= read -r f; do
  if [ -L "$f" ]; then printf 'l\t%s\t%s\n' "$f" "$(readlink "$f")";
  elif [ -d "$f" ]; then printf 'd\t%s\t\n' "$f";
  else printf 'f\t%s\t%s\n' "$f" "$(sha256sum "$f" 2>/dev/null | cut -d' ' -f1)"; fi
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

	if sc.HasProbe(ProbeLink) {
		if s, ok := record(ProbeLink, p.docker.Exec(ctx, containerID, "ip", "-br", "link")); ok {
			out.IPBrLink = p.normalizeBrLink(s)
		}
	}

	if sc.HasProbe(ProbeAddr) {
		if s, ok := record(ProbeAddr, p.docker.Exec(ctx, containerID, "ip", "-j", "addr")); ok {
			addrs, err := p.normalizeAddrJSON(s)
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
// pinned by the lab, then sorts the lines. Interface order in the kernel dump
// follows ifindex, which is host-global.
func (p *Prober) normalizeBrLink(s string) []string {
	lines := p.norm.FileLines(s)
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	sort.Strings(lines)
	return lines
}

// normalizeAddrJSON decodes `ip -j addr`, strips volatile keys and sorts.
func (p *Prober) normalizeAddrJSON(s string) ([]map[string]any, error) {
	var doc []any
	if err := json.Unmarshal([]byte(s), &doc); err != nil {
		return nil, fmt.Errorf("decode ip -j addr: %w", err)
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
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		e := FSEntry{Kind: parts[0], Path: p.norm.Text(parts[1])}
		if len(parts) == 3 {
			v := p.norm.Text(strings.TrimSpace(parts[2]))
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
