"""`build_wheels.py` against five dummy binaries.

The flag contract (`--dist`, `--out`, exactly five wheels) is
`.github/workflows/release.yml`'s only requirement of this script, so it is what
gets asserted, alongside the two things `PORT_SPEC.md` §7.3 pins: the binary
lands in ``.data/scripts/`` and each wheel carries a platform tag.

The tests are skipped when hatchling is unavailable — it is a build-time
dependency, not a runtime one, and the release job pip-installs it.
"""

import os
import shutil
import sys
import tempfile
import unittest
import zipfile

import _support  # noqa: F401  (puts the client package on sys.path)

sys.path.insert(0, _support.PACKAGE_ROOT)

import build_wheels  # noqa: E402

try:
    import hatchling  # noqa: F401
    HAS_HATCHLING = True
except ImportError:  # pragma: no cover
    HAS_HATCHLING = False

#: goreleaser's real directory layout: `<build id>_<goos>_<goarch>[_variant]`.
DUMMY_LAYOUT = {
    "kathara_linux_amd64_v1": ("kathara", b"ELF-linux-amd64"),
    "kathara_linux_arm64_v8.0": ("kathara", b"ELF-linux-arm64"),
    "kathara_darwin_amd64_v1": ("kathara", b"MACHO-darwin-amd64"),
    "kathara_darwin_arm64_v8.0": ("kathara", b"MACHO-darwin-arm64"),
    "kathara_windows_amd64_v1": ("kathara.exe", b"PE-windows-amd64"),
}


def make_dist(root):
    """Materialise a goreleaser-shaped dist tree of five dummy binaries."""
    for directory, (name, payload) in DUMMY_LAYOUT.items():
        target_dir = os.path.join(root, directory)
        os.makedirs(target_dir)
        with open(os.path.join(target_dir, name), "wb") as handle:
            handle.write(payload)
    return root


class DiscoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-wheels-test-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)
        self.dist = make_dist(os.path.join(self.tmpdir, "dist"))

    def test_discovers_five_binaries_from_directory_names(self):
        found = build_wheels.discover_binaries(self.dist)
        self.assertEqual(5, len(found))
        self.assertEqual(sorted(build_wheels.PLATFORMS), sorted(found))

    def test_discovers_from_artifacts_json_when_present(self):
        import json

        artifacts = [
            {"type": "Binary", "goos": goos, "goarch": goarch,
             "path": os.path.join(self.dist, directory, name)}
            for directory, (name, _) in DUMMY_LAYOUT.items()
            for goos in [directory.split("_")[1]]
            for goarch in [directory.split("_")[2]]
        ]
        artifacts.append({"type": "Archive", "path": "ignored.tar.gz"})

        with open(os.path.join(self.dist, "artifacts.json"), "w") as handle:
            json.dump(artifacts, handle)

        found = build_wheels.discover_binaries(self.dist)
        self.assertEqual(sorted(build_wheels.PLATFORMS), sorted(found))

    def test_missing_platform_is_fatal(self):
        shutil.rmtree(os.path.join(self.dist, "kathara_windows_amd64_v1"))
        with self.assertRaises(SystemExit) as caught:
            build_wheels.discover_binaries(self.dist)
        self.assertIn("windows/amd64", str(caught.exception))


class ReleaseVersionTest(unittest.TestCase):
    """`PORT_SPEC.md` §7.3: `pip install kathara` must keep working."""

    def test_a_version_below_the_last_pypi_release_is_refused(self):
        # pip resolves by version order: 1.0.0 would leave every existing
        # install on the Python 3.8.3 line, and say nothing about it.
        for version in ("1.0.0", "3.8.3", "3.8"):
            with self.subTest(version=version):
                with self.assertRaises(SystemExit) as caught:
                    build_wheels.check_release_version(version)
                self.assertIn("3.8.3", str(caught.exception))

    def test_a_release_version_passes(self):
        for version in ("3.8.4", "4.0.0", "4.0.0rc1", "10.0.0"):
            with self.subTest(version=version):
                build_wheels.check_release_version(version)

    def test_a_version_with_no_number_is_refused(self):
        with self.assertRaises(SystemExit):
            build_wheels.check_release_version("nightly")

    def test_the_placeholder_in_version_py_would_not_be_published(self):
        with self.assertRaises(SystemExit):
            build_wheels.check_release_version(build_wheels.read_version(_support.PACKAGE_ROOT))


@unittest.skipUnless(HAS_HATCHLING, "hatchling is a build-time dependency")
class BuildTest(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-wheels-build-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)
        self.dist = make_dist(os.path.join(self.tmpdir, "dist"))
        self.out = os.path.join(self.tmpdir, "wheels")

    def build(self, extra=None):
        argv = ["--dist", self.dist, "--out", self.out] + (extra or [])
        self.assertEqual(0, build_wheels.main(argv))
        return sorted(name for name in os.listdir(self.out) if name.endswith(".whl"))

    def test_builds_exactly_five_wheels(self):
        # release.yml asserts this count too.
        wheels = self.build(["--version", "9.9.9"])
        self.assertEqual(5, len(wheels))

    def test_each_wheel_is_platform_tagged(self):
        wheels = self.build(["--version", "9.9.9"])
        tags = {name.rsplit("-", 1)[-1][: -len(".whl")] for name in wheels}
        self.assertEqual(set(build_wheels.PLATFORMS.values()), tags)
        self.assertNotIn("any", tags)

    def test_binary_is_in_data_scripts_and_executable(self):
        import stat

        self.build(["--version", "9.9.9"])

        for name in sorted(os.listdir(self.out)):
            if not name.endswith(".whl"):
                continue
            with self.subTest(wheel=name):
                with zipfile.ZipFile(os.path.join(self.out, name)) as archive:
                    binary_name = "kathara.exe" if "win_amd64" in name else "kathara"
                    entry = "kathara-9.9.9.data/scripts/%s" % binary_name
                    self.assertIn(entry, archive.namelist())

                    info = archive.getinfo(entry)
                    mode = info.external_attr >> 16
                    # pip's `zip_item_is_executable` is `mode and S_ISREG(mode)
                    # and mode & 0o111` — all three, so all three are asserted.
                    # Dropping S_IFREG installs the binary non-executable.
                    self.assertTrue(mode, "no mode recorded for %s" % entry)
                    self.assertTrue(stat.S_ISREG(mode), "the entry must be marked a regular file")
                    self.assertTrue(mode & 0o111, "the binary must stay executable")

                    for other in archive.namelist():
                        if other != entry:
                            other_mode = archive.getinfo(other).external_attr >> 16
                            self.assertTrue(stat.S_ISREG(other_mode), other)

    def test_each_wheel_carries_the_right_binary(self):
        self.build(["--version", "9.9.9"])

        expected = {
            "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64": b"ELF-linux-amd64",
            "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64": b"ELF-linux-arm64",
            "macosx_12_0_x86_64": b"MACHO-darwin-amd64",
            "macosx_12_0_arm64": b"MACHO-darwin-arm64",
            "win_amd64": b"PE-windows-amd64",
        }

        for tag, payload in expected.items():
            wheel = os.path.join(self.out, "kathara-9.9.9-py3-none-%s.whl" % tag)
            with self.subTest(tag=tag):
                self.assertTrue(os.path.exists(wheel))
                with zipfile.ZipFile(wheel) as archive:
                    name = "kathara.exe" if tag == "win_amd64" else "kathara"
                    self.assertEqual(payload, archive.read("kathara-9.9.9.data/scripts/%s" % name))

    def test_wheel_contains_the_package_and_metadata(self):
        self.build(["--version", "9.9.9"])
        wheel = os.path.join(self.out, "kathara-9.9.9-py3-none-win_amd64.whl")

        with zipfile.ZipFile(wheel) as archive:
            names = archive.namelist()
            self.assertIn("Kathara/model/Lab.py", names)
            self.assertIn("Kathara/parser/netkit/LabParser.py", names)
            self.assertIn("Kathara/manager/Kathara.py", names)
            self.assertIn("kathara-9.9.9.dist-info/METADATA", names)
            self.assertIn("kathara-9.9.9.dist-info/RECORD", names)

            metadata = archive.read("kathara-9.9.9.dist-info/METADATA").decode("utf-8")
            self.assertIn("Name: kathara", metadata)
            self.assertIn("Version: 9.9.9", metadata)
            # The audited dependency set (pyproject.toml).
            self.assertIn("Requires-Dist: fs", metadata)
            self.assertNotIn("chardet", metadata)
            self.assertNotIn("Requires-Dist: docker", metadata)

            wheel_metadata = archive.read("kathara-9.9.9.dist-info/WHEEL").decode("utf-8")
            self.assertIn("Root-Is-Purelib: false", wheel_metadata)
            self.assertIn("Tag: py3-none-win_amd64", wheel_metadata)

            # The version constant shipped in the wheel matches its metadata.
            version_py = archive.read("Kathara/version.py").decode("utf-8")
            self.assertIn('CURRENT_VERSION = "9.9.9"', version_py)

    def test_record_covers_every_file_with_a_valid_hash(self):
        import base64
        import csv
        import hashlib
        import io

        self.build(["--version", "9.9.9"])
        wheel = os.path.join(self.out, "kathara-9.9.9-py3-none-macosx_12_0_arm64.whl")

        with zipfile.ZipFile(wheel) as archive:
            record = archive.read("kathara-9.9.9.dist-info/RECORD").decode("utf-8")
            rows = {row[0]: row for row in csv.reader(io.StringIO(record)) if row}

            self.assertEqual(sorted(archive.namelist()), sorted(rows))

            for name in archive.namelist():
                if name.endswith("RECORD"):
                    self.assertEqual(["", ""], rows[name][1:], "RECORD does not hash itself")
                    continue
                payload = archive.read(name)
                digest = base64.urlsafe_b64encode(hashlib.sha256(payload).digest()).rstrip(b"=").decode()
                self.assertEqual("sha256=%s" % digest, rows[name][1], name)
                self.assertEqual(str(len(payload)), rows[name][2], name)

    def test_version_is_restored_after_the_build(self):
        before = build_wheels.read_version(_support.PACKAGE_ROOT)
        self.build(["--version", "9.9.9"])
        self.assertEqual(before, build_wheels.read_version(_support.PACKAGE_ROOT))

    def test_version_comes_from_goreleaser_metadata_when_not_given(self):
        import json

        with open(os.path.join(self.dist, "metadata.json"), "w") as handle:
            json.dump({"version": "v4.2.3"}, handle)

        wheels = self.build()
        for name in wheels:
            self.assertTrue(name.startswith("kathara-4.2.3-"), name)


if __name__ == "__main__":
    unittest.main()
