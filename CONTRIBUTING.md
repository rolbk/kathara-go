# Contributing to Kathará for Go

Thanks for helping improve the Go implementation of Kathará.

## Development setup

Use the Go version declared in `go.mod`. Docker is required only for commands
and integration checks that create labs. On Unix, the full package suite also
needs tmux and should be run as a regular user.

```console
go build ./...
go test ./...
(cd tools/goldenharness && go test ./...)
(cd tools/cmdparity && go test ./...)
```

Before sending a change, run:

```console
gofmt -w path/to/changed.go
go vet ./...
go test ./...
(cd tools/goldenharness && go test ./...)
(cd tools/cmdparity && go test ./...)
```

## Changes to behaviour

Kathará lab files and CLI output are interfaces used by courses and automation.
Please include focused tests for parser, model, command-line, or backend changes
as appropriate. For a Docker-backed behaviour change, use the verification
tools under `tools/` on a disposable host: they can create privileged
containers and network resources.

## Pull requests

Keep pull requests narrowly scoped, explain user-visible changes, and avoid
committing generated binaries, local configuration, or Docker state. If a
change affects the Python compatibility package, test both its unit suite and
the Go command it invokes.
