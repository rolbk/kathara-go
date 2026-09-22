# Kathará for Go

Kathará is a container-based network emulator for teaching, experimenting with,
and testing network topologies. This repository is a Go implementation of the
Kathará command-line experience and its Docker and Kubernetes backends. It is
compatible with the familiar Kathará lab layout, so supported `lab.conf`,
startup, and shared-files workflows can be used without conversion.

The project targets the behaviour and command vocabulary of Kathará 3.8.3
while distributing the emulator as a single Go binary. A small Python package
is included for programs that use the Kathará model API.

## Highlights

- Start, stop, inspect, connect to, and execute commands in virtual devices and
  complete labs.
- Run labs with Docker; select the Kubernetes backend through settings when a
  Kubernetes cluster is available.
- Use standard Kathará lab directories and device startup files.
- Automate commands with structured JSON and JSON Lines output.
- Build a static binary for Linux, macOS, or Windows.

## Quick start

Kathará uses Docker to create devices and collision domains. Install Docker,
make sure your user can access its daemon, then build the command:

```console
git clone https://github.com/rolbk/kathara-go.git
cd kathara-go
mkdir -p bin
go build -o bin/kathara ./cmd/kathara
./bin/kathara check
```

Start an existing lab directory:

```console
./bin/kathara lstart -d /path/to/lab
./bin/kathara list
./bin/kathara lclean -d /path/to/lab
```

Run `./bin/kathara --help` to see the main command set. The `check` command is
the best first diagnostic if Docker, its network plugin, or local settings need
attention.

## Building and testing

The module declares the required Go version in `go.mod`.

```console
go build ./...
go test ./...
```

The ordinary test suite does not require a running Docker daemon. On Unix, the
full suite also exercises tmux and should be run as a regular user. End-to-end
lab verification is intentionally kept separate because it creates privileged
containers and network state; see `tools/goldenharness` when working on backend
behaviour.

## Python API

The `python/` directory supplies the established `Kathara` model API while
delegating emulator operations to this binary. For local development:

```console
mkdir -p bin
go build -o bin/kathara ./cmd/kathara
python3 -m pip install ./python
export KATHARA_BIN="$PWD/bin/kathara"
```

See [python/README.md](python/README.md) for the supported API boundary and
Python-specific development commands.

## Project layout

| Path | Purpose |
| --- | --- |
| `cmd/kathara` | Command-line application |
| `backend/docker` | Docker implementation |
| `backend/kubernetes` | Kubernetes implementation |
| `labfile`, `model`, `vfs` | Lab parsing, domain model, and filesystem support |
| `kathara`, `settings`, `term` | Public orchestration API, configuration, and terminals |
| `python` | Optional Python compatibility package |
| `test` and package tests | Regression fixtures and automated tests |
| `tools` | Maintainer-only verification utilities |

## Current scope

The Docker workflow is the primary path. The Kubernetes backend is available
but needs cluster credentials and configuration. A few legacy operations are
explicitly unavailable rather than silently approximated: external links via
`lab.ext`, the `linfo` command, and live resource-statistics sampling.

## Versioning

An unstamped Go build reports the 3.8.3 compatibility baseline. Release builds
take their version from the Git tag, and the Python wheels are stamped with the
same version. The wheel builder requires a release version newer than 3.8.3 so
existing Python installations can upgrade to it.

## Contributing

Please format and test changes before opening a pull request:

```console
gofmt -w path/to/changed.go
go vet ./...
go test ./...
```

Kathará is licensed under the [GNU GPL v3.0](LICENSE).
