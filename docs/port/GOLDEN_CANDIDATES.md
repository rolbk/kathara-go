# Layer A Golden-Test Candidates — Kathara-Labs Inventory

Source: `/root/kathara/Kathara-Labs` — 89 `lab.conf` scenarios, inventoried 2026-08-05 against
`PORT_SPEC.md` §9 Layer A. Parsing cross-checked against
`/root/kathara/kathara-python/src/Kathara/parser/netkit/LabParser.py` and
`/root/kathara/kathara-python/src/Kathara/model/Machine.py` (option semantics) and
`docs/kathara-lab.conf.5.ronn` (documented option set).

## Corpus-wide facts (checked, not sampled)

- **Machine options actually used anywhere in the corpus: only 6** — `image` (78 labs),
  `ipv6` (14), `bridged` (9), `sysctl` (7), `port` (6), `num_terms` (5, always `=0`).
  Everything else in the documented option set (`mem`, `cpus`, `exec`, `env`, `ulimit`,
  `shell`, `privileged`, `volume`) appears in **zero** labs → synthetic scenarios required (§ below).
- **`lab.dep`: absent from the entire corpus.** **`shared.startup`: absent.** **`_test/` folders: absent.**
  **`*.shutdown` files: absent.** Table columns for these are omitted; all would read "—" 89 times.
  Each needs a synthetic scenario.
- **`lab.ext`: exactly 3 labs** (`tutorials/kathara-external/*`). ExternalLink is DEFERRED in 1.0
  (spec §0.3) → all 3 are UNUSABLE as goldens but are the natural fixtures for the
  `FeatureNotAvailable` error-path test.
- **Explicit MAC syntax (`cd/mac`): 10 labs** (basic-topics + one-bridge + pox/01).
  NOTE (see `MEMORY.md` / MAC-derivation finding): the spec §9 claim that Kathara derives MACs as
  `md5("<machine>-<iface>")` is **false**; goldens must assert wiring via DriverOpts / explicit
  `cd/mac` labs, and these 10 labs are where explicit MACs are exact assertions.
- **Lab metadata:** only `LAB_DESCRIPTION`, `LAB_VERSION`, `LAB_AUTHOR`, `LAB_EMAIL`, `LAB_WEB`
  are used (61 labs have all 5, 28 have none). **`LAB_NAME` is used by zero labs** → synthetic.
- **Value-syntax variants present** (parser strips quotes; still worth exercising):
  `bridged=true` (6 labs) vs `bridged="True"` (pox/07-09); `ipv6="false"`/`ipv6=True`/
  `ipv6="True"`/`ipv6="False"` (basic-ipv6 has both quoted forms; data-center labs have unquoted `True`);
  `port="3000:3000"` vs `port="3000:3000/tcp"` (protocol suffix only in `tutorials/capture-packets`).
- **No startup script anywhere needs the internet** (no `apt/curl/wget/pip/git` in any of the 506
  `.startup` files). Every lab is runnable in an isolated Linux VM once images are pre-pulled.
  P4 labs compile locally with `p4c` at boot.
- One structural oddity worth keeping: `exercises/01-simple-configuration` is a **single machine
  with two interfaces** on two collision domains — smallest possible deploy golden.
- `two-computers` has **no `.startup` files at all** (covers the missing-startup path);
  `one-bridge` and `basic-ipv6` have fewer startup files than machines.

Size classes: SMALL ≤4 devices, MEDIUM 5–9, LARGE ≥10.
"Dev" = device count, "CDs" = collision domains, "MAC" = explicit `cd/mac` syntax,
"ext" = `lab.ext` present, "LAB_ meta" = number of `LAB_*` metadata keys set.

## Inventory — all 89 scenarios

| # | Path (under `Kathara-Labs/`) | Dev | CDs | Images (explicit; DFLT = default `kathara/base`) | lab.conf options | LAB_ meta | MAC | ext | Size | Notes |
|---|---|---|---|---|---|---|---|---|---|---|
| 1 | `exam-labs/2013-12-20-cloth-cap/lab` | 8 | 14 | DFLT | — | 5 | — | — | MEDIUM |  |
| 2 | `exam-labs/2018-01-17-stairs/lab` | 8 | 10 | DFLT | — | 5 | — | — | MEDIUM |  |
| 3 | `exam-labs/2020-11-05-chair/chair-01` | 6 | 4 | DFLT | — | — | — | — | MEDIUM |  |
| 4 | `exam-labs/2020-11-05-chair/chair-02` | 5 | 3 | DFLT | — | — | — | — | MEDIUM |  |
| 5 | `exam-labs/2020-11-18-puzzle/lab` | 7 | 10 | DFLT | — | — | — | — | MEDIUM |  |
| 6 | `exam-labs/2020-11-18-the-thing/lab` | 7 | 9 | DFLT | — | — | — | — | MEDIUM |  |
| 7 | `exam-labs/2020-12-18-red-baron/lab` | 6 | 12 | DFLT | — | — | — | — | MEDIUM |  |
| 8 | `exam-labs/2022-01-14-alien/lab` | 10 | 9 | DFLT | — | 5 | — | — | LARGE |  |
| 9 | `exercises/01-simple-configuration/lab` | 1 | 2 | DFLT | — | — | — | — | SMALL |  |
| 10 | `exercises/02-add-one-router/lab` | 4 | 3 | DFLT | — | — | — | — | SMALL |  |
| 11 | `main-labs/application-level/dns/kathara-lab_dns` | 10 | 1 | kathara/bind:9.11.5, kathara/core, lscr.io/linuxserver/wireshark | bridged, image, ipv6, num_terms, port | 5 | — | — | LARGE | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 12 | `main-labs/application-level/http-tcp/kathara-lab_http-tcp` | 3 | 1 | kathara/apache, kathara/core, lscr.io/linuxserver/wireshark | bridged, image, ipv6, num_terms, port | 5 | — | — | SMALL | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 13 | `main-labs/application-level/load-balancer-random/kathara-lab_load-balancer-ws-rnd` | 6 | 2 | kathara/apache, kathara/core | image | 5 | — | — | MEDIUM |  |
| 14 | `main-labs/application-level/web-server/kathara-lab_web-server` | 2 | 1 | kathara/apache, kathara/core | image | 5 | — | — | SMALL |  |
| 15 | `main-labs/basic-topics/arp/kathara-lab_arp` | 5 | 3 | kathara/core | image | 5 | — | — | MEDIUM |  |
| 16 | `main-labs/basic-topics/basic-ipv4/kathara-lab_basic-ipv4` | 6 | 3 | kathara/core, lscr.io/linuxserver/wireshark | bridged, image, ipv6, num_terms, port | 5 | Y | — | MEDIUM | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 17 | `main-labs/basic-topics/basic-ipv6/kathara-lab_basic-ipv6` | 6 | 3 | kathara/core, lscr.io/linuxserver/wireshark | bridged, image, ipv6, num_terms, port, sysctl | 5 | Y | — | MEDIUM | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 18 | `main-labs/basic-topics/static-routing/kathara-lab_static-routing` | 4 | 3 | kathara/core | image | 5 | — | — | SMALL |  |
| 19 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/kathara-lab_subnetting-1-lan` | 4 | 2 | kathara/core | image, ipv6 | 5 | Y | — | SMALL |  |
| 20 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/kathara-lab_subnetting-2-lan` | 4 | 3 | kathara/core | image, ipv6 | 5 | Y | — | SMALL |  |
| 21 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/kathara-lab_subnetting-4-lan` | 6 | 5 | kathara/core | image, ipv6 | 5 | Y | — | MEDIUM |  |
| 22 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/solutions/kathara-lab_subnetting-2-lan-solution` | 4 | 3 | kathara/core | image, ipv6 | 5 | Y | — | SMALL |  |
| 23 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/solutions/kathara-lab_subnetting-4-lan-solution` | 6 | 5 | kathara/core | image, ipv6 | 5 | Y | — | MEDIUM |  |
| 24 | `main-labs/basic-topics/two-computers/kathara-lab_two-computers` | 3 | 1 | kathara/core, lscr.io/linuxserver/wireshark | bridged, image, ipv6, num_terms, port | 5 | Y | — | SMALL | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 25 | `main-labs/data-center-routing/data-center-bgp/kathara-lab_data-center-bgp` | 18 | 24 | kathara/apache, kathara/frr:9 | image, ipv6, sysctl | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 26 | `main-labs/data-center-routing/data-center-vxlan/kathara-lab_data-center-vxlan` | 21 | 30 | kathara/apache, kathara/core, kathara/frr:9 | image, ipv6, sysctl | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 27 | `main-labs/data-center-routing/data-center-vxlan/kathara-lab_data-center-vxlan-no-bond` | 21 | 26 | kathara/apache, kathara/core, kathara/frr:9 | image, ipv6, sysctl | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 28 | `main-labs/data-center-routing/data-center-vxlan/kathara-lab_vxlan-base` | 4 | 3 | kathara/core, kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 29 | `main-labs/interdomain-routing/frr/bgp-announcement/kathara-lab_bgp-announcement` | 2 | 3 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 30 | `main-labs/interdomain-routing/frr/bgp-multi-homed-stub-large/kathara-lab_bgp-multi-homed-stub-large` | 6 | 10 | kathara/frr:latest | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 31 | `main-labs/interdomain-routing/frr/bgp-multi-homed-stub/kathara-lab_bgp-multi-homed-stub` | 4 | 6 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 32 | `main-labs/interdomain-routing/frr/bgp-multi-homed/kathara-lab_bgp-multi-homed` | 7 | 9 | kathara/frr:9 | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 33 | `main-labs/interdomain-routing/frr/bgp-prefix-filtering/kathara-lab_bgp-prefix-filtering` | 2 | 5 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 34 | `main-labs/interdomain-routing/frr/bgp-simple-peering/kathara-lab_bgp-simple-peering` | 2 | 3 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 35 | `main-labs/interdomain-routing/frr/bgp-stub-as-static/kathara-lab_bgp-stub-as-static` | 2 | 4 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 36 | `main-labs/interdomain-routing/frr/bgp-stub-as/kathara-lab_bgp-stub-as` | 2 | 4 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 37 | `main-labs/interdomain-routing/quagga/bgp-announcement/kathara-lab_bgp-announcement` | 2 | 3 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 38 | `main-labs/interdomain-routing/quagga/bgp-multi-homed-stub-large/kathara-lab_bgp-multi-homed-stub-large` | 6 | 10 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 39 | `main-labs/interdomain-routing/quagga/bgp-multi-homed-stub/kathara-lab_bgp-multi-homed-stub` | 4 | 6 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 40 | `main-labs/interdomain-routing/quagga/bgp-multi-homed/kathara-lab_bgp-multi-homed` | 7 | 9 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 41 | `main-labs/interdomain-routing/quagga/bgp-prefix-filtering/kathara-lab_bgp-prefix-filtering` | 2 | 5 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 42 | `main-labs/interdomain-routing/quagga/bgp-simple-peering/kathara-lab_bgp-simple-peering` | 2 | 3 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 43 | `main-labs/interdomain-routing/quagga/bgp-stub-as-static/kathara-lab_bgp-stub-as-static` | 2 | 4 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 44 | `main-labs/interdomain-routing/quagga/bgp-stub-as/kathara-lab_bgp-stub-as` | 2 | 4 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 45 | `main-labs/interdomain-routing/quagga/bgp-transit-as/kathara-lab_bgp-transit-as-redistribute-bgp-deterministic` | 9 | 11 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 46 | `main-labs/interdomain-routing/quagga/bgp-transit-as/kathara-lab_bgp-transit-as-redistribute-bgp-nondeterministic` | 9 | 11 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets; nondeterministic by design (BGP tie-break); do not golden |
| 47 | `main-labs/interdomain-routing/quagga/bgp-transit-as/kathara-lab_bgp-transit-as-tunnel-ipip` | 9 | 11 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 48 | `main-labs/intradomain-routing/frr/frrouting-introduction/kathara-lab_frr` | 3 | 4 | kathara/frr:9 | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 49 | `main-labs/intradomain-routing/frr/ospf/kathara-lab_ospf/kathara-lab_ospf_frr-complex` | 12 | 15 | kathara/frr:9 | image | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 50 | `main-labs/intradomain-routing/frr/ospf/kathara-lab_ospf/kathara-lab_ospf_frr-multiarea` | 11 | 14 | kathara/frr:9 | image | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 51 | `main-labs/intradomain-routing/frr/ospf/kathara-lab_ospf/kathara-lab_ospf_frr-singlearea` | 5 | 4 | kathara/frr:9 | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 52 | `main-labs/intradomain-routing/frr/rip/kathara-lab_rip` | 5 | 10 | kathara/frr:9 | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 53 | `main-labs/intradomain-routing/quagga/ospf/kathara-lab_ospf/kathara-lab_ospf-complex` | 12 | 15 | kathara/quagga | image | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 54 | `main-labs/intradomain-routing/quagga/ospf/kathara-lab_ospf/kathara-lab_ospf-multiarea` | 11 | 14 | kathara/quagga | image | 5 | — | — | LARGE | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 55 | `main-labs/intradomain-routing/quagga/ospf/kathara-lab_ospf/kathara-lab_ospf-singlearea` | 5 | 4 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 56 | `main-labs/intradomain-routing/quagga/quagga-introduction/kathara-lab_quagga` | 3 | 4 | kathara/quagga | image | 5 | — | — | SMALL | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 57 | `main-labs/intradomain-routing/quagga/rip/kathara-lab_rip` | 5 | 10 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 58 | `main-labs/labs-integrating-several-technologies/bgp-ospf-rip-interplay/kathara-lab_bgp-ospf-rip` | 9 | 14 | kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 59 | `main-labs/labs-integrating-several-technologies/dns-load-balancer-with-rip/kathara-lab_dns-load-balancer-with-rip` | 9 | 4 | kathara/apache, kathara/bind:9.11.5, kathara/core, kathara/quagga | image | 5 | — | — | MEDIUM | routing daemons: in-device route state converges nondeterministically; golden only static facets |
| 60 | `main-labs/labs-integrating-several-technologies/small-internet-with-dns-and-web-server/kathara-lab_small-internet-with-dns-and-web-server` | 15 | 17 | kathara/apache, kathara/bind:9.11.5, kathara/core, kathara/quagga | image | 5 | — | — | LARGE |  |
| 61 | `main-labs/labs-integrating-several-technologies/two-levels-load-balancing-for-a-web-service/kathara-lab_two-levels-load-balancing` | 13 | 7 | kathara/apache, kathara/bind:9.11.5, kathara/core | image | 5 | — | — | LARGE |  |
| 62 | `main-labs/p4/01-reflector` | 2 | 1 | kathara/bmv2, kathara/core | image, sysctl | — | — | — | SMALL | large bmv2 image; p4c compile at boot (local, no internet) |
| 63 | `main-labs/p4/02-repeater` | 3 | 2 | kathara/bmv2, kathara/core | image, sysctl | — | — | — | SMALL | large bmv2 image; p4c compile at boot (local, no internet) |
| 64 | `main-labs/p4/03-l2-basic-forwarding` | 5 | 4 | kathara/bmv2, kathara/core | image, sysctl | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 65 | `main-labs/p4/03-l2-flooding-flood-others` | 5 | 4 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 66 | `main-labs/p4/03-l2_flooding_flood_all` | 5 | 4 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 67 | `main-labs/p4/04-l2-learning-cpu-copy` | 5 | 4 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 68 | `main-labs/p4/04-l2-learning-digest` | 5 | 4 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 69 | `main-labs/p4/04-mpls-basics` | 10 | 11 | kathara/bmv2, kathara/core | image | — | — | — | LARGE | large bmv2 image; p4c compile at boot (local, no internet) |
| 70 | `main-labs/p4/05-ecmp` | 8 | 10 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 71 | `main-labs/p4/05-flowlet-switching` | 8 | 10 | kathara/bmv2, kathara/core | image | — | — | — | MEDIUM | large bmv2 image; p4c compile at boot (local, no internet) |
| 72 | `main-labs/sdn-openflow/pox/01-pox-controller` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | image | — | Y | — | SMALL | pox controller lab |
| 73 | `main-labs/sdn-openflow/pox/02-pox-core-object` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | image | — | — | — | SMALL | pox controller lab |
| 74 | `main-labs/sdn-openflow/pox/03-pox-events` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | image | — | — | — | SMALL | pox controller lab |
| 75 | `main-labs/sdn-openflow/pox/04-pox-work-with-packets` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | image | — | — | — | SMALL | pox controller lab |
| 76 | `main-labs/sdn-openflow/pox/05-pox-datapaths` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | image | — | — | — | SMALL | pox controller lab |
| 77 | `main-labs/sdn-openflow/pox/06-pox-link-discovery` | 5 | 5 | kathara/pox, kathara/sdn | image | — | — | — | MEDIUM | pox controller lab |
| 78 | `main-labs/sdn-openflow/pox/07-pox-host-discovery` | 9 | 9 | kathara/core, kathara/pox, kathara/sdn | bridged, image | — | — | — | MEDIUM | pox controller lab |
| 79 | `main-labs/sdn-openflow/pox/08-pox-arp-handler` | 4 | 3 | kathara/core, kathara/pox, kathara/sdn | bridged, image | — | — | — | SMALL | pox controller lab |
| 80 | `main-labs/sdn-openflow/pox/09-pox-routing` | 9 | 9 | kathara/core, kathara/pox, kathara/sdn | bridged, image | — | — | — | MEDIUM | pox controller lab |
| 81 | `main-labs/switching/one-bridge/kathara-lab_one-bridge` | 5 | 4 | kathara/core | image, ipv6 | 5 | Y | — | MEDIUM |  |
| 82 | `tutorials/capture-packets/lab` | 3 | 1 | DFLT, lscr.io/linuxserver/wireshark | bridged, image, port | — | — | — | SMALL | third-party ~1GB wireshark web-GUI image (runs headless, no host GUI needed) |
| 83 | `tutorials/kathara-external/base-configuration/lab` | 1 | 1 | kathara/base | image | 5 | — | Y | SMALL | lab.ext: UNUSABLE for goldens (ExternalLink DEFERRED in 1.0); error-path test candidate |
| 84 | `tutorials/kathara-external/communicating-with-the-host/lab` | 1 | 1 | kathara/base | image | 5 | — | Y | SMALL | lab.ext: UNUSABLE for goldens (ExternalLink DEFERRED in 1.0); error-path test candidate |
| 85 | `tutorials/kathara-external/vlan-configuration/lab` | 1 | 1 | DFLT | — | — | — | Y | SMALL | lab.ext: UNUSABLE for goldens (ExternalLink DEFERRED in 1.0); error-path test candidate |
| 86 | `tutorials/traffic-control/fixed-delay/lab` | 3 | 2 | kathara/base | image | 5 | — | — | SMALL |  |
| 87 | `tutorials/traffic-control/limit-bandwidth/lab` | 3 | 2 | kathara/base | image | 5 | — | — | SMALL |  |
| 88 | `tutorials/traffic-control/packet-loss/lab` | 3 | 2 | kathara/base | image | 5 | — | — | SMALL |  |
| 89 | `tutorials/traffic-control/variable-delay/lab` | 3 | 2 | kathara/base | image | 5 | — | — | SMALL |  |

## Proposed GOLDEN SET (24 scenarios, 111 devices total, 10 images)

Selection criteria: every corpus-used lab.conf option and value-syntax variant covered at least
once, every image except the redundant `kathara/frr:latest` tag covered, one LARGE stress lab,
preference for SMALL/deterministic labs, all runnable in an isolated Linux VM with pre-pulled
images. Paths relative to `/root/kathara/Kathara-Labs/`.

| # | Scenario | Dev/CD | Why it is in the set |
|---|---|---|---|
| G1 | `exercises/01-simple-configuration/lab` | 1/2 | Smallest possible deploy; default image; single machine with two interfaces |
| G2 | `exercises/02-add-one-router/lab` | 4/3 | SMALL default-image multi-device lab, deterministic static config |
| G3 | `exam-labs/2020-11-05-chair/chair-02` | 5/3 | Default image + zero LAB_ metadata (metadata-absent path) |
| G4 | `exam-labs/2013-12-20-cloth-cap/lab` | 8/14 | Default image + all 5 LAB_ metadata keys; high CD:device ratio (14 networks) |
| G5 | `main-labs/basic-topics/two-computers/kathara-lab_two-computers` | 3/1 | Explicit MAC; `bridged`, `port`, `num_terms=0`; **no `.startup` files at all** (missing-startup path); wireshark image |
| G6 | `main-labs/basic-topics/basic-ipv6/kathara-lab_basic-ipv6` | 6/3 | Densest option mix: `sysctl` (ipv6 ns), `ipv6="True"` AND `ipv6="False"` quoted variants, MAC, bridged/port/num_terms |
| G7 | `main-labs/basic-topics/static-routing/kathara-lab_static-routing` | 4/3 | Static routes → fully deterministic in-device `ip route` golden |
| G8 | `main-labs/basic-topics/arp/kathara-lab_arp` | 5/3 | Plain `kathara/core` lab, deterministic, per-device config folders |
| G9 | `main-labs/basic-topics/subnetting-ipv4/kathara-lab_subnetting-ipv4/kathara-lab_subnetting-4-lan` | 6/5 | MAC + `ipv6="false"` on every device; 5 CDs |
| G10 | `main-labs/switching/one-bridge/kathara-lab_one-bridge` | 5/4 | MAC + ipv6; only 1 startup file for 5 devices (partial-startup path); in-device Linux bridge |
| G11 | `tutorials/capture-packets/lab` | 3/1 | Only lab with `port="3000:3000/tcp"` protocol suffix; default-image + third-party image mix; no metadata |
| G12 | `tutorials/traffic-control/fixed-delay/lab` | 3/2 | Explicit `kathara/base` image reference; tc/netem startup |
| G13 | `main-labs/application-level/web-server/kathara-lab_web-server` | 2/1 | Smallest `kathara/apache` lab |
| G14 | `main-labs/labs-integrating-several-technologies/dns-load-balancer-with-rip/kathara-lab_dns-load-balancer-with-rip` | 9/4 | 4 images in one lab (apache+bind+core+quagga) — multi-image deploy; covers `kathara/bind:9.11.5` |
| G15 | `main-labs/interdomain-routing/frr/bgp-announcement/kathara-lab_bgp-announcement` | 2/3 | Smallest `kathara/frr:9` lab (static facets golden; route state nondet) |
| G16 | `main-labs/intradomain-routing/frr/frrouting-introduction/kathara-lab_frr` | 3/4 | FRR intro lab, vtysh config mount |
| G17 | `main-labs/intradomain-routing/frr/ospf/kathara-lab_ospf/kathara-lab_ospf_frr-singlearea` | 5/4 | Multi-router OSPF topology shape (static facets golden) |
| G18 | `main-labs/interdomain-routing/quagga/bgp-announcement/kathara-lab_bgp-announcement` | 2/3 | Smallest `kathara/quagga` lab |
| G19 | `main-labs/intradomain-routing/quagga/quagga-introduction/kathara-lab_quagga` | 3/4 | Quagga intro, mirrors G16 for the second routing image |
| G20 | `main-labs/data-center-routing/data-center-vxlan/kathara-lab_vxlan-base` | 4/3 | frr+core mix, vxlan interfaces in-device |
| G21 | `main-labs/data-center-routing/data-center-bgp/kathara-lab_data-center-bgp` | 18/24 | The one LARGE stress lab: 18 devices/24 CDs, unquoted `ipv6=True`, `sysctl net.ipv4.fib_multipath_hash_policy` on many devices |
| G22 | `main-labs/p4/01-reflector` | 2/1 | Smallest `kathara/bmv2` lab; multiple `sysctl` entries per device (repeat-option append semantics) |
| G23 | `main-labs/sdn-openflow/pox/01-pox-controller` | 4/3 | `kathara/pox`+`kathara/sdn` images; explicit MAC; zero metadata |
| G24 | `main-labs/sdn-openflow/pox/08-pox-arp-handler` | 4/3 | `bridged="True"` quoted-boolean variant |

Determinism caveat: for G14–G21 (dynamic routing daemons) the harness must golden only the
deterministic facets (exit codes, docker inspect, `ip link`/`ip addr`, MAC/DriverOpts wiring,
mounts, `/etc/hosts`) — not daemon-learned `ip route` state. G7 (static routes) is the lab where
the full `ip route` table is a safe byte-exact golden.
Explicitly excluded: `kathara-lab_bgp-transit-as-redistribute-bgp-nondeterministic`
(nondeterministic by design) and the three `tutorials/kathara-external/*` labs (`lab.ext` →
ExternalLink DEFERRED; reuse them as `FeatureNotAvailable` error-path fixtures).

## Distinct images (pre-pull list for the harness)

Corpus-wide (11 tags); the golden set needs the first 10 — `kathara/frr:latest` is used by exactly
one lab (`frr/bgp-multi-homed-stub-large`) and is excluded from the set:

1. `kathara/base` — the default when no `image=` is given (12 labs) + 6 explicit references
2. `kathara/core` — 39 labs
3. `kathara/quagga` — 19 labs
4. `kathara/frr:9` — 16 labs
5. `kathara/bmv2` — 10 labs
6. `kathara/apache` — 9 labs
7. `kathara/pox` — 9 labs
8. `kathara/sdn` — 9 labs
9. `lscr.io/linuxserver/wireshark` — 6 labs (third-party, ~1 GB, web-GUI container; runs headless)
10. `kathara/bind:9.11.5` — 4 labs
11. `kathara/frr:latest` — 1 lab (NOT in golden set)

(Sizes not verified — no Docker Hub query made; harness should `docker pull` the 10 set images
once and snapshot `docker image ls --digests` so golden runs pin digests.)

## lab.conf features UNCOVERED by the corpus → synthetic scenarios required

The documented option set (`docs/kathara-lab.conf.5.ronn`, validated in `Machine.add_meta`) is:
`image, mem, cpus, port, bridged, ipv6, exec, sysctl, env, shell, num_terms, ulimit, privileged,
volume`. The corpus (and therefore any golden set drawn from it) exercises only
`image, port, bridged, ipv6, sysctl, num_terms(=0)`. One synthetic per uncovered flag (spec §9A),
all on `kathara/base`, 1–3 devices, no images beyond the pre-pull list:

| Synthetic | lab.conf content (core lines) | Golden assertion |
|---|---|---|
| `syn-mem` | `pc1[mem]=64m` | `docker inspect` HostConfig.Memory == 67108864 |
| `syn-cpus` | `pc1[cpus]=0.5` | HostConfig.NanoCPUs == 500000000 |
| `syn-exec` | two lines: `pc1[exec]="touch /a"`, `pc1[exec]="touch /b"` | both files exist in device; append (not overwrite) semantics; ordering |
| `syn-env` | `pc1[env]="FOO=bar"`, `pc1[env]="FOO=baz"` (overwrite) | Config.Env contains `FOO=baz`; warning on overwrite |
| `syn-ulimit` | `pc1[ulimit]=nofile=1024:2048`, `pc1[ulimit]=nproc=512` (soft-only → hard=soft) | HostConfig.Ulimits entries |
| `syn-shell` | `pc1[shell]=/bin/sh` | shell used by `kathara connect`/exec path (also a Layer B vector) |
| `syn-num-terms` | `pc1[num_terms]=2` (corpus only ever has `=0`) | terminal-spawn count with terminals enabled; no-op headless |
| `syn-privileged` | `pc1[privileged]=true` | HostConfig.Privileged == true (+ the privileged-disallowed error path) |
| `syn-volume` | `pc1[volume]="<host>|/guest|rw"` and 2-part form (defaults `ro`) | Mounts entry with mode; `ro` default |
| `syn-lab-name` | `LAB_NAME="goldlab"` header (used by ZERO corpus labs) | lab hash / container + network naming derived from LAB_NAME |
| `syn-shared-startup` | `shared.startup` touching a file (absent corpus-wide) | file present in every device |
| `syn-lab-dep` | 3 devices + `lab.dep` forcing reverse start order (absent corpus-wide) | startup ordering observable via timestamps written by each `.startup` |
| `syn-shutdown` | `pc1.shutdown` + `shared.shutdown` (absent corpus-wide; executed per `DockerMachine.py` SHUTDOWN_COMMANDS) | side-effect file on `lclean` teardown |
| `syn-test-folder` | a `_test/` folder in the lab dir (reserved name in `utils.RESERVED_MACHINE_NAMES`) | folder not deployed/mounted as a machine; plus error path: `_test[0]="A"` in lab.conf → ValueError |
| `syn-port-udp` | `pc1[port]="6000:53/udp"` (corpus has only tcp) | NetworkSettings.Ports udp mapping; also `sctp` variant |
| `syn-unknown-opt` | `pc1[bogus]=1` (silently stored as generic meta today) | pin current silent-pass-through behaviour so the Go port doesn't diverge |

Error-path candidates (not goldens): the 3 `tutorials/kathara-external/*` labs (`lab.ext` →
`FeatureNotAvailable`), malformed `sysctl` (non-`net.` namespace), malformed `env`/`ulimit`
values, reserved machine names (`shared`, `_test`) in lab.conf, empty `lab.conf` (raises IOError),
collision-domain names with non-word characters.
