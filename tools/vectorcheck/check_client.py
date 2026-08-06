#!/usr/bin/env python3
"""Layer B, third implementation: replay the parser conformance vectors against
the **Python client package** (`PORT_SPEC.md` §7, `python/Kathara`).

`labfile/testdata/vectors/README.md`: "These vectors are shared by the Go
`labfile` package and the Python client's `LabParser`. They are what keeps the
two implementations from drifting." This runner is the client's half of that.

It deliberately reuses `check_python.py` unchanged — same materialisation, same
serialiser, same comparison, same exit status — and only decides *which*
`Kathara` package the import resolves to. That is what makes "the same 142
vectors" literally true rather than approximately true.

Usage:

    # in a venv where the upstream Kathara is NOT installed
    /root/kathara/clientvenv/bin/python tools/vectorcheck/check_client.py

Options: `--client DIR` (default `<repo>/python`) plus every flag of
`check_python.py` (`--vectors`, `--filter`, `--verbose`, `--update`).

`--update` is accepted but should essentially never be used from here: Python
3.8.3 is the authority for these vectors (README "Python is the truth"), so a
disagreement means **the client is wrong**, not the vector.

Independence check: the runner refuses to start if a *different* `Kathara` is
already importable in this interpreter — that is what would silently turn the
run into a re-test of the upstream package. An already-importable Kathara that
IS the client directory (an editable or wheel install of this very package) is
fine and the run proceeds. Pass `--allow-upstream` to override, which is useful
only for A/B-ing the two implementations by hand.
"""

import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_CLIENT = os.path.normpath(os.path.join(HERE, "..", "..", "python"))


def _take_option(argv, name, default):
    """Pull `--name VALUE` (or `--name=VALUE`) out of argv, returning the value."""
    value = default
    remaining = []
    i = 0
    while i < len(argv):
        arg = argv[i]
        if arg == name:
            if i + 1 >= len(argv):
                sys.stderr.write("%s requires a value\n" % name)
                raise SystemExit(2)
            value = argv[i + 1]
            i += 2
            continue
        if arg.startswith(name + "="):
            value = arg[len(name) + 1:]
            i += 1
            continue
        remaining.append(arg)
        i += 1

    return value, remaining


def _take_flag(argv, name):
    if name in argv:
        return True, [a for a in argv if a != name]
    return False, argv


def main() -> int:
    argv = sys.argv[1:]
    client_dir, argv = _take_option(argv, "--client", DEFAULT_CLIENT)
    allow_upstream, argv = _take_flag(argv, "--allow-upstream")

    client_dir = os.path.abspath(client_dir)
    if not os.path.isdir(os.path.join(client_dir, "Kathara")):
        sys.stderr.write("no Kathara package under %s\n" % client_dir)
        return 2

    # Independence: before touching sys.path, check what `Kathara` currently
    # resolves to. Anything other than the client under test means the run
    # would not prove anything about the client.
    if not allow_upstream:
        import importlib.util
        try:
            spec = importlib.util.find_spec("Kathara")
        except (ImportError, ValueError):  # pragma: no cover - broken namespace pkg
            spec = None

        if spec is not None and spec.origin:
            already = os.path.abspath(os.path.dirname(spec.origin))
            if already != os.path.join(client_dir, "Kathara"):
                sys.stderr.write(
                    "refusing to run: `Kathara` is already importable from %s, which is not the client\n"
                    "under test. This runner exists to exercise the client package, so run it in an\n"
                    "environment where no other Kathara is installed (e.g. /root/kathara/clientvenv), or\n"
                    "pass --allow-upstream to override.\n" % already
                )
                return 2

    sys.path.insert(0, client_dir)
    sys.path.insert(0, HERE)

    import check_python  # noqa: E402  (must follow the sys.path surgery)
    import Kathara  # noqa: E402

    resolved = os.path.abspath(os.path.dirname(Kathara.__file__))
    expected = os.path.join(client_dir, "Kathara")
    if resolved != expected:
        sys.stderr.write("Kathara resolved to %s, expected %s\n" % (resolved, expected))
        return 2

    print("client package: %s" % resolved)
    print("interpreter:    %s" % sys.executable)

    sys.argv = [sys.argv[0]] + argv
    return check_python.main()


if __name__ == "__main__":
    sys.exit(main())
