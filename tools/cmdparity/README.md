# Command-parity tool

This is a maintainer utility for comparing command flows between the Python
Kathará release and this implementation. It is not part of the shipped CLI.

It runs selected workflows twice against each implementation, captures command
output and Docker state, and reports nondeterminism or behavioural differences.
The recordings are deliberately local and ignored by Git.

```console
go build -o /tmp/kgo ./cmd/kathara
cd tools/cmdparity
go run . -record -compare
go run . -record -flow exec
go run . -compare
```

The default Python oracle is `/root/kathara/pyvenv/bin/python`. Use
`-py-bin /path/to/python` for a different environment containing Kathará 3.8.3,
and `-go-bin` if the Go binary is not `/tmp/kgo`.

The tool owns the Docker resources it creates and force-cleans Kathará state
before and after each flow. On Linux it also temporarily replaces the invoking
user's real `~/.config/kathara.conf` and restores it on normal exit. Run it only
on a disposable development host.
