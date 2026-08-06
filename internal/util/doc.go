// Package util is the port of Kathará's `Kathara/utils.py`,
// `Kathara/version.py` and `Kathara/trdparty/strtobool` (PORT_SPEC §3.1 row 2,
// PACKAGE_GRAPH.md §2.1).
//
// utils.py is a grab-bag, and so is this package: an identity chain that names
// every container and network, a handful of platform probes, the tar payload
// format, the CRLF/BOM normaliser and two version parsers. What holds it
// together is that everything here is a leaf — the package imports `kerrors`,
// the standard library and the two `golang.org/x` modules PACKAGE_GRAPH.md §5
// pins for it, and nothing else in the module (PACKAGE_GRAPH.md §1.1), so
// every other package can depend on it.
//
// # The identity chain is byte-exact
//
// [GenerateURLSafeHash], [Slug] and [GetCurrentUserName] compose into the
// strings that end up in Docker container names, network names and the `user`
// label, and in Kubernetes namespaces. PORT_SPEC §0.4 forbids changing them:
// one byte of drift and a Go build cannot see scenarios a Python install
// started. They are pinned against the live 3.8.3 oracle by
// testdata/utils/expected.json, regenerated with
//
//	/root/kathara/pyvenv/bin/python tools/vectorcheck/utils_probe.py
//
// # Several CPython primitives are part of the contract
//
// [PyInt], [PyIsDigit] and [PythonRepr] reproduce `int(str)`, `str.isdigit()`
// and `repr(str)` because Kathará feeds user text straight into them and
// dispatches on whether they raised, which RULINGS.md OQ-14a makes binding.
// Their acceptance sets are swept over the whole code space against the oracle
// by testdata/pyint/expected.json, regenerated with
//
//	/root/kathara/pyvenv/bin/python tools/vectorcheck/pyint_probe.py
//
// For the same reason — the result is observable, byte for byte — [pyWhich]
// ports `shutil.which` instead of calling [os/exec.LookPath], [pyLower] ports
// `str.lower()` instead of calling [strings.ToLower], and ntpath.go ports the
// four `ntpath` primitives the Windows build needs instead of reaching for
// path/filepath. Each doc comment names what the stdlib substitute would have
// changed.
//
// # Python-only functions that are deliberately absent
//
// Each of these exists in utils.py and has no Go counterpart. The list is
// exhaustive; anything else in utils.py is ported.
//
//   - `class_for_name` — the `importlib` reflection behind `Factory`.
//     Sanctioned deletion, PORT_SPEC §0.2 #7: Go uses explicit registry maps.
//   - `list_chunks` / `chunk_list` — the CPU-count chunking that shaped every
//     `Pool.map` fan-out. Sanctioned deletion, PORT_SPEC §3.1 row 2: Go uses a
//     bounded errgroup, so the per-chunk barrier disappears with the chunks
//     (CONCURRENCY.tsv, utils.py:94 and :102).
//   - `check_python_version` — an interpreter check with no Go analogue.
//   - `pywintypes_import_stub` / `pywintypes_import_win` / `import_pywintypes` —
//     a fake module so `except pywintypes.error` could be spelled once on every
//     OS. Typed errors in the Docker backend replace it.
//   - `is_platform` — a one-line `sys.platform ==` comparison. Its call sites
//     (DockerLink.py:333,368, LstartCommand.py:193) all ask the same question,
//     "is this Linux"; Go compares [runtime.GOOS] directly or splits the file
//     on a build tag, both of which the compiler checks and this would not.
//     This is the one deletion PACKAGE_GRAPH.md §2.1 does not list, so it is
//     recorded in PROPOSED-DIVERGENCES.md and awaits a contract-owner errata.
//
// One *branch* is dropped rather than a whole function:
// [GetExecutablePath] has no equivalent of utils.py:71-72, which prefixes
// `sys.executable` when the path ends in `kathara.py`. A Go binary has no
// interpreter to name.
//
// utils.py's platform constants survive as [MacOS], [Windows] and [Linux].
// The fourth one, `LINUX2`, does not: it is the Python-2 era value of
// `sys.platform` and [runtime.GOOS] never reports it.
package util
