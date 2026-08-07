// Package docker is the port of `manager/docker/`: the Docker backend, one
// implementation of [kathara.Manager].
//
// The Python subsystem is five classes plus three helper modules, and the
// split survives file-for-file (PACKAGE_GRAPH.md §2.8):
//
//   - manager.go    `DockerManager.py`  — the [kathara.Manager] surface
//   - machine.go    `DockerMachine.py`  — containers
//   - link.go       `DockerLink.py`     — networks
//   - image.go      `DockerImage.py`    — image check/pull/update
//   - plugin.go     `DockerPlugin.py`   — the network-plugin lifecycle
//   - execstream.go `exec_stream/`      — the streaming exec handle
//   - stats.go      `stats/`            — inventory (sampling deferred, §0.3)
//   - tty.go        `terminal/`         — the transport half of the rebuild
//
// Plus one carve-out that has no Python home here: `iptables_linux.go` is
// `os/Networking.get_iptables_version`, pulled out of the deferred `os/` bundle
// because `DockerPlugin` needs it on the 1.0 path (PACKAGE_GRAPH.md D-6,
// SYNTHESIS C-3).
//
// # What this package does not decide
//
// It is handed everything ambient: the settings, the event dispatcher and the
// model defaults arrive in a [kathara.Config] (PORT_SPEC §0.2 #10), so nothing
// here reads a singleton and two Managers over two configurations can coexist.
// It writes no output — the startup-log dump goes to the caller's writer
// (JSON_CLI_CONTRACT.md §1.3) — and it runs no terminal UI: [Manager.ConnectTTY]
// hands back a [kathara.TTYSession] and `term` draws on it (PACKAGE_GRAPH.md
// D-5).
//
// # Registration
//
// There is no `init()`. [Backend] returns the registry row and `cmd/kathara`
// calls `kathara.Register` on it, because registration order is the declared
// docker → kubernetes order and an import graph has no order
// (PACKAGE_GRAPH.md §1.2).
//
// # Concurrency
//
// Every fan-out reproduces `multiprocessing.dummy.Pool` + `chunk_list` exactly:
// see [runChunked]. The shape is neither fail-fast nor complete-then-aggregate,
// and CONCURRENCY.tsv rows 5-13 pin it per site.
package docker
