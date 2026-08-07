// Package labfile is the network-scenario file parsers of
// `Kathara/parser/netkit` plus the vendored `trdparty/depgen` topological sort
// (PACKAGE_GRAPH.md row 7).
//
// It turns the four things a scenario directory can say about itself into
// `model` values:
//
//   - [ParseLab] reads `lab.conf` — the devices, their interfaces and their
//     options — and is the only entry point that runs `check_integrity`;
//   - [ParseDep] reads `lab.dep` and flattens it into a boot order;
//   - [ParseFolder] is the `-F/--force-lab` fallback: every subdirectory is a
//     device;
//   - [ParseOptions] is the CLI's `-o/--pass` value parser.
//
// `lab.ext` is DEFERRED to post-1.0 (PORT_SPEC §0.3): [CheckExt] reports its
// presence as [kerrors.NewFeatureNotAvailable] and there is no ExtParser.
//
// # Faithfulness
//
// The parsers are a byte-for-byte port, and three of the Python behaviours they
// reproduce look like bugs because they are (DIVERGENCES.md 4-8):
//
//   - a trailing `# comment` is swallowed into an unquoted value, so
//     `pc1[image]=kathara/frr # note` stores the comment too;
//   - a UTF-8 BOM makes line 1 a syntax error;
//   - a `LAB_*` value containing a second `=` crashes with a bare ValueError.
//
// The lab.conf device-line pattern uses a `\3` backreference, which RE2 cannot
// express, so [matchDeviceLine] hand-rolls the quote matching *including its
// backtracking* — see that function for why backtracking is observable and not
// a detail. [ParseFolder] hand-rolls the other CPython matcher in scope,
// `glob` + `fnmatch`, because `FolderParser` builds its pattern by
// concatenation and the scenario path is therefore part of it: a `[` in the
// path is a character class, not a bracket. The interface-vs-meta dispatch is CPython's `int()` and nothing
// else (RULINGS.md OQ-14a): PEP 515 underscores and Unicode decimal digits are
// interface numbers, and no error other than a failed integer parse may route a
// line to the meta path.
//
// Every one of those decisions is pinned by the 142-vector Layer B corpus in
// `testdata/vectors`, which `vector_test.go` replays and which
// `tools/vectorcheck/check_python.py` replays against real Kathará 3.8.3.
//
// # Logging
//
// `logging.warning` becomes [log/slog.Warn] on the default logger, with the
// Python message verbatim: the duplicate-meta warning of `LabParser.py:76` and
// the empty-file notice of `DepParser.py:37`.
package labfile
