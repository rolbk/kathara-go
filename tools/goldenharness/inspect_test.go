package main

import (
	"encoding/json"
	"reflect"
	"testing"
)

// inspectFixture is a `docker inspect` answer trimmed to the keys the harness
// decodes, shaped after a real recording of syn-ulimit: two ulimits in lab.conf
// order, the shared bind, tty/stdin_open, and a device on one collision domain.
const inspectFixture = `[{
  "Id": "c0ffee",
  "Name": "/kathara_u_pc1_h",
  "State": {"Status": "running", "Running": true},
  "Config": {
    "Hostname": "pc1", "User": "", "Image": "kathara/base",
    "Env": ["PATH=/usr/bin"], "Cmd": null, "Entrypoint": null,
    "Tty": true, "OpenStdin": true,
    "Labels": {"name": "pc1", "app": "kathara", "lab_hash": "h", "user": "u"}
  },
  "HostConfig": {
    "NetworkMode": "kathara_u_A_h",
    "Binds": [
      "/labs/syn/shared:/shared:rw",
      "/home/u:/hosthome:rw",
      "/labs/syn/vol:/mnt/vol:rw"
    ],
    "CapAdd": ["NET_ADMIN"], "CapDrop": null, "Privileged": false,
    "Sysctls": {"net.ipv4.ip_forward": "1"},
    "Memory": 0, "NanoCpus": 0,
    "PortBindings": {},
    "Ulimits": [
      {"Name": "nproc", "Soft": 512, "Hard": 512},
      {"Name": "nofile", "Soft": 1024, "Hard": 2048}
    ]
  },
  "Mounts": [],
  "NetworkSettings": {
    "Ports": {},
    "Networks": {
      "kathara_u_A_h": {
        "DriverOpts": {"kathara.iface": "0", "kathara.link": "A"},
        "NetworkID": "n1", "EndpointID": "e1", "MacAddress": "",
        "IPAddress": "", "GlobalIPv6Address": ""
      }
    }
  }
}]`

func decodeFixture(t *testing.T, s string) []rawContainer {
	t.Helper()
	var raw []rawContainer
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return raw
}

func TestBuildContainerRecordsUlimitOrder(t *testing.T) {
	n := NewNormalizer()
	recs, failures := buildContainerRecords(decodeFixture(t, inspectFixture),
		map[string]string{"kathara_u_A_h": "A"}, n)
	if len(failures) != 0 {
		t.Fatalf("unexpected assertion failures: %v", failures)
	}
	want := []UlimitRecord{
		{Name: "nproc", Soft: 512, Hard: 512},
		{Name: "nofile", Soft: 1024, Hard: 2048},
	}
	if !reflect.DeepEqual(recs[0].Ulimits, want) {
		t.Fatalf("ulimits = %+v, want %+v (insertion order, not sorted)", recs[0].Ulimits, want)
	}
}

// TestBuildContainerRecordsBinds pins HostConfig.Binds: recorded verbatim, in
// the order Kathara built the `volumes` dict (shared, hosthome, then the
// device's own `volume` options), with the host side tokenized exactly as the
// Mounts array's Source is. Nothing recorded this list before, so a port that
// mounted /shared read-only, dropped /hosthome, or emitted the three binds in
// another order was invisible.
func TestBuildContainerRecordsBinds(t *testing.T) {
	n := NewNormalizer()
	n.AddLiteral("/labs/syn", TokLabDir)
	n.AddLiteral("/home/u", TokHome)
	recs, _ := buildContainerRecords(decodeFixture(t, inspectFixture),
		map[string]string{"kathara_u_A_h": "A"}, n)
	want := []string{
		"<LABDIR>/shared:/shared:rw",
		"<HOME>:/hosthome:rw",
		"<LABDIR>/vol:/mnt/vol:rw",
	}
	if !reflect.DeepEqual(recs[0].Binds, want) {
		t.Fatalf("binds = %q, want %q", recs[0].Binds, want)
	}
}

// TestBuildContainerRecordsTtyAndOpenStdin pins `tty=True` / `stdin_open=True`,
// which DockerMachine.create passes unconditionally. A container created
// without a TTY is the most user-visible untested create parameter: `kathara
// connect` and every startup script's line discipline depend on it.
func TestBuildContainerRecordsTtyAndOpenStdin(t *testing.T) {
	n := NewNormalizer()
	recs, _ := buildContainerRecords(decodeFixture(t, inspectFixture),
		map[string]string{"kathara_u_A_h": "A"}, n)
	if !recs[0].Tty || !recs[0].OpenStdin {
		t.Fatalf("tty = %v, open_stdin = %v, want both true", recs[0].Tty, recs[0].OpenStdin)
	}
}

// TestBuildContainerRecordsNilBindsStayNil keeps the null/[] distinction the
// daemon draws: a device with no volumes at all is reported as `"Binds": null`
// and that is a different fact from an empty list.
func TestBuildContainerRecordsNilBindsStayNil(t *testing.T) {
	var raw []rawContainer
	if err := json.Unmarshal([]byte(`[{"Name":"/x","Config":{"Labels":{"name":"pc1"}},
	  "HostConfig":{"Binds":null},"NetworkSettings":{"Networks":{}}}]`), &raw); err != nil {
		t.Fatal(err)
	}
	recs, _ := buildContainerRecords(raw, map[string]string{}, NewNormalizer())
	if recs[0].Binds != nil {
		t.Fatalf("binds = %#v, want nil", recs[0].Binds)
	}
}

// TestBuildNetworkRecordsIPAMAndOptions pins the rest of what
// `DockerLink.create`'s `networks.create(...)` decides: no IPv6, the null IPAM
// driver's synthesized 0.0.0.0/0 row, and no driver options. None of the three
// was recorded before, so a port that enabled IPv6 on a collision domain or
// handed it a real subnet produced an identical golden.
func TestBuildNetworkRecordsIPAMAndOptions(t *testing.T) {
	var raw []rawNetwork
	if err := json.Unmarshal([]byte(`[{
	  "Name": "kathara_u_A_h", "Id": "n1", "Scope": "local",
	  "Driver": "kathara/katharanp_vde:amd64",
	  "Internal": false, "Attachable": false, "EnableIPv6": false,
	  "Options": {},
	  "IPAM": {"Driver": "null", "Config": [{"Subnet": "0.0.0.0/0"}]},
	  "Labels": {"name": "A", "app": "kathara", "external": "", "lab_hash": "h", "user": "u"}
	}]`), &raw); err != nil {
		t.Fatal(err)
	}
	recs, links := buildNetworkRecords(raw, NewNormalizer())
	if links["kathara_u_A_h"] != "A" {
		t.Fatalf("link map = %v", links)
	}
	rec := recs[0]
	if rec.EnableIPv6 {
		t.Error("enable_ipv6 = true, want false: DockerLink.create never asks for IPv6")
	}
	if !reflect.DeepEqual(rec.Options, map[string]string{}) {
		t.Errorf("options = %v, want {}", rec.Options)
	}
	want := []map[string]any{{"Subnet": "0.0.0.0/0"}}
	if !reflect.DeepEqual(rec.IPAMConfig, want) {
		t.Errorf("ipam_config = %v, want %v", rec.IPAMConfig, want)
	}
}
