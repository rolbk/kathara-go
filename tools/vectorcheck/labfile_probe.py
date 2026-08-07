#!/usr/bin/env python3
"""Oracle probe for the `labfile` package.

Records, from CPython and from Kathara 3.8.3's own patterns, the four
primitives the parser vectors only sample:

  * the `lab.conf` device-line regex, `\\3` backreference and all, over a
    generated line corpus (the hand-rolled Go matcher has to agree on every
    one, captures included);
  * the collision-domain name test `^\\w+$`, which is Unicode-aware;
  * the `lab.dep` line regex, whose `\\s?` and `(\\w+ ?)+` are likewise Unicode;
  * `bytes.decode('utf-8')`, whose UnicodeDecodeError position, span and reason
    escape uncaught from both file parsers;
  * `depgen.has_loop` / `depgen.flatten` over random small graphs, whose output
    order is line-order sensitive and therefore not reproducible by a canonical
    topological sort.

Usage:
    /root/kathara/pyvenv/bin/python tools/vectorcheck/labfile_probe.py \\
        > labfile/testdata/labfile_oracle.json
"""

import argparse
import json
import os
import random
import re
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                "..", "..", "..", "kathara-python", "src"))

from Kathara.trdparty.depgen import depgen  # noqa: E402

# parser/netkit/LabParser.py:44-47, verbatim.
DEVICE_RE = re.compile(
    r"^(?P<key>[a-z0-9_]{1,30})\[(?P<arg>\w+)\]=([\"\']?)(?P<value>[^\"\']+)(\3)(\s+\#.*)?$"
)
# parser/netkit/LabParser.py:67, verbatim.
CD_RE = re.compile(r"^\w+$")
# parser/netkit/DepParser.py:56, verbatim.
DEP_RE = re.compile(r"^(?P<key>\w+):\s?(?P<deps>(\w+ ?)+)$")


# --------------------------------------------------------------------------
# lab.conf device lines
# --------------------------------------------------------------------------

KEYS = ["pc1", "PC1", "pc-1", "p", "_", "0", "shared", "_test", "",
        "a" * 30, "a" * 31, "pc1 ", " pc1", "pC1", "pc1_x"]
ARGS = ["0", "00", "01", "1", "0_1", "1_0", "_0", "0_", "1__0", "eth0", "image",
        "", "1a", "0x1", "\u0663", "\u00b2", "\u00e9th", "\u0438\u043c\u044f",
        "99999999999999999999", "-1", "+1", " 1", "1 ", "exec", "sysctl"]
VALUES = [
    "A", "'A'", '"A"', "'A", 'A"', "'A\"", "''", '""', "", "A B", "A/00:11:22:33:44:55",
    "A/", "/A", "A//B", "A/B/C", "kathara/frr # note", "'A' # note", "'A'# note",
    '"A"  #', "'A'\t# c", "A#b", "A #", "abc #x\"y", "abc #x'y", "'A'  ", "  A  ",
    "a\u00a0b", "\u00e9", "A\u001cB", "x'y\"z", "'x\"y'", "\"x'y\"",
]

ALPHABET = "abc019_[]='\"# \t/:.-\u00b2\u0663\u00e9\u00a0\u001c\\$"

# How much random fuzz to add on top of the systematic corpus, and how finely to
# sample the multi-byte UTF-8 space. `--full` multiplies the first and widens the
# second; the committed fixture is the default, which is what keeps it a
# reviewable size. Both settings have been run and agree with the Go port.
FUZZ = 1
SAMPLE = [0x00, 0x41, 0x7F, 0x80, 0x9F, 0xA0, 0xBF, 0xC0, 0xFF]
SAMPLE_FULL = [0x00, 0x41, 0x7F, 0x80, 0x8F, 0x9F, 0xA0, 0xBF, 0xC0, 0xE0, 0xFF]
TAIL = [0x41, 0x80, 0xBF]


def device_line_cases():
    lines = []
    for key in KEYS:
        for arg in ARGS:
            lines.append("%s[%s]=A" % (key, arg))
    for arg in ARGS:
        for value in VALUES:
            lines.append("pc1[%s]=%s" % (arg, value))
    for value in VALUES:
        lines.append("pc1[0]=%s" % value)
        lines.append("  pc1[image]=%s  " % value)
        lines.append("\tpc1[0]=%s\n" % value)
        lines.append("pc1[0]=%s\r\n" % value)
    # Lines that are not device lines at all, so the Go matcher has to reject
    # them for the same reasons Python does.
    lines += ["", "#", "# c", "  # c", "LAB_NAME=x", "LAB_WEB=http://a/?b=c",
              "broken line", "\ufeffpc1[0]='A'", "pc1[]=A", "pc1[a]b]=A",
              "pc1[a][b]=A", "pc1[0]", "pc1[0]=", "=A", "pc1=A", "pc1[0]==A"]

    rng = random.Random(20260806)
    for _ in range(1200 * FUZZ):
        n = rng.randint(0, 18)
        lines.append("".join(rng.choice(ALPHABET) for _ in range(n)))
    for _ in range(1200 * FUZZ):
        n = rng.randint(0, 10)
        lines.append("pc1[0]=" + "".join(rng.choice(ALPHABET) for _ in range(n)))
    for _ in range(600 * FUZZ):
        n = rng.randint(0, 8)
        lines.append("pc1[" + "".join(rng.choice(ALPHABET) for _ in range(n)) + "]=A")

    out = []
    seen = set()
    for line in lines:
        if line in seen:
            continue
        seen.add(line)
        match = DEVICE_RE.search(line.strip())
        case = {"line": line, "matched": match is not None}
        if match:
            case["key"] = match.group("key")
            case["arg"] = match.group("arg")
            case["value"] = match.group("value")
        out.append(case)
    return out


def cd_name_cases():
    names = ["A", "A B", "A#", "", "_", "123", "\u0663", "\u00b2", "\u00e9", "shared",
             "A\n", "\nA", "A\nB", "A\r", "a-b", "a.b", "a_b", "\u4e00", "\u2160",
             "\u00bd", "A/", "A ", " A", "\u00a0", "A\u00a0B"]
    rng = random.Random(7)
    for _ in range(600 * FUZZ):
        n = rng.randint(0, 6)
        names.append("".join(rng.choice(ALPHABET) for _ in range(n)))
    out = []
    seen = set()
    for name in names:
        if name in seen:
            continue
        seen.add(name)
        out.append({"name": name, "matched": CD_RE.search(name) is not None})
    return out


# --------------------------------------------------------------------------
# lab.dep lines
# --------------------------------------------------------------------------

DEP_ALPHABET = "ab1_: \t,#\u00a0\u0663\u00e9\n"


def dep_line_cases():
    lines = ["pc1: pc2", "pc1:pc2", "pc1:\tpc2", "pc1:  pc2", "pc1 : pc2",
             "pc1: pc2 pc3", "pc1: pc2  pc3", "pc1: pc2 ", "pc1:", "pc1: ",
             "pc1: pc2,pc3", "pc1: pc2 # c", "# c", "  # c", "", "   ",
             "pc\u0661: pc\u00e9", "a:b c d", "a:\u00a0b", "a::b", ":b", "a:b:c"]
    rng = random.Random(99)
    for _ in range(1200 * FUZZ):
        n = rng.randint(0, 12)
        lines.append("".join(rng.choice(DEP_ALPHABET) for _ in range(n)))

    out = []
    seen = set()
    for line in lines:
        if line in seen:
            continue
        seen.add(line)
        stripped = line.strip()
        match = DEP_RE.search(stripped)
        case = {"line": line, "matched": match is not None}
        if match:
            case["key"] = match.group("key")
            case["deps"] = [x.strip() for x in match.group("deps").split(" ")]
        out.append(case)
    return out


# --------------------------------------------------------------------------
# UTF-8 decoding
# --------------------------------------------------------------------------

def utf8_cases():
    blobs = []
    for b in range(256):
        blobs.append(bytes([b]))
        blobs.append(b"A" + bytes([b]))
    for b0 in range(0x80, 0x100):
        for b1 in SAMPLE:
            blobs.append(bytes([b0, b1]))
    for b0 in range(0xE0, 0xF0):
        for b1 in SAMPLE:
            for b2 in SAMPLE:
                blobs.append(bytes([b0, b1, b2]))
    for b0 in range(0xF0, 0xF8):
        for b1 in SAMPLE:
            for b2 in SAMPLE:
                for b3 in TAIL:
                    blobs.append(bytes([b0, b1, b2, b3]))
    blobs += [b"", b"pc1[0]='A'\n", "\ufeffpc1[0]='A'\n".encode("utf-8"),
              b"\xed\xa0\x80", b"\xed\xa0", b"\xf0\x90\x80\x80", b"\xc2",
              b"ok\xff\xfe", b"\xe0\xa0", b"\xf4\x8f\xbf\xbf", b"\xf4\x90\x80\x80"]

    out = []
    seen = set()
    for blob in blobs:
        if blob in seen:
            continue
        seen.add(blob)
        case = {"bytes": blob.hex()}
        try:
            blob.decode("utf-8")
            case["ok"] = True
        except UnicodeDecodeError as e:
            case["ok"] = False
            case["message"] = str(e)
            case["start"] = e.start
            case["end"] = e.end
            case["reason"] = e.reason
        out.append(case)
    return out


# --------------------------------------------------------------------------
# depgen
# --------------------------------------------------------------------------

def depgen_cases():
    graphs = [
        [],
        [("pc1", ["pc2", "pc3"]), ("pc3", ["pc2"])],
        [("d", ["b", "c"]), ("b", ["a"]), ("c", ["a"])],
        [("d", ["c", "b"]), ("c", ["a"]), ("b", ["a"])],
        [("pc1", ["pc2"]), ("pc2", ["pc3"]), ("pc3", ["pc1"])],
        [("pc1", ["pc1"])],
        [("pc1", ["ghost"])],
        [("a", ["b"]), ("b", ["c"]), ("c", ["d"]), ("d", ["e"])],
        [("a", ["b", "c", "d"]), ("b", ["c", "d"]), ("e", ["f", "g"])],
    ]

    nodes = ["a", "b", "c", "d", "e", "f"]
    rng = random.Random(4242)
    for _ in range(600 * FUZZ):
        keys = rng.sample(nodes, rng.randint(1, len(nodes)))
        graph = []
        for key in keys:
            count = rng.randint(1, 3)
            graph.append((key, [rng.choice(nodes) for _ in range(count)]))
        graphs.append(graph)

    out = []
    seen = set()
    for graph in graphs:
        signature = json.dumps(graph)
        if signature in seen:
            continue
        seen.add(signature)

        depdict = {}
        for key, deps in graph:
            depdict[key] = list(deps)

        case = {"graph": [{"key": k, "deps": d} for k, d in graph],
                "has_loop": depgen.has_loop(depdict)}
        if not case["has_loop"]:
            case["flatten"] = depgen.flatten(depdict)
        out.append(case)
    return out


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--full", action="store_true",
                        help="widen the fuzz and the UTF-8 sample (not committed)")
    args = parser.parse_args()
    if args.full:
        global FUZZ, SAMPLE
        FUZZ = 4
        SAMPLE = SAMPLE_FULL

    document = {
        "device_lines": device_line_cases(),
        "cd_names": cd_name_cases(),
        "dep_lines": dep_line_cases(),
        "utf8": utf8_cases(),
        "depgen": depgen_cases(),
    }
    json.dump(document, sys.stdout, sort_keys=True, ensure_ascii=False,
              separators=(",", ":"))
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
