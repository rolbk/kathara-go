package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// pad simulates rich's row padding to the console width.
func pad(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func TestUnwrapLogRecordsFoldAndChop(t *testing.T) {
	// A real capture shape: CRITICAL record whose long path word is moved to a
	// continuation row and hard-chopped there (row filled to exactly 80).
	path := "'/root/kathara/kathara-go/test/scenarios/synthetic/this-directory-does-not-exist'"
	chopAt := consoleWidth - 9
	in := strings.Join([]string{
		pad("CRITICAL (CreateFailed) root path", consoleWidth),
		pad("         "+path[:chopAt], consoleWidth), // exactly full row
		pad("         "+path[chopAt:]+" does not exist", consoleWidth),
	}, "\n")
	want := "CRITICAL (CreateFailed) root path " + path + " does not exist"
	got := UnwrapLogRecords(in, consoleWidth)
	if got != want {
		t.Fatalf("unwrap fold+chop:\n got %q\nwant %q", got, want)
	}
}

func TestUnwrapLogRecordsFoldAtExactWidth(t *testing.T) {
	// syn-env's real WARNING: the fold lands exactly on column 80 after
	// "meta". The break is still a fold (the fragments around it, "meta" and
	// "`env`.", fit the fold width together), so the space must come back.
	row1 := "WARNING  In lab.conf - Line 5: Device `pc1` already has a value assigned to meta"
	if len(row1) != consoleWidth {
		t.Fatalf("test fixture drifted: row1 is %d columns, want %d", len(row1), consoleWidth)
	}
	in := row1 + "\n" + pad("         `env`. Previous value has been overwritten with `FOO=baz`.", consoleWidth)
	want := "WARNING  In lab.conf - Line 5: Device `pc1` already has a value assigned to meta `env`. Previous value has been overwritten with `FOO=baz`."
	if got := UnwrapLogRecords(in, consoleWidth); got != want {
		t.Fatalf("unwrap exact-width fold:\n got %q\nwant %q", got, want)
	}
}

func TestUnwrapLogRecordsEmbeddedNewline(t *testing.T) {
	// A message containing a literal newline renders as a short continuation
	// row; the unwrap normalizes it to a single space, same as a word fold.
	in := strings.Join([]string{
		pad("CRITICAL (SyntaxError) In lab.conf - Line 2: `not valid", consoleWidth),
		pad("         syntax", consoleWidth),
		pad("         `.", consoleWidth),
	}, "\n")
	want := "CRITICAL (SyntaxError) In lab.conf - Line 2: `not valid syntax `."
	if got := UnwrapLogRecords(in, consoleWidth); got != want {
		t.Fatalf("unwrap newline:\n got %q\nwant %q", got, want)
	}
}

func TestUnwrapLogRecordsLeavesPanelsAlone(t *testing.T) {
	in := "┌───┐\n│ x │\n└───┘\nplain\n         indented but no log record above"
	if got := UnwrapLogRecords(in, consoleWidth); got != in {
		t.Fatalf("unwrap touched non-log lines:\n got %q\nwant %q", got, in)
	}
	// "ERROR: ..." from a startup script is not the padded level column.
	in2 := "ERROR: something\n         nine spaces"
	if got := UnwrapLogRecords(in2, consoleWidth); got != in2 {
		t.Fatalf("unwrap misread a non-rich line:\n got %q\nwant %q", got, in2)
	}
}

func TestScrubMACs(t *testing.T) {
	n := NewNormalizer()
	n.KeepMAC("00:00:00:00:00:01")

	cases := []struct{ in, want string }{
		// A plain random MAC is tokenized, and the token is keyed: the first
		// distinct address a scenario scrubs is <MAC1>.
		{"eth0 UP aa:bb:cc:dd:ee:f0 <UP>", "eth0 UP <MAC1> <UP>"},
		// Constants carry no identity and take no ordinal.
		{"lo 00:00:00:00:00:00 x", "lo 00:00:00:00:00:00 x"},
		{"bc ff:ff:ff:ff:ff:ff x", "bc ff:ff:ff:ff:ff:ff x"},
		// Explicitly pinned MACs survive.
		{"eth0 UP 00:00:00:00:00:01 <UP>", "eth0 UP 00:00:00:00:00:01 <UP>"},
		// The interior of an IPv6 address is not a MAC.
		{"addr 2001:db8:aa:bb:cc:dd:ee:f0/64", "addr 2001:db8:aa:bb:cc:dd:ee:f0/64"},
		{"via fc00:12:34:56:78:9a:bc:de dev", "via fc00:12:34:56:78:9a:bc:de dev"},
	}
	for _, tc := range cases {
		if got := n.Text(tc.in); got != tc.want {
			t.Errorf("Text(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestScrubMACsKeyedTokens is the assertion the single blanket `<MAC>` deleted:
// which interface carries which address, whether two interfaces share one, and
// how many distinct addresses the scenario has. Under the old rule every line
// below rendered as "<MAC>" and a port that gave eth0 and eth1 the same address
// — or swapped them — passed.
func TestScrubMACsKeyedTokens(t *testing.T) {
	n := NewNormalizer()
	n.KeepMAC("00:00:00:00:00:09")

	in := []string{
		"eth0 UP aa:bb:cc:dd:ee:f0 <BROADCAST,UP>",
		"eth1 UP aa:bb:cc:dd:ee:f1 <BROADCAST,UP>",
		// The same address again: identity must survive.
		"neigh 10.0.0.1 dev eth0 lladdr aa:bb:cc:dd:ee:f0 REACHABLE",
		// A pinned address takes no ordinal at all.
		"eth2 UP 00:00:00:00:00:09 <BROADCAST,UP>",
		"eth3 UP aa:bb:cc:dd:ee:f2 <BROADCAST,UP>",
	}
	want := []string{
		"eth0 UP <MAC1> <BROADCAST,UP>",
		"eth1 UP <MAC2> <BROADCAST,UP>",
		"neigh 10.0.0.1 dev eth0 lladdr <MAC1> REACHABLE",
		"eth2 UP 00:00:00:00:00:09 <BROADCAST,UP>",
		"eth3 UP <MAC3> <BROADCAST,UP>",
	}
	for i, line := range in {
		if got := n.Text(line); got != want[i] {
			t.Errorf("Text(%q) = %q, want %q", line, got, want[i])
		}
	}
	if n.MACTokenCount() != 3 {
		t.Errorf("MACTokenCount() = %d, want 3", n.MACTokenCount())
	}

	// Two distinct addresses must never collapse onto one token.
	if n.Text("aa:bb:cc:dd:ee:f0") == n.Text("aa:bb:cc:dd:ee:f1") {
		t.Error("distinct MACs collapsed onto the same token")
	}
}

func TestScrubLinkLocal6(t *testing.T) {
	n := NewNormalizer()
	n.KeepMAC("00:00:00:00:00:01") // EUI-64: fe80::200:ff:fe00:1

	cases := map[string]string{
		// EUI-64-derived (random MAC) is scrubbed.
		"inet6 fe80::a8bb:ccff:fedd:eeff/64": "inet6 <LINKLOCAL6>/64",
		// Statically configured link-locals stay byte-exact.
		"inet6 fe80::1/64":     "inet6 fe80::1/64",
		"via fe80::2 dev eth1": "via fe80::2 dev eth1",
		// EUI-64 of a pinned MAC is deterministic and kept.
		"inet6 fe80::200:ff:fe00:1/64": "inet6 fe80::200:ff:fe00:1/64",
	}
	for in, want := range cases {
		if got := n.Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubVethPeerIfIndex(t *testing.T) {
	n := NewNormalizer()
	cases := map[string]string{
		// The `bridged` device's veth peer index is host-global.
		"eth1@if451 UP <MAC> <BROADCAST,MULTICAST,UP,LOWER_UP>": "eth1@if<IFINDEX> UP <MAC> <BROADCAST,MULTICAST,UP,LOWER_UP>",
		// A katharanp_vde interface has no peer suffix and is untouched.
		"eth0 UP <MAC> <BROADCAST,MULTICAST,UP,LOWER_UP>": "eth0 UP <MAC> <BROADCAST,MULTICAST,UP,LOWER_UP>",
		// Nothing else in the line may be rewritten.
		"vxlan1@if12 UNKNOWN <MAC> <UP> master br0": "vxlan1@if<IFINDEX> UNKNOWN <MAC> <UP> master br0",
	}
	for in, want := range cases {
		if got := n.ScrubVethPeerIfIndex(in); got != want {
			t.Errorf("ScrubVethPeerIfIndex(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScrubAddrJSONDropsDADAndSLAAC(t *testing.T) {
	n := NewNormalizer()
	n.KeepMAC("00:00:00:00:00:03")

	const in = `[{"ifindex":7,"ifname":"eth0","link_index":8,"mtu":1500,"addr_info":[
	  {"family":"inet6","local":"2001::3:200:ff:fe00:3","prefixlen":64,"scope":"global",
	   "protocol":"kernel_ra","dynamic":true,"mngtmpaddr":true,"tentative":true,
	   "valid_life_time":86400,"preferred_life_time":14400},
	  {"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link","tentative":true,
	   "valid_life_time":4294967295,"preferred_life_time":4294967295},
	  {"family":"inet6","local":"fe80::200:ff:fe00:3","prefixlen":64,"scope":"link",
	   "protocol":"kernel_ll","valid_life_time":4294967295,"preferred_life_time":4294967295}]}]`

	var doc []any
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(n.ScrubAddrJSON(doc))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	// The SLAAC entry goes whole; nothing of it may survive.
	for _, gone := range []string{"kernel_ra", "2001::3:200:ff:fe00:3", "mngtmpaddr", "tentative", "link_index", "ifindex", "86400", "14400"} {
		if strings.Contains(s, gone) {
			t.Errorf("ScrubAddrJSON kept %q: %s", gone, s)
		}
	}
	// The statically configured link-local and the EUI-64 of the pinned MAC
	// are real assertions and must survive byte-exact — and so do their
	// lifetimes, which for every entry that survives the kernel_ra filter are
	// the constant "forever".
	for _, kept := range []string{
		`"fe80::1"`, `"fe80::200:ff:fe00:3"`, `"kernel_ll"`, `"mtu":1500`,
		`"valid_life_time":4294967295`, `"preferred_life_time":4294967295`,
	} {
		if !strings.Contains(s, kept) {
			t.Errorf("ScrubAddrJSON dropped %q: %s", kept, s)
		}
	}
}

// TestScrubAddrJSONKeepsKernelOrder pins the removal of the addr_info sort. The
// kernel lists an interface's addresses in the order they were added — the
// link-local at carrier-up, then whatever the lab's .startup configured — and
// that order is an assertion about what this implementation did, not an artifact of the
// observation. Canonical-JSON sorting moved a static `2001::` address in front
// of the `fe80::` link-local it is listed after.
func TestScrubAddrJSONKeepsKernelOrder(t *testing.T) {
	n := NewNormalizer()
	const in = `[{"ifname":"eth0","addr_info":[
	  {"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link"},
	  {"family":"inet6","local":"2001:db8::1","prefixlen":64,"scope":"global"}]}]`

	var doc []any
	if err := json.Unmarshal([]byte(in), &doc); err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(n.ScrubAddrJSON(doc))
	if err != nil {
		t.Fatal(err)
	}
	ll, global := strings.Index(string(got), "fe80::1"), strings.Index(string(got), "2001:db8::1")
	if ll < 0 || global < 0 {
		t.Fatalf("ScrubAddrJSON lost an address: %s", got)
	}
	if ll > global {
		t.Fatalf("ScrubAddrJSON reordered addr_info: %s", got)
	}
}

// TestSplitSortCSV pins the sanctioned transform to exactly split + sort. The
// endpoint-sysctls opt is `",".join(<python set>)`, so its *order* is random
// and must go; nothing else about the value is noise.
func TestSplitSortCSV(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{{
		name: "the real opt, sorted",
		in:   "net.ipv6.conf.IFNAME.disable_ipv6=1,net.ipv4.conf.IFNAME.rp_filter=0",
		want: []string{"net.ipv4.conf.IFNAME.rp_filter=0", "net.ipv6.conf.IFNAME.disable_ipv6=1"},
	}, {
		name: "an empty value is an empty list, not a list holding one empty string",
		in:   "",
		want: []string{},
	}, {
		name: "an empty element is a defect in the value and is recorded",
		in:   "b=2,,a=1",
		want: []string{"", "a=1", "b=2"},
	}, {
		name: "whitespace around a sysctl name is a defect and is recorded",
		in:   " a=1,b=2",
		want: []string{" a=1", "b=2"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SplitSortCSV(tc.in); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitSortCSV(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestLinesKeepsCompletedProgressBar pins rule 6.6. rich prints the final
// progress render exactly once on a non-terminal, and HandleProgressBar's
// column set carries no clock, so the completed row is deterministic at a
// pinned COLUMNS and is a real assertion that the deploy finished. Only an
// *unfinished* render — the one whose SpinnerColumn is still animating — is
// time-derived, and only that one is dropped.
func TestLinesKeepsCompletedProgressBar(t *testing.T) {
	n := NewNormalizer()
	bar := strings.Repeat("━", 54)
	in := strings.Join([]string{
		"[Deploying collision domains]   " + strings.Repeat("━", 44) + " 1/1",
		"[Deploying devices]   " + bar + " 3/3",
		"[Deploying devices] ⠋ " + bar + " 0/3",
		"a1b2c3d4e5f6: Pulling fs layer",
		"[Downloading a1b2c3d4e5f6] ━ 42%",
		"[Download Complete a1b2c3d4e5f6] ━ 100%",
	}, "\n")
	want := []string{
		"[Deploying collision domains]   " + strings.Repeat("━", 44) + " 1/1",
		"[Deploying devices]   " + bar + " 3/3",
	}
	if got := n.Lines(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines() = %q, want %q", got, want)
	}
}

// TestHostsLinesLeavesPrivateAddressesAlone pins the removal of the /etc/hosts
// RFC1918 rewrite. The only daemon-allocated address that can reach this file
// is the `bridged` device's, which the runner registers as a literal from
// `docker inspect` — and which this test shows is still tokenized. The blanket
// rewrite additionally destroyed every lab-configured address in the same
// ranges, which is the assertion /etc/hosts exists to carry.
func TestHostsLinesLeavesPrivateAddressesAlone(t *testing.T) {
	n := NewNormalizer()
	n.AddLiteral("172.17.0.2", TokDockerIP)
	in := "127.0.0.1 localhost\n192.168.1.1 r1\n172.16.0.5 pc2\n172.17.0.2 wireshark\n"
	want := []string{"127.0.0.1 localhost", "192.168.1.1 r1", "172.16.0.5 pc2", "<DOCKERIP> wireshark"}
	if got := n.HostsLines(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("HostsLines() = %q, want %q", got, want)
	}
}

func TestEUI64LinkLocal(t *testing.T) {
	cases := map[string]string{
		"00:00:00:00:00:01": "fe80::200:ff:fe00:1",
		"aa:bb:cc:dd:ee:ff": "fe80::a8bb:ccff:fedd:eeff",
		"02:42:ac:11:00:02": "fe80::42:acff:fe11:2",
		"not-a-mac":         "",
	}
	for in, want := range cases {
		if got := eui64LinkLocal(in); got != want {
			t.Errorf("eui64LinkLocal(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLinesUnwrapsBeforeTokenizing(t *testing.T) {
	// The lab dir wraps across rows in the rendered record; after unwrapping,
	// the literal must match and tokenize.
	n := NewNormalizer()
	n.AddLiteral("/root/kathara/kathara-go/test/scenarios/synthetic/this-directory-does-not-exist", TokLabDir)
	path := "'/root/kathara/kathara-go/test/scenarios/synthetic/this-directory-does-not-exist'"
	chopAt := consoleWidth - 9
	in := strings.Join([]string{
		pad("CRITICAL (CreateFailed) root path", consoleWidth),
		pad("         "+path[:chopAt], consoleWidth),
		pad("         "+path[chopAt:]+" does not exist", consoleWidth),
	}, "\n")
	want := []string{"CRITICAL (CreateFailed) root path '<LABDIR>' does not exist"}
	if got := n.Lines(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("Lines() = %q, want %q", got, want)
	}
}

// TestNormalizeCapabilities pins the harness's capability canonicalization to
// the Go Docker SDK's own client-side rewrite
// (docker@v28.5.2/client/container_create.go:139 normalizeCapabilities, :159
// normalizeCap): upper-case, "CAP_" prefix unless already present or the value
// is the "ALL" magic value, de-duplicate, sort.
func TestNormalizeCapabilities(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{{
		name: "kathara MACHINE_CAPABILITIES as docker-py stores them",
		// DockerMachine.py's literal, in its source order: this is exactly what
		// `docker inspect` echoes on the Python side.
		in:   []string{"NET_ADMIN", "NET_RAW", "NET_BROADCAST", "NET_BIND_SERVICE", "SYS_ADMIN"},
		want: []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST", "CAP_NET_RAW", "CAP_SYS_ADMIN"},
	}, {
		name: "the same set as the Go SDK stores it is a fixed point",
		in:   []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST", "CAP_NET_RAW", "CAP_SYS_ADMIN"},
		want: []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE", "CAP_NET_BROADCAST", "CAP_NET_RAW", "CAP_SYS_ADMIN"},
	}, {
		name: "ALL is the magic value and keeps no prefix",
		in:   []string{"all"},
		want: []string{"ALL"},
	}, {
		name: "ALL sorts before prefixed names, as the SDK sorts it",
		in:   []string{"net_admin", "ALL"},
		want: []string{"ALL", "CAP_NET_ADMIN"},
	}, {
		name: "mixed case and mixed spelling collapse to one entry",
		in:   []string{"net_admin", "NET_ADMIN", "cap_net_admin", "CAP_NET_ADMIN"},
		want: []string{"CAP_NET_ADMIN"},
	}, {
		name: "empty list stays an empty list, not null",
		in:   []string{},
		want: []string{},
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeCapabilities(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeCapabilities(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Idempotent: re-recording a canonical snapshot must not move it.
			if again := NormalizeCapabilities(got); !reflect.DeepEqual(again, tc.want) {
				t.Fatalf("not idempotent: second pass = %q, want %q", again, tc.want)
			}
		})
	}

	// nil survives as nil: cap_drop is omitempty and an absent list must not
	// become an empty array in the snapshot.
	if got := NormalizeCapabilities(nil); got != nil {
		t.Fatalf("NormalizeCapabilities(nil) = %q, want nil", got)
	}

	// The empty-list case must serialize as [] so syn-privileged's recorded
	// `"cap_add": []` (Kathara passes cap_add=None under privileged) is stable.
	b, err := json.Marshal(NormalizeCapabilities([]string{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != "[]" {
		t.Fatalf("empty capability list marshals as %s, want []", b)
	}
}
