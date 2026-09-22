#!/usr/bin/env python3
"""Layer B authority runner: replay the parser conformance vectors against the
REAL Python Kathara parsers and compare the result with each vector's
`expected.json`.

Python is the reference for these vectors. If a vector differs, inspect the
result, re-record it with `--update` when appropriate, and document the
observed behaviour in `labfile/testdata/vectors/README.md`.

Usage:
    /root/kathara/pyvenv/bin/python tools/vectorcheck/check_python.py [options]

Options:
    --vectors DIR   vector corpus root (default: ../../labfile/testdata/vectors)
    --filter SUBSTR only run vectors whose id contains SUBSTR
    --update        rewrite expected.json from the observed Python behaviour
    --verbose       print a line per passing vector too

Exit status is 0 only when every vector passes.
"""

import argparse
import json
import logging
import os
import shutil
import sys
import tempfile

try:
    from Kathara.parser.netkit.DepParser import DepParser
    from Kathara.parser.netkit.FolderParser import FolderParser
    from Kathara.parser.netkit.LabParser import LabParser
    from Kathara.parser.netkit.OptionParser import OptionParser
except ImportError:  # pragma: no cover
    sys.stderr.write(
        "Cannot import Kathara. Run this with the oracle interpreter, e.g.\n"
        "  /root/kathara/pyvenv/bin/python tools/vectorcheck/check_python.py\n"
    )
    raise

HERE = os.path.dirname(os.path.abspath(__file__))
DEFAULT_VECTORS = os.path.normpath(os.path.join(HERE, "..", "..", "labfile", "testdata", "vectors"))

TYPED_META_KEYS = ("exec_commands", "sysctls", "envs", "ports", "ulimits", "volumes")


# --------------------------------------------------------------------------
# The canonical serializer. Exactly one implementation, shared by every kind
# of vector; `gen`/`--update` and the comparison path both go through it.
# --------------------------------------------------------------------------

def ser_meta(meta):
    out = {
        "exec_commands": list(meta.get("exec_commands", [])),
        "sysctls": dict(meta.get("sysctls", {})),
        "envs": dict(meta.get("envs", {})),
        "ports": {"%d/%s" % (k[0], k[1]): v for k, v in meta.get("ports", {}).items()},
        "ulimits": {k: {"soft": v["soft"], "hard": v["hard"]}
                    for k, v in meta.get("ulimits", {}).items()},
        "volumes": {k: {"guest_path": v["guest_path"], "mode": v["mode"]}
                    for k, v in meta.get("volumes", {}).items()},
        "extra": {},
    }
    for key, value in meta.items():
        if key in TYPED_META_KEYS:
            continue
        out["extra"][key] = value
    return out


def ser_machine(lab, machine):
    interfaces = {}
    for num, iface in machine.interfaces.items():
        interfaces[str(num)] = {
            "cd": iface.link.name if iface is not None else None,
            "mac": iface.mac_address if iface is not None else None,
        }
    startup = "%s.startup" % machine.name
    return {
        "interfaces": interfaces,
        "meta": ser_meta(machine.meta),
        "has_dir": machine.fs is not None,
        "startup_file": startup if lab.fs.exists(startup) else None,
    }


def ser_lab(lab):
    return {
        "name": lab.name,
        # The hash is derived from the lab path when there is no LAB_NAME, which
        # makes it tmpdir-dependent; it is only pinned when a name was parsed.
        "hash": lab.hash if lab.name is not None else None,
        "description": lab.description,
        "version": lab.version,
        "author": lab.author,
        "email": lab.email,
        "web": lab.web,
        "machines": {name: ser_machine(lab, m) for name, m in lab.machines.items()},
        "machine_order": list(lab.machines.keys()),
        "links": {name: {"machines": [m for m in lab.machines
                                      if m in link.machines]}
                  for name, link in lab.links.items()},
        "link_order": list(lab.links.keys()),
        "general_options": dict(lab.general_options),
        "global_machine_metadata": dict(lab.global_machine_metadata),
        "has_dependencies": lab.has_dependencies,
    }


# --------------------------------------------------------------------------

class WarningCollector(logging.Handler):
    def __init__(self):
        super().__init__(level=logging.WARNING)
        self.messages = []

    def emit(self, record):
        if record.levelno >= logging.WARNING:
            self.messages.append(record.getMessage())


def materialize(vector_dir, vec, dest):
    src = os.path.join(vector_dir, "input")
    if os.path.isdir(src):
        for entry in sorted(os.listdir(src)):
            if entry == ".gitkeep":
                continue
            shutil.copy2(os.path.join(src, entry), os.path.join(dest, entry))
    # Empty directories cannot live in git, so folder layouts are declared.
    for d in vec.get("dirs", []):
        os.makedirs(os.path.join(dest, d), exist_ok=True)


def run_vector(vector_dir, vec):
    """Return (actual_dict, ) - the observed behaviour in vector JSON shape."""
    collector = WarningCollector()
    root_logger = logging.getLogger()
    old_level = root_logger.level
    root_logger.addHandler(collector)
    root_logger.setLevel(logging.WARNING)

    actual = {}
    tmp = tempfile.mkdtemp(prefix="kathara-vector-")
    try:
        parser = vec["parser"]
        if parser != "options":
            materialize(vector_dir, vec, tmp)
        try:
            if parser == "lab":
                lab = LabParser.parse(tmp, conf_name=vec.get("conf_name", "lab.conf"))
                actual["lab"] = ser_lab(lab)
            elif parser == "dep":
                actual["dep"] = DepParser.parse(tmp)
            elif parser == "folder":
                actual["lab"] = ser_lab(FolderParser.parse(tmp))
            elif parser == "options":
                actual["options"] = OptionParser.parse(vec.get("options"))
            elif parser == "lab+dep":
                lab = LabParser.parse(tmp, conf_name=vec.get("conf_name", "lab.conf"))
                deps = DepParser.parse(tmp)
                actual["dep"] = deps
                if deps:
                    lab.apply_dependencies(deps)
                actual["lab"] = ser_lab(lab)
            else:
                raise SystemExit("unknown parser kind %r in %s" % (parser, vector_dir))
        except Exception as e:  # noqa: BLE001 - record the observed exception type
            actual = {"error": {"class": type(e).__name__, "message": str(e)}}
    finally:
        root_logger.removeHandler(collector)
        root_logger.setLevel(old_level)
        shutil.rmtree(tmp, ignore_errors=True)

    if collector.messages:
        actual["warnings"] = collector.messages
    return actual


def normalize(doc, order_sensitive):
    """Fill in defaults and, for order-insensitive vectors, sort the orderings."""
    doc = json.loads(json.dumps(doc))
    doc.setdefault("warnings", [])
    if not order_sensitive and "lab" in doc:
        lab = doc["lab"]
        lab["machine_order"] = sorted(lab.get("machine_order", []))
        lab["link_order"] = sorted(lab.get("link_order", []))
        for link in lab.get("links", {}).values():
            link["machines"] = sorted(link["machines"])
    return doc


def canon(obj):
    return json.dumps(obj, sort_keys=True, indent=2, ensure_ascii=False) + "\n"


def diff_lines(expected, actual):
    import difflib
    return list(difflib.unified_diff(
        canon(expected).splitlines(), canon(actual).splitlines(),
        fromfile="expected", tofile="python", lineterm=""))


def discover(root):
    out = []
    for dirpath, dirnames, filenames in os.walk(root):
        if "vector.json" in filenames:
            out.append(dirpath)
            dirnames[:] = []
        else:
            dirnames.sort()
    return sorted(out)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--vectors", default=DEFAULT_VECTORS)
    ap.add_argument("--filter", default=None)
    ap.add_argument("--update", action="store_true")
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    root = os.path.abspath(args.vectors)
    vector_dirs = discover(root)
    if not vector_dirs:
        sys.stderr.write("no vectors found under %s\n" % root)
        return 2

    passed = failed = updated = 0
    failures = []
    by_category = {}

    for vdir in vector_dirs:
        vid = os.path.relpath(vdir, root)
        if args.filter and args.filter not in vid:
            continue
        with open(os.path.join(vdir, "vector.json")) as f:
            vec = json.load(f)
        with open(os.path.join(vdir, "expected.json")) as f:
            expected = json.load(f)

        category = vid.split(os.sep)[0]
        by_category.setdefault(category, 0)
        by_category[category] += 1

        actual = run_vector(vdir, vec)
        order_sensitive = vec.get("order_sensitive", True)
        norm_expected = normalize(expected, order_sensitive)
        norm_actual = normalize(actual, order_sensitive)

        if norm_expected == norm_actual:
            passed += 1
            if args.verbose:
                print("PASS %s" % vid)
            continue

        failed += 1
        failures.append((vid, diff_lines(norm_expected, norm_actual)))
        if args.update:
            out = dict(actual)
            if not out.get("warnings"):
                out.pop("warnings", None)
            with open(os.path.join(vdir, "expected.json"), "w") as f:
                f.write(canon(out))
            updated += 1

    for vid, diff in failures:
        print("FAIL %s" % vid)
        for line in diff:
            print("    " + line)
        print()

    print("-" * 60)
    for category in sorted(by_category):
        print("  %-12s %d vectors" % (category, by_category[category]))
    print("%d passed, %d failed%s" % (passed, failed,
                                      " (%d expected.json rewritten)" % updated if args.update else ""))
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
