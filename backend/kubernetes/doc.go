// Package kubernetes is the port of `manager/kubernetes/`: the Megalos
// backend, one implementation of [kathara.Manager].
//
// The Python subsystem is seven classes plus four helper modules, and the split
// survives file-for-file (PACKAGE_GRAPH.md §2.8):
//
//   - manager.go    `KubernetesManager.py`   — the [kathara.Manager] surface
//   - machine.go    `KubernetesMachine.py`   — devices as Deployments/Pods
//   - link.go       `KubernetesLink.py`      — collision domains as NADs
//   - namespace.go  `KubernetesNamespace.py` — the scenario's namespace
//   - secret.go     `KubernetesSecret.py`    — the private-registry Secret
//   - configmap.go  `KubernetesConfigMap.py` — the hostlab ConfigMap
//   - config.go     `KubernetesConfig.py`    — kubeconfig, and the VNI seed
//   - execstream.go `exec_stream/`           — the streaming exec handle
//   - stats.go      `stats/`                 — inventory (sampling deferred)
//   - tty.go        `terminal/`              — the transport half of the rebuild
//
// Plus four files with no Python home: naming.go (the frozen resource-name
// schema), startup.go (the postStart/preStop script templates), pool.go (the
// `multiprocessing.dummy.Pool` shape) and pack.go (`Machine.pack_data`, which
// PACKAGE_GRAPH.md §2.2 assigns to a `model/pack.go` that does not exist yet —
// see the file's own note).
//
// # What this package does not decide
//
// It is handed everything ambient: the settings, the event dispatcher and the
// model defaults arrive in a [kathara.Config] (PORT_SPEC §0.2 #10), so nothing
// here reads a singleton. Python's `deploy_machines` writes
// `Setting.get_instance().open_terminals = False` — a process-wide mutation
// (k8s-backend.md G20) — which here is a no-op the backend does not need:
// Megalos never opens a terminal because [Manager.ConnectTTY] is the only
// terminal path and the CLI drives it explicitly. DIVERGENCES.md records it.
//
// It writes no output — the startup-log dump goes to the caller's writer
// (JSON_CLI_CONTRACT.md §1.3) — and it runs no terminal UI: [Manager.ConnectTTY]
// hands back a [kathara.TTYSession] and `term` draws on it (PACKAGE_GRAPH.md
// D-5).
//
// # Registration
//
// There is no `init()`. [Backend] returns the registry row and `cmd/kathara`
// calls `kathara.Register` on it from `backends_all.go`, which is what lets the
// `//go:build nok8s` build leave `client-go` out of the binary entirely
// (PACKAGE_GRAPH.md §1.2, PORT_SPEC §0.2 #8). Nothing outside this package's
// own files imports `k8s.io/client-go`.
//
// # Concurrency
//
// Every fan-out reproduces `multiprocessing.dummy.Pool` + `chunk_list` exactly:
// see [runChunked]. CONCURRENCY.tsv rows 14-25 pin it per site, including the
// two that have no Docker counterpart — the pod watcher that runs alongside the
// deploy fan-out and the VNI reservation map that Python hosts in a
// `multiprocessing.Manager` process and that is a mutex here.
package kubernetes
