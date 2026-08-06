#!/usr/bin/env python3
"""Build the five platform-tagged Kathará wheels.

`PORT_SPEC.md` §7.3:

    goreleaser builds five binaries. A build script emits five platform-tagged
    wheels with the binary in `.data/scripts/` so pip puts it on PATH directly.
    Backend is hatchling. No cibuildwheel, no auditwheel, no C toolchain,
    because there is no C extension. This is the `ruff` and `uv` distribution
    pattern.

Flag contract (`.github/workflows/release.yml`)::

    python python/build_wheels.py --dist dist --out python/dist

``--dist`` is the goreleaser output tree; ``--out`` is where the wheels land.
The wheels are built **from the binaries goreleaser just produced**, never from
a second compile, so a wheel and the release tarball can never disagree.

How it works: hatchling builds one pure-Python wheel; then, per platform, that
wheel is unpacked, the matching binary is dropped into
``kathara-<version>.data/scripts/``, the ``WHEEL`` metadata is retagged,
``RECORD`` is regenerated, and the result is repacked under its platform tag.

Extra flags beyond the contract: ``--version`` (override, otherwise read from
``dist/metadata.json`` when goreleaser wrote one, otherwise from
``Kathara/version.py``) and ``--project`` (the directory holding
``pyproject.toml``; defaults to this script's directory).
"""

import argparse
import base64
import csv
import hashlib
import io
import json
import os
import shutil
import sys
import tempfile
import zipfile

HERE = os.path.dirname(os.path.abspath(__file__))

#: goreleaser's five targets (`.goreleaser.yaml`: linux/darwin/windows ×
#: amd64/arm64, minus windows/arm64), mapped to the wheel platform tags.
#:
#: The Linux tags are compound because the binaries are `CGO_ENABLED=0` static
#: builds: they have no libc dependency at all, so one wheel is genuinely valid
#: on manylinux and musllinux alike. A bare `linux_x86_64` tag would also be
#: accurate but PyPI rejects it.
#: The macOS tags state the floor of the **Go toolchain**, not of the wheel
#: format: `go.mod` says `go 1.26`, whose darwin binaries require macOS 12. A
#: lower tag would let pip install, on macOS 11, a wheel whose binary cannot
#: run, with nothing said at install time. Keep in sync with `go.mod`.
PLATFORMS = {
    ("linux", "amd64"): "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64",
    ("linux", "arm64"): "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64",
    ("darwin", "amd64"): "macosx_12_0_x86_64",
    ("darwin", "arm64"): "macosx_12_0_arm64",
    ("windows", "amd64"): "win_amd64",
}

EXPECTED_WHEEL_COUNT = len(PLATFORMS)

#: The last version of the Python Kathará on PyPI. `PORT_SPEC.md` §7.3 requires
#: `pip install kathara` to keep working, and pip resolves by version order: a
#: wheel that does not sort above this one would leave every existing install on
#: the Python line, silently. Release builds must therefore carry a higher
#: version, which goreleaser's tag supplies (`dist/metadata.json`).
LAST_PYTHON_RELEASE = (3, 8, 3)

BINARY_NAMES = ("kathara", "kathara.exe")

WHEEL_GENERATOR = "kathara-build_wheels"


# --------------------------------------------------------------------------
# Discovering the goreleaser binaries
# --------------------------------------------------------------------------

def _from_artifacts_json(dist_dir):
    """Read the binary list out of goreleaser's own manifest, when present."""
    path = os.path.join(dist_dir, "artifacts.json")
    if not os.path.exists(path):
        return {}

    with open(path) as handle:
        artifacts = json.load(handle)

    found = {}
    for artifact in artifacts:
        if artifact.get("type") != "Binary":
            continue
        key = (artifact.get("goos"), artifact.get("goarch"))
        if key in PLATFORMS:
            binary = artifact.get("path")
            if binary and not os.path.isabs(binary):
                binary = os.path.join(dist_dir, binary) if not binary.startswith(dist_dir) else binary
            if binary and os.path.exists(binary):
                found[key] = binary

    return found


def _from_directory_names(dist_dir):
    """Fall back to goreleaser's `<id>_<goos>_<goarch>[_variant]/` layout."""
    found = {}

    for entry in sorted(os.listdir(dist_dir)):
        directory = os.path.join(dist_dir, entry)
        if not os.path.isdir(directory):
            continue

        tokens = entry.split("_")
        for (goos, goarch) in PLATFORMS:
            if goos in tokens and goarch in tokens:
                for name in BINARY_NAMES:
                    candidate = os.path.join(directory, name)
                    if os.path.isfile(candidate):
                        found[(goos, goarch)] = candidate
                        break
                break

    return found


def discover_binaries(dist_dir):
    """Return ``{(goos, goarch): path}`` for the five release binaries.

    Args:
        dist_dir (str): The goreleaser dist directory.

    Returns:
        Dict[Tuple[str, str], str]: The discovered binaries.

    Raises:
        SystemExit: If any of the five platforms is missing.
    """
    found = _from_artifacts_json(dist_dir)
    for key, path in _from_directory_names(dist_dir).items():
        found.setdefault(key, path)

    missing = [key for key in PLATFORMS if key not in found]
    if missing:
        raise SystemExit(
            "missing binaries in %s for: %s" % (dist_dir, ", ".join("%s/%s" % key for key in sorted(missing)))
        )

    return found


# --------------------------------------------------------------------------
# Version handling
# --------------------------------------------------------------------------

def read_version(project_dir):
    path = os.path.join(project_dir, "Kathara", "version.py")
    with open(path) as handle:
        for line in handle:
            if line.startswith("CURRENT_VERSION"):
                return line.split("=", 1)[1].strip().strip('"').strip("'")

    raise SystemExit("no CURRENT_VERSION in %s" % path)


def version_from_dist(dist_dir):
    """goreleaser writes `dist/metadata.json` with the release version."""
    path = os.path.join(dist_dir, "metadata.json")
    if not os.path.exists(path):
        return None

    with open(path) as handle:
        try:
            return json.load(handle).get("version")
        except ValueError:
            return None


def release_ordinal(version):
    """Return the leading numeric components of a version, for comparison."""
    components = []
    for part in version.split("."):
        digits = ""
        for character in part:
            if not character.isdigit():
                break
            digits += character
        if not digits:
            break
        components.append(int(digits))

    return tuple(components)


def check_release_version(version):
    """Refuse to build wheels that `pip install -U kathara` would never pick.

    Args:
        version (str): The resolved wheel version.

    Raises:
        SystemExit: If the version does not sort above the last PyPI release.
    """
    ordinal = release_ordinal(version)
    if not ordinal:
        raise SystemExit("version %r has no numeric component" % version)

    if ordinal <= LAST_PYTHON_RELEASE:
        raise SystemExit(
            "version %s does not sort above the last PyPI release (%s): `pip install -U kathara` would keep the "
            "Python build. Pass --version, or tag the release, with a higher version."
            % (version, ".".join(str(part) for part in LAST_PYTHON_RELEASE))
        )


class PinnedVersion(object):
    """Temporarily rewrite ``Kathara/version.py`` for the duration of a build.

    The runtime constant and the wheel metadata are the same fact; pinning the
    file (rather than patching metadata afterwards) is what keeps them equal,
    and it means ``Kathara.version.CURRENT_VERSION`` in an installed wheel
    matches the ``kathara -v`` of the binary shipped beside it.
    """

    def __init__(self, project_dir, version):
        self.path = os.path.join(project_dir, "Kathara", "version.py")
        self.version = version
        self.original = None

    def __enter__(self):
        with open(self.path) as handle:
            self.original = handle.read()

        if self.version is not None:
            patched = []
            for line in self.original.splitlines(keepends=True):
                if line.startswith("CURRENT_VERSION"):
                    line = 'CURRENT_VERSION = "%s"\n' % self.version
                patched.append(line)

            with open(self.path, "w") as handle:
                handle.write("".join(patched))

        return self

    def __exit__(self, exc_type, exc_value, traceback):
        if self.original is not None:
            with open(self.path, "w") as handle:
                handle.write(self.original)
        return False


# --------------------------------------------------------------------------
# Wheel surgery
# --------------------------------------------------------------------------

def build_base_wheel(project_dir, workdir):
    """Build the pure-Python wheel with hatchling and return its path."""
    try:
        from hatchling.build import build_wheel
    except ImportError:  # pragma: no cover - the workflow pip-installs it
        raise SystemExit("hatchling is required: pip install hatchling")

    previous = os.getcwd()
    os.chdir(project_dir)
    try:
        name = build_wheel(workdir)
    finally:
        os.chdir(previous)

    return os.path.join(workdir, name)


def _record_line(root, path):
    relative = os.path.relpath(path, root).replace(os.sep, "/")
    with open(path, "rb") as handle:
        payload = handle.read()

    digest = base64.urlsafe_b64encode(hashlib.sha256(payload).digest()).rstrip(b"=").decode("ascii")
    return [relative, "sha256=%s" % digest, str(len(payload))]


def _retag_wheel_metadata(wheel_file, platform_tag):
    """Rewrite the ``WHEEL`` metadata for a platform wheel."""
    with open(wheel_file) as handle:
        lines = [line for line in handle.read().splitlines() if not line.startswith(("Tag:", "Root-Is-Purelib:"))]

    lines.append("Root-Is-Purelib: false")
    # The compressed tag set in the filename expands to one Tag line per
    # platform (PEP 427); pip reads either form, wheel-validating tools do not.
    for part in platform_tag.split("."):
        lines.append("Tag: py3-none-%s" % part)

    with open(wheel_file, "w") as handle:
        handle.write("\n".join(lines) + "\n")


def make_platform_wheel(base_wheel, out_dir, version, platform_tag, binary_path, binary_name):
    """Unpack, inject the binary, retag, and repack.

    Returns:
        str: The path of the wheel that was written.
    """
    staging = tempfile.mkdtemp(prefix="kathara-wheel-")
    try:
        with zipfile.ZipFile(base_wheel) as archive:
            archive.extractall(staging)

        dist_info = os.path.join(staging, "kathara-%s.dist-info" % version)
        if not os.path.isdir(dist_info):
            raise SystemExit("unexpected wheel layout: no %s" % os.path.basename(dist_info))

        scripts_dir = os.path.join(staging, "kathara-%s.data" % version, "scripts")
        os.makedirs(scripts_dir, exist_ok=True)
        target = os.path.join(scripts_dir, binary_name)
        shutil.copyfile(binary_path, target)
        os.chmod(target, 0o755)

        _retag_wheel_metadata(os.path.join(dist_info, "WHEEL"), platform_tag)

        # RECORD last: it hashes everything else, itself excepted.
        record_path = os.path.join(dist_info, "RECORD")
        if os.path.exists(record_path):
            os.remove(record_path)

        rows = []
        for root, _, files in os.walk(staging):
            for name in sorted(files):
                rows.append(_record_line(staging, os.path.join(root, name)))
        rows.sort()
        rows.append(["kathara-%s.dist-info/RECORD" % version, "", ""])

        buffer = io.StringIO()
        writer = csv.writer(buffer, lineterminator="\n")
        writer.writerows(rows)
        with open(record_path, "w", newline="") as handle:
            handle.write(buffer.getvalue())

        wheel_name = "kathara-%s-py3-none-%s.whl" % (version, platform_tag)
        wheel_path = os.path.join(out_dir, wheel_name)

        with zipfile.ZipFile(wheel_path, "w", zipfile.ZIP_DEFLATED) as archive:
            for root, _, files in os.walk(staging):
                for name in sorted(files):
                    absolute = os.path.join(root, name)
                    relative = os.path.relpath(absolute, staging).replace(os.sep, "/")
                    info = zipfile.ZipInfo(relative, date_time=(1980, 1, 1, 0, 0, 0))
                    # Preserve the executable bit of the bundled binary: pip
                    # only chmod +x an extracted script when the zip entry says
                    # "regular file, some execute bit set", so S_IFREG has to be
                    # part of the high half of external_attr. Without it the
                    # binary lands in the venv's bin/ unrunnable.
                    mode = 0o755 if relative.endswith("/" + binary_name) else 0o644
                    info.external_attr = (0o100000 | mode) << 16
                    info.compress_type = zipfile.ZIP_DEFLATED
                    with open(absolute, "rb") as handle:
                        archive.writestr(info, handle.read())

        return wheel_path
    finally:
        shutil.rmtree(staging, ignore_errors=True)


# --------------------------------------------------------------------------

def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description="Build the five platform-tagged Kathara wheels.")
    parser.add_argument("--dist", required=True, help="the goreleaser dist directory")
    parser.add_argument("--out", required=True, help="where to write the wheels")
    parser.add_argument("--project", default=HERE, help="directory containing pyproject.toml")
    parser.add_argument("--version", default=None, help="override the wheel version")
    args = parser.parse_args(argv)

    dist_dir = os.path.abspath(args.dist)
    out_dir = os.path.abspath(args.out)
    project_dir = os.path.abspath(args.project)

    if not os.path.isdir(dist_dir):
        raise SystemExit("no such dist directory: %s" % dist_dir)

    binaries = discover_binaries(dist_dir)

    version = args.version or version_from_dist(dist_dir)
    if version:
        version = version.lstrip("v")
    else:
        version = read_version(project_dir)

    check_release_version(version)

    os.makedirs(out_dir, exist_ok=True)

    workdir = tempfile.mkdtemp(prefix="kathara-basewheel-")
    written = []
    try:
        # Pinning to the resolved version is a no-op when it already matches
        # what `Kathara/version.py` says, and the fix when it does not.
        with PinnedVersion(project_dir, version):
            base_wheel = build_base_wheel(project_dir, workdir)

            for (goos, goarch), platform_tag in sorted(PLATFORMS.items()):
                binary_name = "kathara.exe" if goos == "windows" else "kathara"
                wheel_path = make_platform_wheel(
                    base_wheel, out_dir, version, platform_tag, binaries[(goos, goarch)], binary_name
                )
                written.append(wheel_path)
                print("%s/%s -> %s" % (goos, goarch, os.path.basename(wheel_path)))
    finally:
        shutil.rmtree(workdir, ignore_errors=True)

    if len(written) != EXPECTED_WHEEL_COUNT:
        raise SystemExit("expected %d wheels, wrote %d" % (EXPECTED_WHEEL_COUNT, len(written)))

    print("%d wheels in %s" % (len(written), out_dir))
    return 0


if __name__ == "__main__":
    sys.exit(main())
