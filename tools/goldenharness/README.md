# Golden harness

This maintainer tool verifies Docker-backed Kathará labs against checked-in
observable snapshots. It runs a configured Kathará command, captures the lab's
containers, networks, command output, and in-device probes, then compares that
state with `test/goldens`.

The default manifest includes 24 scenarios from the separate
`KatharaFramework/Kathara-Labs` repository. Check it out and pass its location
with `-labs-root` or `KATHARA_LABS_ROOT`.

Build the emulator and harness from the repository root:

```console
mkdir -p bin
go build -trimpath -o bin/kathara ./cmd/kathara
(cd tools/goldenharness && go build -trimpath -o ../../bin/goldenharness .)
```

```console
# Inspect available scenarios.
./bin/goldenharness list

# Verify the Go binary against existing snapshots.
KATHARA_CMD=./bin/kathara ./bin/goldenharness verify -v
```

To prove a new recording deterministic, record it from Kathará 3.8.3 and then
verify a second oracle run into a retained scratch directory before comparing
the Go binary.

The harness creates privileged containers and network resources. Use a
disposable Linux host with a rootful Docker daemon; it is intentionally not a
routine unit-test command. On Linux, Kathará reads the invoking account's real
`~/.config/kathara.conf`; set `image_update_policy` to `Never` and prevent the
weekly release check before recording. See `NORMALIZATION.md` for that caveat
and for the fields canonicalised before comparison.
