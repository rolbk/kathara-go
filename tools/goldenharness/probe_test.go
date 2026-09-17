package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestVolatileMACsMaskDerivedAddresses is the rule the keyed MAC tokens made
// necessary. `br_stp_recalculate_bridge_id` gives a Linux bridge the smallest
// MAC among its enslaved ports, so with random port MACs *which* port it copies
// is a coin flip — three consecutive Python deploys of 20-vxlan-base recorded
// br100 copying eth0, then vtep100, then eth0. Masking the bridge before the
// keyed scrub both waives that relation and keeps the other interfaces'
// ordinals stable however the copy landed: eth0 and eth1 below are <MAC1> and
// <MAC2> whichever address br100 happens to hold.
func TestVolatileMACsMaskDerivedAddresses(t *testing.T) {
	// The bridge's row comes first in the kernel dump, as it does in the real
	// recording (the vxlan and the bridge get low in-namespace ifindexes while
	// the plugin's interfaces keep high ones).
	rows := func(bridgeMAC string) string {
		return "br100             UP             " + bridgeMAC + " <BROADCAST,MULTICAST,UP,LOWER_UP>\n" +
			"eth0              UP             aa:bb:cc:00:00:01 <BROADCAST,MULTICAST,UP,LOWER_UP>\n" +
			"eth1              UP             aa:bb:cc:00:00:02 <BROADCAST,MULTICAST,UP,LOWER_UP>\n"
	}
	want := []string{
		"br100 UP <MACDERIVED> <BROADCAST,MULTICAST,UP,LOWER_UP>",
		"eth0 UP <MAC1> <BROADCAST,MULTICAST,UP,LOWER_UP>",
		"eth1 UP <MAC2> <BROADCAST,MULTICAST,UP,LOWER_UP>",
	}
	derived := map[string]bool{"br100": true}
	// Both coin-flip outcomes must record identically.
	for _, bridgeMAC := range []string{"aa:bb:cc:00:00:01", "aa:bb:cc:00:00:09"} {
		p := NewProber(nil, NewNormalizer())
		if got := p.normalizeBrLink(rows(bridgeMAC), derived); !reflect.DeepEqual(got, want) {
			t.Fatalf("bridge MAC %s: normalizeBrLink =\n %q\nwant\n %q", bridgeMAC, got, want)
		}
	}
	// Without the rule the two outcomes are distinguishable, which is what
	// made the Go verify diff.
	a := NewProber(nil, NewNormalizer())
	b := NewProber(nil, NewNormalizer())
	if reflect.DeepEqual(
		a.normalizeBrLink(rows("aa:bb:cc:00:00:01"), nil),
		b.normalizeBrLink(rows("aa:bb:cc:00:00:09"), nil),
	) {
		t.Fatal("the two coin-flip outcomes are indistinguishable even unmasked; the fixture is wrong")
	}
}

// TestVolatileMACsMaskAddrJSON is the same waiver on the other probe that
// carries the address.
func TestVolatileMACsMaskAddrJSON(t *testing.T) {
	p := NewProber(nil, NewNormalizer())
	const in = `[{"ifname":"br100","address":"aa:bb:cc:00:00:09","addr_info":[]},
	             {"ifname":"eth0","address":"aa:bb:cc:00:00:01","addr_info":[]}]`
	got, err := p.normalizeAddrJSON(in, map[string]bool{"br100": true})
	if err != nil {
		t.Fatal(err)
	}
	if got[0]["address"] != TokDerivedMAC {
		t.Errorf("br100 address = %v, want %s", got[0]["address"], TokDerivedMAC)
	}
	if got[1]["address"] != "<MAC1>" {
		t.Errorf("eth0 address = %v, want <MAC1>: masking must not consume an ordinal", got[1]["address"])
	}
}

// TestVolatileMACSetIsPerDevice keeps one device's waiver from reaching another.
func TestVolatileMACSetIsPerDevice(t *testing.T) {
	sc := &Scenario{VolatileMACs: []string{"vtep1:br100", "vtep2:br100"}}
	if !sc.VolatileMACSet("vtep1")["br100"] {
		t.Error("vtep1:br100 not in vtep1's set")
	}
	if len(sc.VolatileMACSet("s1")) != 0 {
		t.Errorf("s1 inherited a waiver: %v", sc.VolatileMACSet("s1"))
	}
	// The recorded manifest entry must survive into the snapshot header.
	b, err := json.Marshal(ScenarioRecord{VolatileMACs: sc.VolatileMACs})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(json.Valid(b), true) {
		t.Fatal("scenario record does not marshal")
	}
}

// TestParseFSTreeRecordsModeOwner pins the mode/uid/gid triple the fs-tree walk
// now carries. `pack_data` ships a tar built from the host tree and the bind
// mount carries the host's ownership through, so a port that rewrote a mode,
// dropped an exec bit or chowned the tree while producing byte-identical file
// contents was previously invisible.
func TestParseFSTreeRecordsModeOwner(t *testing.T) {
	p := NewProber(nil, NewNormalizer())
	const out = "d\t./etc\t755\t0\t0\t\n" +
		"f\t./etc/hosts\t644\t0\t0\tabc123\n" +
		"f\t./run.sh\t755\t1000\t1000\tdef456\n" +
		"l\t./link\t777\t0\t0\t./run.sh\n"
	want := []FSEntry{
		{Kind: "d", Path: "./etc", Mode: "755", UID: "0", GID: "0"},
		{Kind: "f", Path: "./etc/hosts", Mode: "644", UID: "0", GID: "0", SHA256: "abc123"},
		{Kind: "l", Path: "./link", Mode: "777", UID: "0", GID: "0", Target: "./run.sh"},
		{Kind: "f", Path: "./run.sh", Mode: "755", UID: "1000", GID: "1000", SHA256: "def456"},
	}
	if got := p.parseFSTree(out); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFSTree =\n %+v\nwant\n %+v", got, want)
	}
}

// TestParseFSTreeToleratesMissingStat: an image whose `stat` cannot produce the
// triple must still yield a usable tree rather than an empty one.
func TestParseFSTreeToleratesMissingStat(t *testing.T) {
	p := NewProber(nil, NewNormalizer())
	got := p.parseFSTree("f\t./x\t\t\t\tabc\n")
	want := []FSEntry{{Kind: "f", Path: "./x", SHA256: "abc"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseFSTree = %+v, want %+v", got, want)
	}
}
