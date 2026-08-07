// Package kathara is the public API of the port: the [Manager] contract a
// backend implements, the [Client] that selects one, and the value types the
// two exchange.
//
// It is the port of `foundation/manager/` (the `IManager`, `IExecStream`,
// `ITerminalSession`, `IMachineStats` and `ILinkStats` interfaces),
// `foundation/manager/ManagerFactory.py` plus `foundation/factory/Factory.py`
// (the reflection this package replaces with an explicit registry), and
// `manager/Kathara.py` (the facade this package replaces with a constructed
// value). PACKAGE_GRAPH.md §2.7 is the row-by-row mapping.
//
// # Four sanctioned redesigns land here
//
// §0.2 #7 deletes reflection. `ManagerFactory` built the dotted module path
// `Kathara.manager.{manager_type}.{Manager_type}Manager`, imported it and
// called `getattr`; a typo in `manager_type` surfaced as an `ImportError`
// whose `.name` the entrypoint then printed. registry.go replaces the whole
// mechanism with [Registry], an ordered table that `cmd/kathara` fills by
// name in the declared order docker → kubernetes (SYNTHESIS.md C-5).
//
// §0.2 #10 deletes the singleton. `Kathara.get_instance()` froze the backend
// choice for the process and raised `InstantiationError("This class is a
// singleton!")` on a second construction; [NewClient] returns a value, and two
// clients over two backends can coexist. What did *not* change is when the
// backend is built: [NewClient] constructs it eagerly, exactly as
// `Kathara.__init__` did, so a Docker daemon that is not running is an error
// from the constructor and not from the first operation
// (analysis/manager-foundation.md §7 gotcha 9).
//
// §0.2 #11 puts a context.Context first on every operation, [Manager] and
// [Client] alike, including the ones that only read.
//
// §0.3 reduces the two stats interfaces to inventory. [MachineStats] and
// [LinkStats] are plain records; the resource sampling their Python
// counterparts did in `update()` is deferred, and [MachineStats.Update] and
// [LinkStats.Update] answer `FeatureNotAvailable` rather than silently doing
// nothing.
//
// # The facade delegates and nothing else
//
// [Client] has one method per `manager/Kathara.py` method and every body is a
// forward. That is not a simplification: all thirty `self.manager.…` bodies in
// `manager/Kathara.py:63-605` are literally `self.manager.<name>(<same args,
// positionally>)`, with no validation, no transformation and no added
// behaviour (analysis/manager-foundation.md §1.11). The
// `check_required_single_not_none_var` calls that guard the lab-identifier
// triple live in the *backends* — eleven sites in `DockerManager.py`, eleven
// in `KubernetesManager.py` — and putting them here instead would move an
// observable error: `get_machine_stats` is a Python *generator function*, so
// its guard does not run until the first `next()`, and a client-side check
// would raise it at call time. [LabRef.RequireSingle] and [LabRef.AtMostOne]
// are provided for the backends to call at the sites Python calls them.
//
// # Errors
//
// errors.go re-exports the `kerrors` taxonomy so the PORT_SPEC §4.3 surface
// (`kathara.ErrLabNotFound`, `kathara.MachineError`, …) holds even though the
// taxonomy itself lives one package lower to break the `kathara` ↔ `model`
// cycle (PACKAGE_GRAPH.md D-1).
//
// # Dependencies
//
// `kerrors`, `model`, `settings` and `event`, and nothing else of the module
// (PACKAGE_GRAPH.md §1.2). In particular not `internal/util`, which is why the
// two-line "count the non-zero fields" of [LabRef.RequireSingle] is spelled
// here instead of calling `util.CheckRequiredSingleNotNoneVar`, and why
// resolving a [LabRef.Name] to a scenario hash is the backend's job: that needs
// `util.GenerateURLSafeHash`.
//
// The *test* binary does import `internal/util`, once, to put the two copies of
// the guard side by side so a drift fails a test rather than a user's terminal
// (`TestLabRefMessagesMatchUtil`). It is recorded as DIVERGENCES.md #54; the
// shipped package's edge list is the four above.
package kathara
