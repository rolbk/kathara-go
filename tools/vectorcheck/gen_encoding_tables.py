#!/usr/bin/env python3
"""Code generator for internal/util/encoding_tables.go and encoding_tree.go.

Everything binaryornot's `is_binary` consults that is *data* rather than logic
is lifted straight out of the reference environment, so this implementation never restates a
table by hand:

  * `binary_extensions.csv` -> the extension set consulted before the file is
    ever opened;
  * `binary_formats.csv`    -> the magic-signature prefixes;
  * `binaryornot/tree.py`   -> the trained decision tree, transpiled from its
    Python AST (it is itself generated code, "Do not edit by hand");
  * CPython's own `gb2312` / `big5` / `shift_jis` / `euc-jp` / `euc-kr` codecs
    -> exact accept/reject tables for the five `try_<cjk>` features. These are
    enumerated by asking CPython to decode every candidate byte sequence, so
    the Go validators accept exactly the byte strings CPython accepts rather
    than an approximation of the standards.

Usage:
    /root/kathara/pyvenv/bin/python tools/vectorcheck/gen_encoding_tables.py
"""

import ast
import csv
import os
import sys
from importlib.resources import files

HERE = os.path.dirname(os.path.abspath(__file__))
UTIL = os.path.normpath(os.path.join(HERE, "..", "..", "internal", "util"))

CJK = [
    ("gb2312", "gb2312"),
    ("big5", "big5"),
    ("shiftJIS", "shift_jis"),
    ("eucJP", "euc-jp"),
    ("eucKR", "euc-kr"),
]


def decodes(codec: str, bs: bytes) -> bool:
    try:
        bs.decode(codec)
        return True
    except Exception:  # noqa: BLE001
        return False


def ranges(values: "list[int]") -> "list[tuple[int, int]]":
    out = []
    for v in values:
        if out and v == out[-1][1] + 1:
            out[-1][1] = v
        else:
            out.append([v, v])
    return [(a, b) for a, b in out]


def build_codec(codec: str) -> dict:
    """Enumerate CPython's accept set for one multibyte codec.

    The CPython multibyte decoders are deterministic and never backtrack: the
    lead byte fixes the sequence length, then the whole sequence either maps to
    a character or raises. So the accept set is fully described by a per-lead
    length plus, for multi-byte leads, the set of accepted continuations.
    """
    lead_len = [0] * 256
    trail = {}
    trail3 = {}

    for b in range(256):
        if decodes(codec, bytes([b])):
            lead_len[b] = 1
            continue
        ok2 = [t for t in range(256) if decodes(codec, bytes([b, t]))]
        if ok2:
            lead_len[b] = 2
            trail[b] = ranges(ok2)
            continue
        # 3-byte lead (only EUC-JP 0x8F in practice). Probe the full second
        # byte space against one known-good third byte, then enumerate.
        found = {}
        for t in range(256):
            ok3 = [u for u in range(256) if decodes(codec, bytes([b, t, u]))]
            if ok3:
                found[t] = ranges(ok3)
        if found:
            lead_len[b] = 3
            trail3[b] = found
    return {"lead_len": lead_len, "trail": trail, "trail3": trail3}


# --------------------------------------------------------------------------
# Decision-tree transpiler. tree.py is a single function whose body is nothing
# but nested `if features[i] <= C:` / `else:` / `return bool`, so a direct AST
# walk is exact -- no float reformatting, the literal text of every threshold
# is copied through verbatim.
# --------------------------------------------------------------------------
def transpile_tree(src_path: str) -> str:
    src = open(src_path, encoding="utf-8").read()
    mod = ast.parse(src)
    fn = next(n for n in mod.body if isinstance(n, ast.FunctionDef) and n.name == "is_binary")
    body = [n for n in fn.body if not (isinstance(n, ast.Expr) and isinstance(n.value, ast.Constant))]
    lines: "list[str]" = []

    def walk(nodes, indent: int) -> None:
        pad = "\t" * indent
        for node in nodes:
            if isinstance(node, ast.Return):
                assert isinstance(node.value, ast.Constant) and isinstance(node.value.value, bool)
                lines.append(f"{pad}return {'true' if node.value.value else 'false'}")
            elif isinstance(node, ast.If):
                test = node.test
                assert isinstance(test, ast.Compare) and len(test.ops) == 1
                assert isinstance(test.ops[0], ast.LtE), "tree uses only <="
                left = test.left
                assert isinstance(left, ast.Subscript) and left.value.id == "features"
                idx = left.slice.value
                thr = ast.get_source_segment(src, test.comparators[0])
                lines.append(f"{pad}if f[{idx}] <= {thr} {{")
                walk(node.body, indent + 1)
                if node.orelse:
                    lines.append(f"{pad}}} else {{")
                    walk(node.orelse, indent + 1)
                lines.append(f"{pad}}}")
            else:  # pragma: no cover
                raise AssertionError(f"unexpected node {ast.dump(node)[:80]}")

    walk(body, 1)
    return "\n".join(lines)


# --------------------------------------------------------------------------
def main() -> int:
    data = files("binaryornot.data")

    with data.joinpath("binary_extensions.csv").open() as fh:
        exts = sorted({row["extension"].strip().lower() for row in csv.DictReader(fh)})

    with data.joinpath("binary_formats.csv").open() as fh:
        sigs = []
        for row in csv.DictReader(fh):
            h = row["magic_hex"].strip()
            if h:
                sigs.append((row["format"], h))
    # Longest first so the generated Go reads as a real prefix table; the
    # Python loader's order is irrelevant (it is an `any` over all of them).
    sigs.sort(key=lambda s: (-len(s[1]), s[0]))

    import binaryornot
    from importlib import metadata

    version = metadata.version("binaryornot")

    out = []
    out.append("// Code generated by tools/vectorcheck/gen_encoding_tables.py. DO NOT EDIT.")
    out.append(f"// Source: binaryornot {version} (binary_extensions.csv, binary_formats.csv)")
    out.append("// plus CPython's own gb2312/big5/shift_jis/euc-jp/euc-kr codec accept sets.")
    out.append("")
    out.append("package util")
    out.append("")
    out.append("// binaryExtensions is binaryornot's binary_extensions.csv, the set consulted")
    out.append("// by has_binary_extension before the file is opened at all.")
    out.append("var binaryExtensions = map[string]struct{}{")
    for e in exts:
        out.append(f'\t{e!r}: {{}},'.replace("'", '"'))
    out.append("}")
    out.append("")
    out.append("// binaryMagics is binaryornot's binary_formats.csv: a chunk whose prefix")
    out.append("// matches any of these is binary before the decision tree is consulted.")
    out.append("var binaryMagics = [][]byte{")
    for name, h in sigs:
        body = "".join(f"\\x{h[i:i+2]}" for i in range(0, len(h), 2))
        out.append(f'\t[]byte("{body}"), // {name}')
    out.append("}")
    out.append("")

    for goname, codec in CJK:
        t = build_codec(codec)
        out.append(f"// {goname}Lead gives the sequence length CPython's {codec!r} codec commits to")
        out.append("// for each lead byte; 0 means the byte cannot start a character.")
        out.append(f"var {goname}Lead = [256]uint8{{")
        for i in range(0, 256, 16):
            out.append("\t" + " ".join(f"{v}," for v in t["lead_len"][i : i + 16]))
        out.append("}")
        out.append("")
        out.append(f"// {goname}Trail lists, per two-byte lead, the accepted continuation ranges.")
        out.append(f"var {goname}Trail = map[byte][]byteRange{{")
        for lead in sorted(t["trail"]):
            rs = ", ".join(f"{{0x{a:02x}, 0x{b:02x}}}" for a, b in t["trail"][lead])
            out.append(f"\t0x{lead:02x}: {{{rs}}},")
        out.append("}")
        out.append("")
        if t["trail3"]:
            out.append(f"// {goname}Trail3 lists, per three-byte lead and second byte, the accepted")
            out.append("// third-byte ranges.")
            out.append(f"var {goname}Trail3 = map[byte]map[byte][]byteRange{{")
            for lead in sorted(t["trail3"]):
                out.append(f"\t0x{lead:02x}: {{")
                for b2 in sorted(t["trail3"][lead]):
                    rs = ", ".join(f"{{0x{a:02x}, 0x{b:02x}}}" for a, b in t["trail3"][lead][b2])
                    out.append(f"\t\t0x{b2:02x}: {{{rs}}},")
                out.append("\t},")
            out.append("}")
            out.append("")
        else:
            out.append(f"// {goname} has no three-byte sequences.")
            out.append(f"var {goname}Trail3 map[byte]map[byte][]byteRange")
            out.append("")

    with open(os.path.join(UTIL, "encoding_tables.go"), "w", encoding="utf-8") as fh:
        fh.write("\n".join(out) + "\n")

    tree_src = os.path.join(os.path.dirname(binaryornot.__file__), "tree.py")
    tree_go = [
        "// Code generated by tools/vectorcheck/gen_encoding_tables.py. DO NOT EDIT.",
        f"// Transpiled from binaryornot {version}'s tree.py, itself generated by",
        "// binaryornot's scripts/train_detector.py. Thresholds are copied verbatim.",
        "",
        "package util",
        "",
        "// binaryDecisionTree is binaryornot.tree.is_binary: the trained classifier",
        "// over the 24-element feature vector computeBinaryFeatures produces.",
        "func binaryDecisionTree(f *[binaryFeatureCount]float64) bool {",
        transpile_tree(tree_src),
        "}",
    ]
    with open(os.path.join(UTIL, "encoding_tree.go"), "w", encoding="utf-8") as fh:
        fh.write("\n".join(tree_go) + "\n")

    sys.stdout.write(
        "wrote encoding_tables.go (%d extensions, %d magics) and encoding_tree.go\n" % (len(exts), len(sigs))
    )
    return 0


if __name__ == "__main__":
    sys.exit(main())
