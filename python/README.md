# Kathará — Python client

The Python half of the Kathará Go port (`PORT_SPEC.md` §7). `pip install kathara`
still gives you a working `kathara` command and the same Python API.

```python
from Kathara.model.Lab import Lab
from Kathara.manager.Kathara import Kathara

lab = Lab("Getting Started")
pc1 = lab.new_machine("pc1", **{"image": "kathara/base"})
pc2 = lab.new_machine("pc2", **{"image": "kathara/base"})
lab.connect_machine_to_link(pc1.name, "A")
lab.connect_machine_to_link(pc2.name, "A")
lab.create_startup_file_from_list(pc1, ["ip address add 10.0.0.1/24 dev eth0"])

Kathara.get_instance().deploy_lab(lab)
```

## What is Python and what is Go

| Layer | Where it lives | Why |
|---|---|---|
| `Kathara.model` (`Lab`, `Machine`, `Link`, `Interface`, the filesystem mixins) | **Python**, ported from v3.8.3 | ~770 SLOC of pure data manipulation that never touches Docker. Scenario construction — most real usage — needs no manager at all. |
| `Kathara.parser.netkit` (`LabParser`, `DepParser`, `FolderParser`, `OptionParser`) | **Python**, ported from v3.8.3 | Consumers import it directly. Kept honest against the Go parser by the shared Layer B conformance vectors. |
| `Kathara.exceptions` | **Python**, unchanged classes | Downstream code catches them by name and reads `str(e)`. |
| `Kathara.setting.Setting` | **Python**, same file and schema | Reads and writes the same `kathara.conf` as the binary. |
| `Kathara.manager.Kathara` | **Python facade over the Go binary** | Every operation is a `kathara --format json`/`jsonl` subprocess call. |
| Everything else (deploy, Docker, Kubernetes, terminals, the CLI) | **Go** | One static binary, no Python at runtime. |

The model files are byte-identical to Kathará v3.8.3 apart from one additive,
read-only `Lab.hash_seed` property, and `exceptions.py` adds exactly one class
(`KatharaError`, the mandated fallback for error codes a newer binary may
introduce).

## The wire protocol

`docs/port/JSON_CLI_CONTRACT.md` (frozen) and `docs/port/ERROR_CODES.md`
(frozen) are normative. In particular:

* `deploy_lab(lab)` serialises the in-memory scenario to a tar of a normal
  Kathará scenario directory and pipes it to
  `kathara lstart --from-archive - --name <name> --format json`. The `--name`
  value is the string the scenario's hash was derived from, so the deployed
  identity is exactly `lab.hash`.
* `exec(...)` streams `kathara exec --format jsonl` and yields the same
  `(stdout, stderr)` chunk tuples the Docker SDK demux produced.
* Error envelopes are decoded back into the original exception classes, code by
  code, with `str(e)` byte-identical to what v3.8.3 raised.

## Locating the binary

The wheels ship the Go binary in `<wheel>.data/scripts/`, so `pip` puts it on
`PATH` next to the interpreter. Discovery order: `$KATHARA_BIN`, the running
interpreter's scripts directory, `PATH`, then `Kathara/bin/`.

## What 1.0 does not do

Deliberate, bounded losses (`PORT_SPEC.md` §7.2, §0.3). All of them raise
`NotSupportedError` rather than doing something approximate:

* `deploy_link`, `undeploy_link`, `copy_files`, `retrieve_files`,
  `get_link_api_object(s)`, `get_lab_from_api`, `update_lab_from_api`,
  `get_link(s)_stats`, `check_image`;
* `get_machine(s)_api_objects` return inventory dicts, not docker-py
  `Container` objects, which cannot cross a process boundary;
* `get_machine(s)_stats` return the inventory fields only — resource sampling
  is deferred;
* `ExternalLink` / `lab.ext` scenarios error with `FeatureNotAvailable`;
* a scenario with a *disconnected* interface (a hole left by
  `remove_interface`) cannot be serialised to a `lab.conf` and is refused by
  `deploy_lab`.

## Open question: the release version number

`Kathara/version.py` currently says `1.0.0`, matching the Go port's own
versioning. The Python distribution it replaces is at **3.8.3**, and downstream
projects pin against that series — kathara-lab-checker requires
`kathara>=3.8.1`. A `kathara 1.0.0` on PyPI therefore does **not** satisfy its
own main consumer, and `pip install kathara_lab_checker` would resolve to the
old pure-Python 3.8.3 instead of the port.

Either the release is tagged `>= 3.9.0` (continuing the distribution's series,
whatever the Go binary calls itself) or downstream pins have to be relaxed
first. This is a tagging decision, not a code one: the wheel version comes from
the git tag via `build_wheels.py --version` / `dist/metadata.json`.

## Development

```console
# the conformance vectors, against this package, in a venv without upstream Kathara
python tools/vectorcheck/check_client.py

# the unit tests, which drive a fake binary that speaks the JSON contract
cd python && python -m unittest discover -s tests -t tests

# the release wheels, from a goreleaser dist tree
python python/build_wheels.py --dist dist --out python/dist
```
