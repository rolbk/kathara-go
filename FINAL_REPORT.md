# Kathará Python → Go Port — Final Report

**Date:** 2026-08-12 · **Spec:** `docs/port/PORT_SPEC.md` (v3.8.3 baseline) · **Repo:** `github.com/KatharaFramework/kathara-go` (local)
**Done-criteria (owner-locked):** all Linux-verifiable gates + built release artifacts; Windows/macOS ported and cross-compiling but runtime-untested; CI authored, not run.

## 1. Gate status — all machine-verifiable gates GREEN

| Gate | Result | Evidence |
|---|---|---|
| **Layer A — golden suite** | **47/47** (0 mismatches; 1 *expected* reference drift: `err-lab-ext`, deferred feature by spec §0.3) | `test/phase5-run{1,2,3}.log`; history 45/47 → 47/47 → 47/47 (re-proof after Phase 6) |
| Layer A oracle determinism | 47 scenarios recorded twice from Python 3.8.3, byte-identical | Phase 1 four-pass proof; `tools/goldenharness/NORMALIZATION.md` |
| **Layer B — parser vectors** | **142/142 on BOTH implementations** (Go `labfile` + Python client `LabParser`) | `labfile/vector_test.go`; `tools/vectorcheck/check_{python,client}.py` |
| **Layer C — test-suite mining** | All 36 upstream test files mined to SDK-agnostic expectations, realized as table tests/goldens | `/root/kathara/analysis/EXPECTATIONS-{docker,k8s,core}.md` |
| **Layer D gate 1 — kathara-lab-checker** | **PASS — all 6 CSVs byte-identical** to the Python-recorded baseline (78/78 good, 69/80 bad, same 11 failures; flake reproduced on the oracle → inherent) | `/root/kathara/goldens/layer-d/bgp_ipv4_ipv6/` |
| **Layer D gate 2 — API tutorials** | **PASS** — `getting-started` and `managing-filesystem` run unmodified (latter under a real PTY, fair-compared vs Python) | Layer D agent logs |
| **Kubernetes/Megalos (owner-extended)** | **Full parity in k3d**: real pods, VXLAN collision domains, ICMP between devices; identical VNI (13776144), identical namespace, identical `pod-template-hash` (byte-identical pod templates); identical failure modes without CNI | Phase 8 report; `PROGRESS.md` |
| Static gates | `go vet`, `staticcheck`, `errcheck`, `gofmt` clean; `go test -race` all 15 packages; goleak in terminal/pty tests | every commit since the first |
| Cross-compilation | `GOOS=windows`, `GOOS=darwin` build clean (all packages incl. ConPTY leg) | Phase 9 + fix-batch runs |
| Interop | Go client cleanly undeployed a Python-deployed lab (identical hash/naming) | Layer D report |

## 2. Deliverables

- **Go implementation** — all 15 spec §3.1 packages plus the §0.2 sanctioned rebuilds (settings `config` command + bubbletea TUI; built-in terminal multiplexer + tmux driver + external adapters; vstart family as one-device sugar; typed Meta; ordered interfaces; explicit registry; constructed `*Client` with `context.Context` throughout; JSON CLI per frozen contract).
- **Python client** (`python/`) — model + parsers **byte-identical to upstream 3.8.3**, subprocess manager over the JSON CLI, same 40 exception classes; proven by the shared 142-vector corpus and Layer D.
- **Release artifacts** (`dist/`, `python/dist/`) — 5 static binaries (linux amd64/arm64, darwin amd64/arm64, windows amd64) via goreleaser snapshot; 5 platform-tagged wheels (`build_wheels.py`, version-ordering guard included). Linux wheel smoke-tested in a fresh venv: `kathara -v`, `kathara check` (real container), client import + in-memory Lab all pass.
- **Verification infrastructure** — `tools/goldenharness` (record/verify/diff, 47 scenarios incl. 21 synthetics + 6 error fixtures, NORMALIZATION.md with per-rule provenance); 142 Layer B vectors + dual-implementation runners; constraint registers (`docs/port/*.tsv`); frozen contracts (`PACKAGE_GRAPH`, `JSON_CLI_CONTRACT`, `ERROR_CODES`, `RULINGS`).
- **CI** (authored, not run — by design): 5-platform build/lint/test matrix, dispatch-only golden workflow (Kathara-Labs pinned to the goldens' commit), goreleaser release workflow.

## 3. Divergence inventory

- **`DIVERGENCES.md` — ~105 numbered entries**: upstream Python bugs reproduced bug-for-bug (tombstone crashes, ulimit message variable, BOM/`LAB_*=` parser crashes, FolderParser dir-kill, …); environment-forced representation differences (Go SDK capability canonicalization, tar header deltas, CPython 3.13 tarfile fields); accidental-side-effect drops recorded per §10 (root-logger basicConfig; CLI chrome through the subprocess client).
- **`PROPOSED-DIVERGENCES.md`**: deterministic-MAC driver-opt (would fix the spec §9 assumption upstream falsified), FolderParser sort canonicalization, tar-padding unification, image autocomplete return, config-reset notes.
- **Spec corrections discovered**: §9's deterministic-MAC anchor is false for 3.8.3 + katharanp_vde (goldens pivot to DriverOpts wiring); §4.1's "sparse from lab.conf" comment is unreachable; §0.3's deferral boundary needed two carve-outs (`get_iptables_version`, privilege-drop no-op).

## 4. Post-1.0 backlog (recommended order)

1. Resource stats + `linfo` (spec §0.3) — inventory fields already ship.
2. `lab.ext` + `netns/` (Linux build tag) → unlocks the IXP Digital Twin gate (Layer D gate 3).
3. macOS/Windows **runtime** verification (code cross-compiles; ConPTY leg designed + compiled, untested) and code signing (Apple Developer + Windows cert — spec §8 budget item).
4. Version-check webhooks (if wanted) — restores settings-TUI image autocomplete.
5. Small items: `connect_tty` CLI-chrome divergence (daemon-mode would fix), cross-node EVPN data-plane probe (k3d rung left unprobed), tar-padding unification, `DockerImage.py:194/:204` arch log lines (needs sorted-set repr ruling), release version scheme ≥ 4.0.0 (PyPI ordering guard is built into `build_wheels.py`).

## 5. Capacity actuals

Fits the spec §13 model: analysis + oracle ≈ 2 windows; porting (15 packages, 12 §10 loops) ≈ 5 windows; Phase 5 convergence was *two* fix batches (the smoke's transport wiring + one log-rendering class) — far below the §11 worst case, exactly because the oracle was built first.
