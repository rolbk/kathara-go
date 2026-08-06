// Package model is the port of `Kathara/model/*` (PORT_SPEC §3.1 row 6): the
// in-memory network scenario — [Lab], [Machine], [Link], [Interface] and the
// typed [Meta] — plus the `foundation/model` filesystem mixins, whose bodies
// now live in `vfs` (§0.2 #9) and are re-exposed here as the methods Python
// callers used.
//
// The package holds no backend, `settings` or `event` import (PACKAGE_GRAPH.md
// §1.2). It imports `kerrors`, `internal/util` and `vfs`, and nothing else of
// the module.
//
// # Two sanctioned redesigns, and what had to survive them
//
// §0.2 #4 turns `Machine.interfaces` from an `OrderedDict[int, Interface]` into
// an ordered slice, because a Go map iteration reaching a container produces a
// wrong but running network. §0.2 #5 turns the `Dict[str, Any]` meta bag into a
// typed struct with pointer tri-states plus an [Meta.Extras] map for the keys
// nothing knows about (PACKAGE_GRAPH.md §0 ruling).
//
// Neither redesign was allowed to change behaviour, so three Python properties
// that look like accidents are reproduced deliberately:
//
//   - **Tombstones.** [Machine.RemoveInterface] does not delete the slot, it
//     nulls it (`model/Machine.py:136-141`). The number stays taken, `len()`
//     stays inflated — and auto-numbering is `len(interfaces)`, not
//     "first free" — iteration must skip the hole, and the two Lab methods that
//     do not skip it crash. See [Machine.Interfaces].
//   - **Lazy validation.** `mem`, `cpus`, `num_terms` and `ipv6` are stored raw
//     and parsed by their getters, so an invalid value surfaces at deploy time
//     and not at parse time. The typed [Meta] keeps the raw value in a [Scalar]
//     for exactly this reason; moving the parse forward would move every one of
//     those error messages to a different command.
//   - **Per-key precedence.** `image`, `mem`, `cpus`, `num_terms`, `ipv6` and
//     `privileged` consult [Lab.GlobalMachineMetadata] before the device's own
//     meta; `bridged` and `shell` do not (`model/Machine.py:437-443,541-547`).
//     The asymmetry is not unified here.
//
// # Errors, never panics
//
// Several Python paths in these files end in a builtin exception — the
// `AttributeError` a tombstone triggers in `get_links_from_machines`, the
// `TypeError` a string `bridged_iface` triggers in `check()`, the bare
// `ValueError` a `--5` sysctl value triggers. ERROR_CODES.md §1.2 gives
// `ValueError` a code and buckets `TypeError`/`AttributeError`/`KeyError` into
// `InternalError`; either way they are returned as ordinary Go errors here (see
// [PyRuntimeError]), never raised as panics, so a device holding a hole cannot
// take a fan-out down with it.
//
// # Settings are injected, never read from a singleton
//
// `Setting.get_instance()` backs five accessors in Python. The OQ-4 resolution
// (PACKAGE_GRAPH.md §1.1) injects them instead: [Defaults] is constructed by
// the caller and handed to the [Lab] constructor, which is why this package
// does not import `settings`.
//
// # Not here yet
//
// `Machine.pack_data` (`model/Machine.py:381`), which PACKAGE_GRAPH.md §2.2
// assigns to a `model/pack.go`, is not in this package. It needs two things
// that live below it and do not exist in the shape it wants: a bytes-level
// `convert_win_2_linux` (`internal/util` exposes it over a host *path*, because
// Python's WriteTarFS buffers on a real filesystem before writing the archive)
// and a tar writer that can emit directory members (`util.WriteTar` emits
// regular files only, which is all `pack_files_for_tar` ever needed). Building
// either one inside this package would duplicate a responsibility the register
// puts in `internal/util`. Its only consumers are the two backends'
// `copy_files`, neither of which is ported yet. The gap is tracked in
// PROPOSED-DIVERGENCES.md, which asks the contract owner to schedule it or move
// the symbol; it must not ship as a silent omission.
package model
