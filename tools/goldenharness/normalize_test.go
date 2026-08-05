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

	cases := map[string]string{
		// A plain random MAC is tokenized.
		"eth0 UP aa:bb:cc:dd:ee:f0 <UP>": "eth0 UP <MAC> <UP>",
		// Constants carry no identity.
		"lo 00:00:00:00:00:00 x": "lo 00:00:00:00:00:00 x",
		"bc ff:ff:ff:ff:ff:ff x": "bc ff:ff:ff:ff:ff:ff x",
		// Explicitly pinned MACs survive.
		"eth0 UP 00:00:00:00:00:01 <UP>": "eth0 UP 00:00:00:00:00:01 <UP>",
		// The interior of an IPv6 address is not a MAC.
		"addr 2001:db8:aa:bb:cc:dd:ee:f0/64": "addr 2001:db8:aa:bb:cc:dd:ee:f0/64",
		"via fc00:12:34:56:78:9a:bc:de dev":  "via fc00:12:34:56:78:9a:bc:de dev",
	}
	for in, want := range cases {
		if got := n.Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
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
	  {"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link","tentative":true},
	  {"family":"inet6","local":"fe80::200:ff:fe00:3","prefixlen":64,"scope":"link",
	   "protocol":"kernel_ll"}]}]`

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
	for _, gone := range []string{"kernel_ra", "2001::3:200:ff:fe00:3", "mngtmpaddr", "tentative", "link_index", "ifindex", "valid_life_time"} {
		if strings.Contains(s, gone) {
			t.Errorf("ScrubAddrJSON kept %q: %s", gone, s)
		}
	}
	// The statically configured link-local and the EUI-64 of the pinned MAC
	// are real assertions and must survive byte-exact.
	for _, kept := range []string{`"fe80::1"`, `"fe80::200:ff:fe00:3"`, `"kernel_ll"`, `"mtu":1500`} {
		if !strings.Contains(s, kept) {
			t.Errorf("ScrubAddrJSON dropped %q: %s", kept, s)
		}
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
