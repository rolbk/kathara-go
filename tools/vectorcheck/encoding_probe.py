#!/usr/bin/env python3
"""Layer B authority runner for the encoding / binary-detection spike."""

import argparse
import hashlib
import importlib
import json
import os
import random
import shutil
import sys
import tempfile

try:
    from binaryornot.check import is_binary
    from Kathara import utils
except ImportError:  # pragma: no cover
    sys.stderr.write(
        "Cannot import Kathara/binaryornot. Run this with the oracle interpreter, e.g.\n"
        "  /root/kathara/pyvenv/bin/python tools/vectorcheck/encoding_probe.py\n"
    )
    raise

HERE = os.path.dirname(os.path.abspath(__file__))
TESTDATA = os.path.normpath(os.path.join(HERE, "..", "..", "internal", "util", "testdata", "encoding"))
FIXTURE_DIR = os.path.join(TESTDATA, "fixtures")
DEFAULT_OUT = os.path.join(TESTDATA, "expected.json")

BOM8 = b"\xef\xbb\xbf"


def _prng(n: int, seed: int) -> bytes:
    """Deterministic filler bytes. Mersenne Twister is stable across CPython
    releases, so --regen-corpus reproduces the committed fixtures exactly."""
    return random.Random(seed).randbytes(n)


# --------------------------------------------------------------------------
# The corpus. Name -> bytes. The *name* is load-bearing: binaryornot >= 0.5
# consults the file extension before it ever opens the file, so several
# fixtures exist only to pin that (text bytes under a binary extension and
# vice versa).
# --------------------------------------------------------------------------
CORPUS: "dict[str, bytes]" = {
    # --- plain text, encodings -------------------------------------------
    "utf8_plain.txt": b"hello world\nsecond line\nthird line\n",
    "utf8_bom.txt": BOM8 + b"with bom\nline2\n",
    "utf8_bom_only.txt": BOM8,
    "utf8_multibyte.txt": "café 你好 \U0001f600 — done\n".encode("utf-8"),
    "utf8_invalid.txt": b"valid start \xc3\x28 invalid continuation byte\nsecond line\n",
    "latin1.txt": "café résumé naïve straße\nsecond líne\n".encode("latin-1"),
    "cp1252_curly.txt": "“smart” quotes — and… ‘more’\n".encode("cp1252"),
    "shift_jis.txt": "こんにちは世界\nテスト\n".encode("shift_jis"),
    "utf16le_bom.txt": b"\xff\xfe" + "hello utf16le\nsecond line\n".encode("utf-16-le"),
    "utf16be_bom.txt": b"\xfe\xff" + "hello utf16be\nsecond line\n".encode("utf-16-be"),
    "utf16le_nobom.txt": "hello utf16le no bom\nsecond line\n".encode("utf-16-le"),
    "utf16be_nobom.txt": "hello utf16be no bom\nsecond line\n".encode("utf-16-be"),
    "utf16le_short.txt": b"\xff\xfe" + "hi\n".encode("utf-16-le"),
    "utf32le_bom.txt": b"\xff\xfe\x00\x00" + "hello utf32\nline\n".encode("utf-32-le"),
    # --- line endings ------------------------------------------------------
    "crlf.txt": b"line1\r\nline2\r\nline3\r\n",
    "cr_only.txt": b"line1\rline2\rline3\r",
    "mixed_eol.txt": b"lf\ncrlf\r\ncr\rend\n",
    "lone_cr.txt": b"before\rafter with no trailing newline",
    "nl_cr.txt": b"a\n\rb\n\r\nc\n",
    "bom_crlf.txt": BOM8 + b"a\r\nb\r\nc\r\n",
    "cr_at_eof.txt": b"text body\r",
    "no_trailing_newline.txt": b"no newline at end of file",
    "whitespace_only.txt": b"\n\n\r\n\r\n\t \n",
    "tabs_ff_vt.txt": b"col1\tcol2\x0bvt\x0cff\x08bs\nnext line\n",
    "huge_single_line.txt": b"A" * 100000 + b"\n",
    "huge_line_crlf.txt": (b"B" * 4096 + b"\r\n") * 12,
    # --- realistic lab files ----------------------------------------------
    "pc1.startup": BOM8 + b"ip addr add 10.0.0.1/24 dev eth0\r\nip route add default via 10.0.0.254\r\n",
    "noext": b"#!/bin/sh\necho plain text with no extension\n",
    # --- NUL / control / high bytes ---------------------------------------
    "nul_text.txt": b"text\x00with\x00nuls\nplus more plain ascii text to pad the chunk out\n",
    "nul_heavy.bin": b"\x00" * 600,
    "single_nul.dat": b"\x00",
    "control_heavy.dat": bytes(range(1, 9)) * 80,
    "high_bytes.dat": bytes(range(0x80, 0x100)) * 4,
    # --- binary --------------------------------------------------------------
    "png_header.bin": b"\x89PNG\r\n\x1a\n" + _prng(504, 1),
    "elf_header.bin": b"\x7fELF" + _prng(508, 2),
    "gzip_header.bin": b"\x1f\x8b\x08" + _prng(509, 3),
    "random_nomagic.bin": bytes([0x55]) + _prng(1023, 4),
    # --- size edges ---------------------------------------------------------
    "empty.txt": b"",
    "empty.bin": b"",
    "single_byte.txt": b"a",
    # --- chunk-boundary probes ---------------------------------------------
    # 512 bytes of text then NULs: inside binaryornot >= 0.5's 512-byte window
    # the file looks like text; 0.4.4's 1024-byte window sees the NULs.
    "boundary_512.txt": b"a" * 512 + b"\x00" * 200,
    "boundary_1024.txt": b"a" * 600 + b"\x00" * 600,
    # --- short-magic false positives ---------------------------------------
    # "BM" (bmp) and "MZ" (dos exe) are two-byte signatures, so ordinary
    # English prose can trip them.
    "bmw_prose.txt": b"BMW cars are nice and this is plain english prose\n",
    "mz_prose.txt": b"MZ is a two letter prefix that also starts DOS binaries\n",
    # --- extension vs content ----------------------------------------------
    "text_content.png": b"this is plain ascii text stored under a png extension\n",
    "text_content.pyc": b"this is plain ascii text stored under a pyc extension\n",
    "text_content.zip": b"this is plain ascii text stored under a zip extension\n",
    "text_content.gz": b"this is plain ascii text stored under a gz extension\n",
    "text_content.bin": b"this is plain ascii text stored under a bin extension\n",
    "crlf_content.tar": b"line1\r\nline2\r\n",
    "png_content.txt": b"\x89PNG\r\n\x1a\n" + _prng(504, 5),
    "PNG_UPPERCASE.PNG": b"plain text under an uppercase png extension\n",
}


# --------------------------------------------------------------------------
def regen_corpus() -> None:
    os.makedirs(FIXTURE_DIR, exist_ok=True)
    for name, data in CORPUS.items():
        with open(os.path.join(FIXTURE_DIR, name), "wb") as fh:
            fh.write(data)
    # Drop files that are no longer in the table so the corpus cannot rot.
    for name in os.listdir(FIXTURE_DIR):
        if name not in CORPUS:
            os.remove(os.path.join(FIXTURE_DIR, name))


def verify_corpus() -> "list[str]":
    problems = []
    for name, data in CORPUS.items():
        path = os.path.join(FIXTURE_DIR, name)
        if not os.path.exists(path):
            problems.append(f"{name}: missing (run --regen-corpus)")
            continue
        with open(path, "rb") as fh:
            on_disk = fh.read()
        if on_disk != data:
            problems.append(f"{name}: on-disk bytes differ from the CORPUS table")
    if os.path.isdir(FIXTURE_DIR):
        for name in sorted(os.listdir(FIXTURE_DIR)):
            if name not in CORPUS:
                problems.append(f"{name}: on disk but not in the CORPUS table")
    return problems


# --------------------------------------------------------------------------
def load_legacy(legacy_dir: str):
    """Import binaryornot 0.4.4 from an unpacked wheel, shadowing the installed
    one for the duration of the call. Returns its is_binary."""
    legacy_dir = os.path.abspath(legacy_dir)
    saved_path = list(sys.path)
    saved_mods = {k: v for k, v in sys.modules.items() if k == "binaryornot" or k.startswith("binaryornot.")}
    for k in saved_mods:
        del sys.modules[k]
    sys.path.insert(0, legacy_dir)
    try:
        mod = importlib.import_module("binaryornot.check")
        if not hasattr(mod, "is_binary"):
            raise RuntimeError("legacy binaryornot has no is_binary")
        fn = mod.is_binary

        def call(path: str) -> bool:
            saved2 = {k: v for k, v in sys.modules.items() if k == "binaryornot" or k.startswith("binaryornot.")}
            sys.modules.update(legacy_mods)
            try:
                return bool(fn(path))
            finally:
                sys.modules.update(saved2)

        legacy_mods = {k: v for k, v in sys.modules.items() if k == "binaryornot" or k.startswith("binaryornot.")}
        return call
    finally:
        sys.path[:] = saved_path
        for k in [k for k in sys.modules if k == "binaryornot" or k.startswith("binaryornot.")]:
            del sys.modules[k]
        sys.modules.update(saved_mods)


# --------------------------------------------------------------------------
def probe_one(name: str, tmpdir: str, legacy) -> dict:
    """Run every observable behaviour against one fixture."""
    src = os.path.join(FIXTURE_DIR, name)
    with open(src, "rb") as fh:
        raw = fh.read()

    rec: dict = {
        "name": name,
        "size": len(raw),
        "sha256": hashlib.sha256(raw).hexdigest(),
        "input_hex": raw.hex() if len(raw) <= 4096 else None,
    }

    # --- binaryornot ------------------------------------------------------
    rec["is_binary"] = bool(is_binary(src))
    try:
        rec["is_binary_content_only"] = bool(is_binary(src, check_extensions=False))
    except TypeError:
        # 0.4.4 has no check_extensions keyword.
        rec["is_binary_content_only"] = rec["is_binary"]
    try:
        from binaryornot.helpers import has_binary_extension

        rec["has_binary_extension"] = bool(has_binary_extension(src))
    except ImportError:
        rec["has_binary_extension"] = None

    # The 24-element feature vector the >=0.5 decision tree consumes. Recorded
    # so implementations can be diffed feature-by-feature when a verdict disagrees,
    # and so float drift in the entropy term is measurable rather than guessed.
    try:
        from binaryornot.helpers import CHUNK_SIZE, _compute_features, get_starting_chunk

        chunk = get_starting_chunk(src, CHUNK_SIZE)
        rec["chunk_size"] = CHUNK_SIZE
        rec["chunk_len"] = len(chunk)
        if chunk:
            # _compute_features can return a plain int 0 for the even/odd null
            # ratios (the `else 0` branch); normalise to float so the vector is
            # uniformly typed on the Go side.
            feats = [float(f) for f in _compute_features(chunk)]
            rec["features"] = feats
            rec["features_hex"] = [float.hex(f) for f in feats]
        else:
            rec["features"] = None
            rec["features_hex"] = None
    except ImportError:
        rec["chunk_size"] = None
        rec["chunk_len"] = None
        rec["features"] = None
        rec["features_hex"] = None

    if legacy is not None:
        rec["is_binary_v044"] = bool(legacy(src))

    # --- convert_win_2_linux, read mode -----------------------------------
    work = os.path.join(tmpdir, name)
    shutil.copyfile(src, work)
    try:
        out = utils.convert_win_2_linux(work)
        # Only small outputs are spelled out in full; the digest keeps the
        # oversized ones checkable without a megabyte of hex per fixture.
        rec["read_hex"] = None if out is None or len(out) > 4096 else out.hex()
        rec["read_sha256"] = None if out is None else hashlib.sha256(out).hexdigest()
        rec["read_size"] = None if out is None else len(out)
        rec["read_is_none"] = out is None
        rec["read_error"] = None
    except Exception as e:  # noqa: BLE001 - pinning the observable failure
        rec["read_hex"] = None
        rec["read_sha256"] = None
        rec["read_size"] = None
        rec["read_is_none"] = None
        rec["read_error"] = f"{type(e).__name__}: {e}"

    # --- convert_win_2_linux, write mode ----------------------------------
    shutil.copyfile(src, work)
    try:
        ret = utils.convert_win_2_linux(work, write=True)
        with open(work, "rb") as fh:
            after = fh.read()
        rec["write_returns_none"] = ret is None
        rec["write_result_hex"] = after.hex() if len(after) <= 4096 else None
        rec["write_result_sha256"] = hashlib.sha256(after).hexdigest()
        rec["write_result_size"] = len(after)
        rec["write_changed"] = after != raw
        rec["write_error"] = None
    except Exception as e:  # noqa: BLE001
        rec["write_returns_none"] = None
        rec["write_result_hex"] = None
        rec["write_result_sha256"] = None
        rec["write_result_size"] = None
        rec["write_changed"] = None
        rec["write_error"] = f"{type(e).__name__}: {e}"

    os.remove(work)
    return rec


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--regen-corpus", action="store_true")
    ap.add_argument("--out", default=DEFAULT_OUT)
    ap.add_argument("--legacy", default=None)
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    if args.regen_corpus:
        regen_corpus()

    problems = verify_corpus()
    if problems:
        for p in problems:
            sys.stderr.write(f"corpus: {p}\n")
        return 1

    legacy = load_legacy(args.legacy) if args.legacy else None

    import binaryornot
    from importlib import metadata

    try:
        bon_version = metadata.version("binaryornot")
    except metadata.PackageNotFoundError:  # pragma: no cover
        bon_version = "unknown"
    try:
        chardet_version = metadata.version("chardet")
    except metadata.PackageNotFoundError:  # pragma: no cover
        chardet_version = "unknown"

    records = []
    with tempfile.TemporaryDirectory(prefix="katharaenc") as tmpdir:
        for name in sorted(CORPUS):
            rec = probe_one(name, tmpdir, legacy)
            records.append(rec)
            if args.verbose:
                sys.stdout.write(
                    "%-26s bin=%-5s ext=%-5s read=%s write_changed=%s\n"
                    % (
                        name,
                        rec["is_binary"],
                        rec["has_binary_extension"],
                        "err" if rec["read_error"] else ("None" if rec["read_is_none"] else "bytes"),
                        rec["write_changed"],
                    )
                )

    doc = {
        "_comment": (
            "Generated by tools/vectorcheck/encoding_probe.py against the oracle venv. "
            "Do not hand-edit."
        ),
        "binaryornot": bon_version,
        "chardet": chardet_version,
        "python": sys.version.split()[0],
        "binaryornot_module": os.path.dirname(binaryornot.__file__),
        "legacy_binaryornot": "0.4.4" if legacy else None,
        "fixtures": records,
    }
    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(doc, fh, indent=2, sort_keys=False)
        fh.write("\n")

    sys.stdout.write(f"wrote {args.out}: {len(records)} fixtures (binaryornot {bon_version})\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
