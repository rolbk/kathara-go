#!/usr/bin/env python3
"""Layer B authority runner for the rest of ``Kathara/utils.py`` + ``version.py``.

Pins the observable behaviour of the identity chain and the small pure helpers
that `internal/util` ports:

  * ``utils.generate_urlsafe_hash``      (container/network/lab naming -- sacred)
  * ``utils.slug``                       (NFKD + ASCII fold + collapse)
  * ``utils.get_current_user_name``      (slug(user + "-" + hash(hostname)))
  * ``utils.human_readable_bytes``       (Python float repr, banker's rounding)
  * ``utils.parse_docker_engine_version``(char loop, break-on-empty-part)
  * ``version.parse`` / ``version.less_than`` (tuple compare, prefix-is-smaller)
  * ``utils.get_architecture``           (kernel arch map)
  * ``utils.parse_cd_mac_address``       (empty-segment filter)
  * ``utils.check_single_not_none_var`` / ``check_required_single_not_none_var``
  * ``utils.get_absolute_path``          (realpath + the islink/readlink tail)
  * ``utils.check_directory_permissions``(r -> w -> x probe order)
  * ``utils.get_executable_path``        (embedded double quotes)
  * ``shutil.which``                     (the PATH search it delegates to)
  * ``ntpath.splitroot/split/join/normcase`` (the Windows build's path rules)

and dumps the result to ``internal/util/testdata/utils/expected.json`` for the
Go table tests in ``internal/util/utils_test.go``.

The Python behaviour is the truth. When Go disagrees with ``expected.json``,
Go is wrong.

Usage:
    /root/kathara/pyvenv/bin/python tools/vectorcheck/utils_probe.py
    /root/kathara/pyvenv/bin/python tools/vectorcheck/utils_probe.py --out PATH
"""

import argparse
import json
import os
import random
import platform
import shutil
import stat
import sys
import tempfile
import unicodedata

# The Kathara source tree is the oracle; import it directly.
KATHARA_SRC = "/root/kathara/kathara-python/src"
if KATHARA_SRC not in sys.path:
    sys.path.insert(0, KATHARA_SRC)

from Kathara import utils, version  # noqa: E402
from Kathara.exceptions import HostArchitectureError, InvocationError  # noqa: E402

DEFAULT_OUT = os.path.join(
    os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))),
    "internal", "util", "testdata", "utils", "expected.json",
)

# The placeholder the Go test substitutes with its own t.TempDir().
ROOT = "{ROOT}"


def hexs(s):
    return s.encode("utf-8").hex()


# ---------------------------------------------------------------------------
# generate_urlsafe_hash / slug / get_current_user_name
# ---------------------------------------------------------------------------

HASH_INPUTS = [
    "",
    "a",
    "lab",
    "Lab",
    "LAB",
    "kathara",
    "/home/user/labs/bgp",
    "/home/user/labs/bgp/",
    "  spaced  ",
    "with-dash_and_underscore",
    "0123456789",
    "!@#$%^&*()",
    "\x00\x01\x7f",              # ASCII control chars survive the [^\x00-\x7F] strip
    "caffè",                      # non-ASCII run removed entirely
    "CAFFÈ",
    "über-lab",
    "日本語",                      # every char stripped -> hashes the empty string
    "a日本語b",                    # run removed, "ab" hashed
    "emoji-🐳-lab",
    "naïve café résumé",
    "ÅÄÖ",
    "Ω",
    " ",                     # NBSP: non-ASCII, stripped
    "mixed CASE Hostname",
    "my.host.example.com",
    "MY.HOST.EXAMPLE.COM",
    "host-01",
    "DESKTOP-4F2K9QA",
    "a" * 200,
    "\U0001f600" * 10,
]

SLUG_INPUTS = [
    "",
    "a",
    "Hello World",
    "  Hello   World  ",
    "HELLO",
    "hello_world",
    "hello-world",
    "hello--world",
    "hello - world",
    "hello\tworld",
    "hello\nworld",
    "hello\x0bworld",
    "hello\x0cworld",
    "hello\rworld",
    "hello\x1cworld",             # \x1c-\x1f are \s and strip()-able in Python
    "hello\x1dworld",
    "hello\x1eworld",
    "hello\x1fworld",
    "\x1chello\x1f",
    "a.b.c",
    "a/b\\c",
    "a:b;c",
    "user@host",
    "café",
    "CAFÉ",
    "naïve",
    "über",
    "Ångström",
    "ﬁ",                          # NFKD compatibility decomposition -> "fi"
    "①",                          # NFKD -> "1"
    "Ⅻ",                          # NFKD -> "XII" -> "xii"
    "㍿",                          # NFKD -> "株式会社" -> dropped by ASCII fold
    "日本語",
    "a日本b",
    "🐳",
    "x🐳y",
    "___",
    "---",
    "-a-",
    " - ",
    "ＡＢＣ",                      # fullwidth -> NFKD -> "ABC" -> "abc"
    "user-YWJjZGVmZ2hpams",
    "root-DTRVYAyx4W7wnzUsvbaBOw",
]

# (username, hostname) pairs for the identity chain. platform.node() cannot be
# monkeypatched per-case in-process cheaply, so the chain is reproduced from its
# two documented inputs (utils.py:236 and utils.py:246).
IDENTITY_PAIRS = [
    ("root", "kathara-host"),
    ("root", "KATHARA-HOST"),
    ("user", "localhost"),
    ("User", "LocalHost"),
    ("john.doe", "my.host.example.com"),
    ("john_doe", "MY-PC"),
    ("jose", "café-box"),
    ("josé", "cafe-box"),
    ("josé", "café-box"),
    ("user name", "host name"),
    ("", ""),
    ("ÜSER", "HÖST"),
    ("日本", "日本"),
    ("dev", "DESKTOP-4F2K9QA"),
    ("_svc", "host_01"),
]


def nfkd_sweep():
    """Every code point above ASCII whose NFKD normalisation yields ASCII.

    These are exactly the code points where `slug` can produce output from a
    non-ASCII input, so they are the complete parity surface between CPython's
    `unicodedata.normalize("NFKD", ...)` and Go's `x/text/unicode/norm.NFKD`.
    """
    rows = []
    for cp in range(0x80, 0x110000):
        ch = chr(cp)
        if unicodedata.normalize("NFKD", ch).encode("ascii", "ignore"):
            rows.append([cp, utils.slug("a" + ch + "b")])
    return rows


def probe_identity():
    out = {"urlsafe_hash": [], "slug": [], "user_name_chain": [],
           "slug_nfkd_sweep": nfkd_sweep()}
    for s in HASH_INPUTS:
        out["urlsafe_hash"].append({
            "input": s, "input_hex": hexs(s), "output": utils.generate_urlsafe_hash(s),
        })
    for s in SLUG_INPUTS:
        out["slug"].append({
            "input": s, "input_hex": hexs(s), "output": utils.slug(s),
        })
    for user, host in IDENTITY_PAIRS:
        h = utils.generate_urlsafe_hash(host)
        out["user_name_chain"].append({
            "username": user,
            "username_hex": hexs(user),
            "hostname": host,
            "hostname_hex": hexs(host),
            "hostname_hash": h,
            "output": utils.slug("%s-%s" % (user, h)),
        })
    return out


# ---------------------------------------------------------------------------
# human_readable_bytes
# ---------------------------------------------------------------------------

def hrb_sizes():
    sizes = [0, 1, 2, 512, 1000, 1023, 1024, 1025, 1536, 2048, 10000]
    for k in range(0, 7):                      # 1024**0 .. 1024**6 (int64 range)
        p = 1024 ** k
        for d in (-2, -1, 0, 1, 2, 3, 5, 7, 512, 1000):
            v = p + d
            if v > 0:
                sizes.append(v)
        for mul in (3, 7, 512, 1000, 1023):
            v = p * mul
            if v < 2 ** 63:
                sizes.append(v)
    # Rounding edges: .005 boundaries, and the 1023.995 -> "1024.0 KB" carry.
    sizes += [
        1048571, 1048570, 1048575, 1048576,
        1074, 1075, 1076,                      # 1.049..., 1.0498..., 1.0507...
        1126, 1127,
        123456789, 987654321, 1234567890123,
        2 ** 62, 2 ** 63 - 1,
        # exact .xx5 midpoints in units of 1/1024 -> banker's rounding probes
        1024 + 5, 1024 * 3 + 5, 1049, 1050, 1051,
    ]
    seen, ordered = set(), []
    for s in sizes:
        if s not in seen:
            seen.add(s)
            ordered.append(s)
    return ordered


def probe_hrb():
    rows = []
    for size in hrb_sizes():
        try:
            rows.append({"size": str(size), "output": utils.human_readable_bytes(size)})
        except Exception as e:  # noqa: BLE001 - recording the taxonomy is the point
            rows.append({"size": str(size), "error": type(e).__name__ + ": " + str(e)})
    for size in (-1, -1024):
        try:
            rows.append({"size": str(size), "output": utils.human_readable_bytes(size)})
        except Exception as e:  # noqa: BLE001
            rows.append({"size": str(size), "error": type(e).__name__ + ": " + str(e)})
    return rows


def probe_hrb_bulk():
    """A seeded random sweep, to catch banker's-rounding and repr divergences.

    `int(math.floor(math.log(n, 1024)))` is a float computation and it is wrong
    for n = 2**50-1 and 2**60-1 (float(n) rounds up onto the power), so the
    sweep deliberately includes every 2**k neighbourhood.
    """
    rnd = random.Random(20260805)
    sizes = []
    for b in range(0, 63):
        for d in (-2, -1, 0, 1, 2):
            v = (1 << b) + d
            if 0 < v < 2 ** 63:
                sizes.append(v)
    for _ in range(2000):
        sizes.append(rnd.randrange(1, 2 ** 63))
    for _ in range(600):
        sizes.append(rnd.randrange(1, 1024 ** 3))
    seen, rows = set(), []
    for s in sizes:
        if s in seen:
            continue
        seen.add(s)
        rows.append([str(s), utils.human_readable_bytes(s)])
    return rows


# ---------------------------------------------------------------------------
# version.py + parse_docker_engine_version
# ---------------------------------------------------------------------------

ENGINE_VERSIONS = [
    "20.10.14",
    "20.10.14+azure-1",
    "20.10.14+azure",
    "20.10-beta.3",
    "24.0.7",
    "27.3.1",
    "v20.10.14",
    "",
    ".",
    "..",
    "1.",
    ".1",
    "1..2",
    "1.2.3.4.5",
    "abc",
    "1abc.2def.3ghi",
    "1a2.3",
    "0.0.0",
    "007.008",
    "20.10.14-ce",
    "18.09.7",
    "1.2.3-rc1.4",
    "١٢.٣",                       # Arabic-Indic digits: str.isdigit() -> True, int() ok
    "٣",
    "²",                          # superscript two: isdigit() True, int() raises
    "1².2",
    "1_0.2",                      # PEP 515 underscore is NOT a digit -> truncates
    " 1.2",
    "1.2 ",
]

VERSION_PARSE = [
    "3.8.3",
    "3.8",
    "3",
    "0.0.0",
    "007.008",
    "3.08.3",
    "",
    "3.8.3-beta",
    "v3.8.3",
    "1.2.3.4",
    " 3 . 8 ",
    "+3.8",
    "-3.8",
    "3_0.8",
    "1_0",
    "_1",
    "1_",
    "٣.٤",
    "²",
    "3.8.3\n",
    " 3",
]

VERSION_PAIRS = [
    ("3.8.3", "3.8.3"),
    ("3.8.2", "3.8.3"),
    ("3.8.3", "3.8.2"),
    ("3.8", "3.8.1"),
    ("3.8.1", "3.8"),
    ("3.8", "3.8"),
    ("3.10", "3.9"),
    ("3.9", "3.10"),
    ("1.2.3", "1.10"),
    ("20.10.14", "18.06.0"),
    ("18.06.0", "20.10.14"),
    ("0", "0.0"),
    ("0.0", "0"),
    ("3.8.3", ""),
    ("", "3.8.3"),
    ("3.8.3", "3.8.3-beta"),
]


def probe_version():
    engine = []
    for v in ENGINE_VERSIONS:
        row = {"input": v, "input_hex": hexs(v)}
        try:
            row["output"] = utils.parse_docker_engine_version(v)
        except Exception as e:  # noqa: BLE001
            row["error"] = type(e).__name__ + ": " + str(e)
        engine.append(row)

    parsed = []
    for v in VERSION_PARSE:
        row = {"input": v, "input_hex": hexs(v)}
        try:
            row["parts"] = list(version.parse(v))
        except Exception as e:  # noqa: BLE001
            row["error"] = type(e).__name__ + ": " + str(e)
        parsed.append(row)

    pairs = []
    for a, b in VERSION_PAIRS:
        row = {"a": a, "b": b}
        try:
            row["less_than"] = version.less_than(a, b)
        except Exception as e:  # noqa: BLE001
            row["error"] = type(e).__name__ + ": " + str(e)
        pairs.append(row)

    return {
        "current_version": version.CURRENT_VERSION,
        "docker_engine": engine,
        "parse": parsed,
        "less_than": pairs,
    }


# ---------------------------------------------------------------------------
# get_architecture
# ---------------------------------------------------------------------------

ARCH_INPUTS = [
    "x86_64", "X86_64", "amd64", "AMD64", "i686", "I686", "arm64", "ARM64",
    "aarch64", "AArch64", "armv7l", "ARMv7l", "armv6l", "riscv64", "ppc64le",
    "s390x", "i386", "armv8l", "", "x86_64 ",
]


def probe_arch():
    rows = []
    real = utils.machine
    try:
        for m in ARCH_INPUTS:
            utils.machine = lambda m=m: m
            row = {"machine": m}
            try:
                row["output"] = utils.get_architecture()
            except HostArchitectureError as e:
                row["error"] = type(e).__name__ + ": " + str(e)
            rows.append(row)
    finally:
        utils.machine = real
    return {"host_machine": platform.machine(), "cases": rows}


# ---------------------------------------------------------------------------
# parse_cd_mac_address
# ---------------------------------------------------------------------------

CD_MAC_INPUTS = [
    "A", "A/00:11:22:33:44:55", "A//B", "A/", "/A", "A/B/C", "/", "//", "",
    "A//", "//A", "a/b", "A/B/", "/A/B", "  A  /  B  ",
]


def probe_cd_mac():
    rows = []
    for v in CD_MAC_INPUTS:
        row = {"input": v}
        try:
            cd, mac = utils.parse_cd_mac_address(v)
            row["cd"] = cd
            row["mac"] = mac
        except Exception as e:  # noqa: BLE001
            row["error"] = type(e).__name__ + ": " + str(e)
        rows.append(row)
    return rows


# ---------------------------------------------------------------------------
# check_single_not_none_var / check_required_single_not_none_var
# ---------------------------------------------------------------------------

CHECK_CASES = [
    # (required?, ordered (name, is_not_none) pairs)
    (False, [("lab_hash", False), ("lab_name", False), ("lab", False)]),
    (False, [("lab_hash", True), ("lab_name", False), ("lab", False)]),
    (False, [("lab_hash", True), ("lab_name", True), ("lab", False)]),
    (False, [("lab_hash", False), ("lab_name", True), ("lab", True)]),
    (False, [("lab_hash", True), ("lab_name", True), ("lab", True)]),
    (True, [("lab_hash", False), ("lab_name", False), ("lab", False)]),
    (True, [("lab_hash", True), ("lab_name", False), ("lab", False)]),
    (True, [("lab_hash", False), ("lab_name", False), ("lab", True)]),
    (True, [("lab_hash", True), ("lab_name", True), ("lab", False)]),
    (True, [("lab_hash", True), ("lab_name", True), ("lab", True)]),
    (False, [("machine_name", False), ("machine", False)]),
    (True, [("machine_name", False), ("machine", False)]),
    (True, [("machine_name", True), ("machine", True)]),
    # falsy-but-set values still count as provided (NILABILITY.tsv)
    (True, [("lab_hash", "empty-string"), ("lab_name", False), ("lab", False)]),
    (False, [("lab_hash", "empty-string"), ("lab_name", "zero"), ("lab", False)]),
]


def probe_checks():
    rows = []
    for required, pairs in CHECK_CASES:
        kwargs = {}
        flags = []
        for name, present in pairs:
            if present == "empty-string":
                kwargs[name] = ""
                flags.append(True)
            elif present == "zero":
                kwargs[name] = 0
                flags.append(True)
            elif present:
                kwargs[name] = object()
                flags.append(True)
            else:
                kwargs[name] = None
                flags.append(False)
        row = {
            "required": required,
            "names": [name for name, _ in pairs],
            "present": flags,
        }
        try:
            if required:
                utils.check_required_single_not_none_var(**kwargs)
            else:
                utils.check_single_not_none_var(**kwargs)
        except InvocationError as e:
            row["error"] = str(e)
        rows.append(row)
    return rows


# ---------------------------------------------------------------------------
# get_absolute_path (realpath + islink/readlink tail)
# ---------------------------------------------------------------------------

PARENT = "{ROOTPARENT}"


def placehold(p, root):
    """Rewrite the machine-specific temp root out of a path."""
    if p == root:
        return ROOT
    if p.startswith(root + "/"):
        return ROOT + p[len(root):]
    parent = os.path.dirname(root)
    if p == parent:
        return PARENT
    if p.startswith(parent + "/"):
        return PARENT + p[len(parent):]
    return p


def build_tree(root):
    """Create the symlink corpus. Mirrored byte for byte by the Go test."""
    os.makedirs(os.path.join(root, "real", "sub"))
    with open(os.path.join(root, "real", "file.txt"), "w") as f:
        f.write("x")
    with open(os.path.join(root, "real", "sub", "deep.txt"), "w") as f:
        f.write("y")
    os.symlink("real", os.path.join(root, "link_dir"))
    os.symlink("real/file.txt", os.path.join(root, "link_file"))
    os.symlink(os.path.join(root, "real"), os.path.join(root, "abs_link"))
    os.symlink("nowhere", os.path.join(root, "dangling"))
    os.symlink("loop2", os.path.join(root, "loop1"))
    os.symlink("loop1", os.path.join(root, "loop2"))
    os.symlink("self", os.path.join(root, "self"))
    os.symlink("../real/sub", os.path.join(root, "real", "up_link"))
    os.symlink("link_dir", os.path.join(root, "link_to_link"))
    os.makedirs(os.path.join(root, "nested", "a", "b"))
    os.symlink("../../real", os.path.join(root, "nested", "a", "to_real"))


REALPATH_CASES = [
    "{ROOT}",
    "{ROOT}/",
    "{ROOT}/real",
    "{ROOT}/real/",
    "{ROOT}/real/file.txt",
    "{ROOT}/real/./file.txt",
    "{ROOT}/real/../real/file.txt",
    "{ROOT}/link_dir",
    "{ROOT}/link_dir/",
    "{ROOT}/link_dir/file.txt",
    "{ROOT}/link_dir/../real/file.txt",
    "{ROOT}/link_file",
    "{ROOT}/abs_link",
    "{ROOT}/abs_link/file.txt",
    "{ROOT}/link_to_link",
    "{ROOT}/link_to_link/sub",
    "{ROOT}/dangling",
    "{ROOT}/dangling/deeper",
    "{ROOT}/loop1",
    "{ROOT}/loop2",
    "{ROOT}/loop1/x",
    "{ROOT}/self",
    "{ROOT}/real/up_link",
    "{ROOT}/real/up_link/deep.txt",
    "{ROOT}/nested/a/to_real",
    "{ROOT}/nested/a/to_real/file.txt",
    "{ROOT}/missing",
    "{ROOT}/missing/deeper",
    "{ROOT}/real/../../etc",
    "{ROOT}/../..",
    "/",
    "/..",
    "/../..",
    "//",
    "///real",
    "/etc/../etc",
    ".",
    "..",
    "",
    "real",
    "real/file.txt",
    "./real/../link_dir/file.txt",
    "link_dir",
    "loop1",
    "missing",
]


def probe_realpath(root):
    rows = []
    old_cwd = os.getcwd()
    os.chdir(root)
    try:
        for case in REALPATH_CASES:
            path = case.replace(ROOT, root)
            row = {"input": case}
            try:
                row["realpath"] = placehold(os.path.realpath(path), root)
                row["output"] = placehold(utils.get_absolute_path(path), root)
            except Exception as e:  # noqa: BLE001
                row["error"] = type(e).__name__ + ": " + str(e)
            rows.append(row)
    finally:
        os.chdir(old_cwd)
    return rows


# ---------------------------------------------------------------------------
# check_directory_permissions
# ---------------------------------------------------------------------------

PERM_MODES = ["ro", "r", "w", "x", "rw", "rwx", "", "wx", "rx"]


def probe_perms(root):
    base = os.path.join(root, "perms")
    os.makedirs(base)
    dirs = {
        "d_000": 0o000,
        "d_400": 0o400,
        "d_200": 0o200,
        "d_100": 0o100,
        "d_500": 0o500,
        "d_700": 0o700,
        "d_755": 0o755,
    }
    for name, mode in dirs.items():
        p = os.path.join(base, name)
        os.makedirs(p)
        os.chmod(p, mode)

    with open(os.path.join(base, "afile"), "w") as f:
        f.write("x")

    rows = []
    for name in sorted(dirs):
        for mode in PERM_MODES:
            p = os.path.join(base, name)
            row = {"dir": name, "dir_mode": dirs[name], "mode": mode}
            try:
                row["missing"] = utils.check_directory_permissions(p, mode)
            except Exception as e:  # noqa: BLE001
                row["error"] = type(e).__name__ + ": " + str(e)
            rows.append(row)

    for target, kind in ((os.path.join(base, "afile"), "file"),
                         (os.path.join(base, "nope"), "missing")):
        row = {"dir": kind, "mode": "ro"}
        try:
            row["missing"] = utils.check_directory_permissions(target, "ro")
        except Exception as e:  # noqa: BLE001
            row["error"] = type(e).__name__ + ": " + str(e).replace(root, ROOT)
        rows.append(row)

    for name, mode in dirs.items():
        os.chmod(os.path.join(base, name), 0o755)

    return {"euid": os.geteuid(), "cases": rows}


# The uid/gid the unprivileged re-run drops to. `nobody` owns nothing in the
# tree, so every directory answers out of its "other" bits.
UNPRIVILEGED_UID = 65534
UNPRIVILEGED_GID = 65534


def probe_perms_unprivileged(root):
    """Re-run the permission matrix as a user who is actually missing things.

    Recorded as root, every row above has an empty ``missing``: root passes
    access(2) on a directory whatever its mode. The half of the function that
    matters -- the read -> write -> execute report order and the exact label
    strings -- therefore has no oracle coverage at all unless the probe drops
    privileges, which it can only do in a child process.
    """
    if os.geteuid() != 0:
        return None

    base = os.path.join(root, "perms_unprivileged")
    os.makedirs(base)
    dirs = {
        "d_000": 0o000,
        "d_400": 0o400,
        "d_004": 0o004,
        "d_002": 0o002,
        "d_001": 0o001,
        "d_005": 0o005,
        "d_007": 0o007,
        "d_755": 0o755,
    }
    for name, mode in dirs.items():
        p = os.path.join(base, name)
        os.makedirs(p)
        os.chmod(p, mode)

    # The child has to be able to walk down to the cases.
    os.chmod(root, 0o755)
    os.chmod(base, 0o755)

    read_fd, write_fd = os.pipe()
    pid = os.fork()
    if pid == 0:
        try:
            os.close(read_fd)
            os.setgroups([])
            os.setgid(UNPRIVILEGED_GID)
            os.setuid(UNPRIVILEGED_UID)

            rows = []
            for name in sorted(dirs):
                for mode in PERM_MODES:
                    p = os.path.join(base, name)
                    row = {"dir": name, "dir_mode": dirs[name], "mode": mode}
                    try:
                        row["missing"] = utils.check_directory_permissions(p, mode)
                    except Exception as e:  # noqa: BLE001
                        row["error"] = type(e).__name__ + ": " + str(e).replace(root, ROOT)
                    rows.append(row)
            payload = json.dumps({"euid": os.geteuid(), "cases": rows}).encode()
        except BaseException as e:  # noqa: BLE001
            payload = json.dumps({"fatal": repr(e)}).encode()
        finally:
            with os.fdopen(write_fd, "wb") as w:
                w.write(payload)
            os._exit(0)

    os.close(write_fd)
    with os.fdopen(read_fd, "rb") as r:
        payload = r.read()
    _, status = os.waitpid(pid, 0)

    for name in dirs:
        os.chmod(os.path.join(base, name), 0o755)

    assert status == 0, "unprivileged perms child exited with status %d" % status
    data = json.loads(payload)
    assert "fatal" not in data, data["fatal"]
    assert data["euid"] == UNPRIVILEGED_UID, data["euid"]
    return data


# ---------------------------------------------------------------------------
# get_executable_path
# ---------------------------------------------------------------------------

def probe_executable(root):
    rows = []
    exe = os.path.join(root, "kathara-bin")
    with open(exe, "w") as f:
        f.write("#!/bin/sh\n")
    os.chmod(exe, 0o755)
    notexec = os.path.join(root, "plain.txt")
    with open(notexec, "w") as f:
        f.write("x")
    adir = os.path.join(root, "real")

    old_cwd = os.getcwd()
    os.chdir(root)
    try:
        for case in ["{ROOT}/kathara-bin", "kathara-bin", "{ROOT}/plain.txt",
                     "{ROOT}/real", "{ROOT}/missing", "sh", "definitely-not-a-binary-xyz"]:
            path = case.replace(ROOT, root)
            got = utils.get_executable_path(path)
            rows.append({
                "input": case,
                "output": None if got is None else got.replace(root, ROOT),
            })
    finally:
        os.chdir(old_cwd)
    return rows


# ---------------------------------------------------------------------------
# shutil.which (the PATH search inside get_executable_path)
# ---------------------------------------------------------------------------

# (cmd, PATH). A PATH of None means "leave PATH unset"; every entry and every
# command is written with the {ROOT} placeholder the Go test substitutes.
# `cwd` is {ROOT}/which/bin1 throughout, so the relative cases have something
# to find.
WHICH_CASES = [
    ("tool", "{ROOT}/which/bin1:{ROOT}/which/bin2"),
    ("tool", "{ROOT}/which/bin2:{ROOT}/which/bin1"),
    ("tool", "{ROOT}/which/bin2"),
    ("tool", "{ROOT}/which/nope"),
    ("tool", "{ROOT}/which/bin1:{ROOT}/which/bin1"),
    ("tool", ""),
    ("tool", None),
    # A relative PATH entry: os.path.join does not normalise, so the answer
    # keeps its "./" -- which filepath.Join would have Cleaned away.
    ("tool", "."),
    ("tool", ":"),
    ("tool", "::{ROOT}/which/bin2"),
    # Neither a trailing separator nor a doubled one is collapsed.
    ("tool", "{ROOT}/which/bin1/"),
    ("tool", "{ROOT}/which/bin1//"),
    # A `..` through a directory that does not exist fails the access check
    # instead of being folded away.
    ("tool", "{ROOT}/which/nope/../bin1"),
    ("tool", "{ROOT}/which/sub/../bin1"),
    # A command with a directory part never looks at PATH.
    ("./tool", "{ROOT}/which/bin2"),
    ("{ROOT}/which/bin1/tool", ""),
    ("{ROOT}/which/bin1//tool", ""),
    ("{ROOT}/which/bin1/./tool", ""),
    ("sub/tool", "{ROOT}/which/bin2"),
    ("tool/", "{ROOT}/which/bin1"),
    # Not executable, a directory, and a symlink to an executable.
    ("noexec", "{ROOT}/which/bin3"),
    ("adir", "{ROOT}/which/bin3"),
    ("linked", "{ROOT}/which/bin3"),
    ("", "{ROOT}/which/bin1"),
    ("missing-entirely", "{ROOT}/which/bin1"),
]


def probe_which(root):
    base = os.path.join(root, "which")
    for name in ("bin1", "bin2", "bin3", "bin1/sub"):
        os.makedirs(os.path.join(base, name))

    for name in ("bin1/tool", "bin2/tool", "bin1/sub/tool"):
        p = os.path.join(base, name)
        with open(p, "w") as f:
            f.write("#!/bin/sh\n")
        os.chmod(p, 0o755)

    noexec = os.path.join(base, "bin3", "noexec")
    with open(noexec, "w") as f:
        f.write("x")
    os.chmod(noexec, 0o644)
    os.makedirs(os.path.join(base, "bin3", "adir"))
    os.symlink(os.path.join(base, "bin1", "tool"), os.path.join(base, "bin3", "linked"))

    rows = []
    old_cwd = os.getcwd()
    old_path = os.environ.get("PATH")
    os.chdir(os.path.join(base, "bin1"))
    try:
        for cmd, path_env in WHICH_CASES:
            if path_env is None:
                os.environ.pop("PATH", None)
            else:
                os.environ["PATH"] = path_env.replace(ROOT, root)

            got = shutil.which(cmd.replace(ROOT, root))
            rows.append({
                "cmd": cmd,
                "path": path_env,
                "output": None if got is None else got.replace(root, ROOT),
            })
    finally:
        if old_path is None:
            os.environ.pop("PATH", None)
        else:
            os.environ["PATH"] = old_path
        os.chdir(old_cwd)

    return rows


# ---------------------------------------------------------------------------
# ntpath primitives (importable on any platform, so the Windows build's path
# helpers can be checked from Linux)
# ---------------------------------------------------------------------------

NTPATH_INPUTS = [
    "", "C:", "C:.", "C:\\", "C:/", "C:foo", "C:\\foo", "C:foo\\bar",
    "\\foo", "/foo", "foo", "foo\\bar", "foo/bar", "foo\\\\bar", "foo\\",
    "foo\\\\", "\\", "\\\\", "\\\\host", "\\\\host\\share",
    "\\\\host\\share\\", "\\\\host\\share\\dir\\file", "\\\\.\\device",
    "\\\\?\\UNC\\host\\share\\dir", "\\\\?\\C:\\foo", "a\\..\\b", "..",
    ".", "..\\..", "C:\\Users\\me\\", "d:\\x", "1:foo",
]

# join(a, b) pairs: the HOMEDRIVE/HOMEPATH shapes first, then the drive rules.
NTPATH_JOINS = [
    ("C:", ""), ("C:", "\\Users\\me"), ("C:", "Users\\me"),
    ("C:", "Users\\me\\"), ("C:", "a\\..\\b"), ("", "\\Users\\me"),
    ("", ""), ("C:\\", "foo"), ("C:\\a", "b"), ("C:\\a\\", "b"),
    ("C:\\a", "\\b"), ("C:\\a", "D:\\b"), ("C:\\a", "c:\\b"),
    ("C:\\a", "D:b"), ("C:\\a", "C:b"), ("\\\\host\\share", "dir"),
    ("\\\\host\\share\\", "dir"), ("\\\\host", "dir"), ("foo", "bar"),
    ("foo/", "bar"), ("foo", "/bar"), ("/", "foo"),
]


def probe_ntpath():
    import ntpath

    return {
        "splitroot": [
            {"input": p, "output": list(ntpath.splitroot(p))} for p in NTPATH_INPUTS
        ],
        "split": [
            {"input": p, "output": list(ntpath.split(p))} for p in NTPATH_INPUTS
        ],
        "join": [
            {"a": a, "b": b, "output": ntpath.join(a, b)} for a, b in NTPATH_JOINS
        ],
        # normcase's Windows implementation lower-cases through LCMapStringEx,
        # which this platform does not have; only ASCII vectors are recorded,
        # where every lower-casing agrees.
        "normcase": [
            {"input": p, "output": ntpath.normcase(p)}
            for p in NTPATH_INPUTS + ["C:\\Users\\ME", "MiXeD/Case"]
        ],
    }


# ---------------------------------------------------------------------------
# live identity / constants
# ---------------------------------------------------------------------------

def probe_live():
    info = utils.get_current_user_info()
    uid, gid = utils.get_current_user_uid_gid()
    return {
        "platform": sys.platform,
        "node": platform.node(),
        "pw_name": info.pw_name,
        "pw_uid": info.pw_uid,
        "pw_gid": info.pw_gid,
        "pw_dir": info.pw_dir,
        "sudo_uid": os.environ.get("SUDO_UID"),
        "home_env": os.environ.get("HOME"),
        "uid": uid,
        "gid": gid,
        "os_getuid": os.getuid(),
        "current_user_name": utils.get_current_user_name(),
        "current_user_home": utils.get_current_user_home(),
        "is_admin": utils.is_admin(),
        "is_wsl": utils.is_wsl_platform(),
        "pool_size": utils.get_pool_size(),
        "uname_release": os.uname().release,
        "reserved_machine_names": list(utils.RESERVED_MACHINE_NAMES),
        "excluded_files": list(utils.EXCLUDED_FILES),
        "architecture": utils.get_architecture(),
    }


# ---------------------------------------------------------------------------
# re_search_fail
# ---------------------------------------------------------------------------

RE_CASES = [
    (r"^[a-z]+_?[a-z_]+$", "kathara"),
    (r"^[a-z]+_?[a-z_]+$", "kathara_net"),
    (r"^[a-z]+_?[a-z_]+$", "Kathara"),
    (r"^[a-z]+_?[a-z_]+$", "kathara1"),
    (r"^[a-z]+_?[a-z_]+$", ""),
    (r"^[a-z]+_?[a-z_]+$", "kathara\n"),
    (r"^[a-z]+_?[a-z_]+$", "a"),
    (r"^[a-z]+_?[a-z_]+$", "ab"),
]


def probe_re():
    rows = []
    for expr, line in RE_CASES:
        row = {"expr": expr, "line": line}
        try:
            m = utils.re_search_fail(expr, line)
            row["match"] = m.group(0)
        except ValueError as e:
            row["error"] = "ValueError: " + str(e)
        rows.append(row)
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", default=DEFAULT_OUT)
    args = ap.parse_args()

    root = tempfile.mkdtemp(prefix="kathara-utils-probe-")
    try:
        build_tree(root)
        data = {
            "_comment": (
                "Generated by tools/vectorcheck/utils_probe.py against the oracle "
                "venv. Do not hand-edit."
            ),
            "python": platform.python_version(),
            "kathara_version": version.CURRENT_VERSION,
            "live": probe_live(),
            "human_readable_bytes": probe_hrb(),
            "human_readable_bytes_bulk": probe_hrb_bulk(),
            "version": probe_version(),
            "architecture": probe_arch(),
            "cd_mac": probe_cd_mac(),
            "check_vars": probe_checks(),
            "re_search_fail": probe_re(),
            "realpath": probe_realpath(root),
            "permissions": probe_perms(root),
            "permissions_unprivileged": probe_perms_unprivileged(root),
            "executable_path": probe_executable(root),
            "which": probe_which(root),
            "ntpath": probe_ntpath(),
        }
        data.update(probe_identity())
    finally:
        for dirpath, dirnames, _ in os.walk(root):
            for d in dirnames:
                p = os.path.join(dirpath, d)
                if not os.path.islink(p):
                    os.chmod(p, 0o755)
        shutil.rmtree(root, ignore_errors=True)

    os.makedirs(os.path.dirname(args.out), exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2, ensure_ascii=False, sort_keys=False)
        f.write("\n")
    print("wrote %s" % args.out)

    # A couple of sanity assertions so a silently-empty probe cannot pass.
    assert data["urlsafe_hash"], "no hash vectors"
    assert data["slug"], "no slug vectors"
    assert any("error" in r for r in data["version"]["parse"]), "no parse errors captured"
    assert stat and unicodedata  # keep the imports honest


if __name__ == "__main__":
    main()
