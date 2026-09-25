
import io
import os
import shutil
import tarfile
import tempfile
import unittest

import _support  # noqa: F401  (puts the client package on sys.path)

from Kathara import _archive
from Kathara.exceptions import InvocationError, NotSupportedError
from Kathara.model.Lab import Lab
from Kathara.model.ExternalLink import ExternalLink
from Kathara.parser.netkit.DepParser import DepParser
from Kathara.parser.netkit.LabParser import LabParser


def model_of(lab):
    """A comparable summary of the parts of a scenario lab.conf can carry."""
    return {
        "name": lab.name,
        "description": lab.description,
        "version": lab.version,
        "author": lab.author,
        "email": lab.email,
        "web": lab.web,
        "machine_order": list(lab.machines.keys()),
        "link_order": list(lab.links.keys()),
        "machines": {
            name: {
                "interfaces": {
                    number: (iface.link.name, iface.mac_address)
                    for number, iface in machine.interfaces.items()
                },
                "meta": machine.meta,
            }
            for name, machine in lab.machines.items()
        },
    }


class ArchiveTestCase(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-archive-test-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)

    def unpack(self, lab):
        """Pack, extract to a directory, and return that directory."""
        payload = _archive.pack_lab(lab)
        destination = tempfile.mkdtemp(dir=self.tmpdir)
        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:*") as tar:
            tar.extractall(destination, filter="data")
        return destination

    def round_trip(self, lab):
        """Pack, extract, re-parse. Returns the re-parsed scenario."""
        return LabParser.parse(self.unpack(lab))


class RoundTripTest(ArchiveTestCase):
    def test_external_links_are_shipped_as_lab_ext(self):
        lab = Lab("external")
        lab.connect_machine_to_link("pc1", "A")
        lab.get_link("A").external.append(ExternalLink("eth0", 20))
        destination = self.unpack(lab)
        with open(os.path.join(destination, "lab.ext"), encoding="utf-8") as source:
            self.assertEqual("A eth0.20\n", source.read())

    def test_interfaces_and_macs(self):
        lab = Lab("rt")
        lab.connect_machine_to_link("pc1", "A")
        lab.connect_machine_to_link("pc1", "B", mac_address="00:11:22:33:44:55")
        lab.connect_machine_to_link("pc2", "A")

        reparsed = self.round_trip(lab)

        self.assertEqual(model_of(lab)["machines"], model_of(reparsed)["machines"])
        self.assertEqual(["pc1", "pc2"], list(reparsed.machines))
        self.assertEqual(["A", "B"], list(reparsed.links))

    def test_every_structured_meta(self):
        lab = Lab("metas")
        machine = lab.new_machine("pc1")
        lab.connect_machine_to_link("pc1", "A")

        machine.add_meta("image", "kathara/frr")
        machine.add_meta("exec", "echo one")
        machine.add_meta("exec", "echo two")
        machine.add_meta("sysctl", "net.ipv4.ip_forward=1")
        machine.add_meta("sysctl", "net.ipv6.conf.all.forwarding=0")
        machine.add_meta("env", "MY_VAR=hello world")
        machine.add_meta("port", "8080:80/tcp")
        machine.add_meta("port", "5000:5000/udp")
        machine.add_meta("ulimit", "nofile=1024:2048")
        machine.add_meta("volume", "/host/data|/guest/data|rw")
        machine.add_meta("privileged", True)
        machine.add_meta("bridged", False)
        machine.add_meta("mem", "512m")
        machine.add_meta("num_terms", "2")
        machine.add_meta("shell", "/bin/sh")

        reparsed = self.round_trip(lab)

        self.assertEqual(machine.meta, reparsed.get_machine("pc1").meta)

    def test_exec_command_order_is_preserved(self):
        lab = Lab("execs")
        machine = lab.new_machine("pc1")
        lab.connect_machine_to_link("pc1", "A")
        for command in ["first", "second", "third"]:
            machine.add_meta("exec", command)

        reparsed = self.round_trip(lab)

        self.assertEqual(["first", "second", "third"], reparsed.get_machine("pc1").meta["exec_commands"])

    def test_lab_metadata(self):
        lab = Lab("meta scenario")
        lab.description = "A scenario"
        lab.version = "1.0"
        lab.author = "Someone"
        lab.email = "someone@example.org"
        lab.web = "https://example.org/kathara"
        lab.connect_machine_to_link("pc1", "A")

        reparsed = self.round_trip(lab)

        self.assertEqual("meta scenario", reparsed.name)
        self.assertEqual("A scenario", reparsed.description)
        self.assertEqual("1.0", reparsed.version)
        self.assertEqual("Someone", reparsed.author)
        self.assertEqual("someone@example.org", reparsed.email)
        self.assertEqual("https://example.org/kathara", reparsed.web)

    def test_device_order_survives_apply_dependencies(self):
        lab = Lab("deps")
        for name in ["pc1", "pc2", "pc3"]:
            lab.connect_machine_to_link(name, "A")

        lab.apply_dependencies(["pc3", "pc2"])
        expected = list(lab.machines.keys())

        reparsed = self.round_trip(lab)

        self.assertEqual(expected, list(reparsed.machines.keys()))

    def test_generated_lab_dep_reorders_nothing(self):
        # The generated lab.dep exists for its *other* effect — v3.8.3 deploys
        # sequentially when a scenario has dependencies (`DockerMachine.py:174`)
        # — so re-applying it on the far side has to be the identity.
        lab = Lab("deps")
        for name in ["pc1", "pc2", "pc3"]:
            lab.connect_machine_to_link(name, "A")

        lab.apply_dependencies(["pc3", "pc2"])
        expected = list(lab.machines.keys())

        destination = self.unpack(lab)
        reparsed = LabParser.parse(destination)
        reparsed.apply_dependencies(DepParser.parse(destination))

        self.assertEqual(expected, list(reparsed.machines.keys()))
        self.assertTrue(reparsed.has_dependencies)

    def test_device_without_interfaces_or_metas_survives(self):
        lab = Lab("lonely")
        lab.new_machine("pc1")
        lab.connect_machine_to_link("pc2", "A")

        reparsed = self.round_trip(lab)

        self.assertIn("pc1", reparsed.machines)
        # The introducer meta is a true no-op: `is_bridged()` is False either way.
        self.assertFalse(reparsed.get_machine("pc1").is_bridged())
        self.assertEqual({}, reparsed.get_machine("pc1").interfaces)


class ArchiveContentTest(ArchiveTestCase):
    def test_filesystem_content_is_shipped(self):
        lab = Lab("files")
        machine = lab.new_machine("pc1")
        lab.connect_machine_to_link("pc1", "A")
        lab.create_startup_file_from_list(machine, ["ip a"])
        lab.create_file_from_string("#!/bin/sh\n", "shared.startup")
        machine.create_file_from_string("nameserver 1.1.1.1\n", "/etc/resolv.conf")

        destination = self.unpack(lab)

        self.assertTrue(os.path.exists(os.path.join(destination, "lab.conf")))
        self.assertTrue(os.path.exists(os.path.join(destination, "pc1.startup")))
        self.assertTrue(os.path.exists(os.path.join(destination, "shared.startup")))
        self.assertTrue(os.path.exists(os.path.join(destination, "pc1", "etc", "resolv.conf")))

        with open(os.path.join(destination, "pc1", "etc", "resolv.conf")) as handle:
            self.assertEqual("nameserver 1.1.1.1\n", handle.read())

    def test_lab_dep_is_shipped_only_for_a_scenario_with_dependencies(self):
        lab = Lab("deps")
        for name in ["pc1", "pc2"]:
            lab.connect_machine_to_link(name, "A")

        self.assertIsNone(_archive.generate_lab_dep(lab))

        lab.apply_dependencies(["pc2"])
        self.assertEqual("pc2: pc1\n", _archive.generate_lab_dep(lab))

        # One device has nothing to sequence.
        single = Lab("one")
        single.connect_machine_to_link("pc1", "A")
        single.apply_dependencies([])
        self.assertIsNone(_archive.generate_lab_dep(single))

    def test_stale_lab_conf_and_lab_dep_are_replaced(self):
        # A scenario parsed from disk carries the original files in its fs; the
        # model is what the caller asked to deploy, so the generated lab.conf
        # wins and lab.dep is dropped (its ordering is already in the model).
        source = os.path.join(self.tmpdir, "scenario")
        os.makedirs(source)
        with open(os.path.join(source, "lab.conf"), "w") as handle:
            handle.write('pc1[0]="A"\n')
        with open(os.path.join(source, "lab.dep"), "w") as handle:
            handle.write("pc1: pc2\n")

        lab = LabParser.parse(source)
        lab.connect_machine_to_link("pc9", "Z")

        destination = self.unpack(lab)

        self.assertFalse(os.path.exists(os.path.join(destination, "lab.dep")))
        with open(os.path.join(destination, "lab.conf")) as handle:
            content = handle.read()
        self.assertIn('pc9[0]="Z"', content)

    def test_archive_is_reproducible(self):
        lab = Lab("repro")
        lab.connect_machine_to_link("pc1", "A")
        self.assertEqual(_archive.pack_lab(lab), _archive.pack_lab(lab))

    def test_gzip_header_carries_no_wall_clock(self):
        # `_archive._FIXED_MTIME`'s claim covers the *gzip* header too, and that
        # is the half two same-second packs cannot expose: bytes 4-8 are the
        # gzip MTIME field, and `tarfile.open(mode="w:gz")` fills them with
        # `time.time()`, so two packs of the same scenario differ whenever they
        # straddle a second boundary. Byte-identical, or the claim is false.
        lab = Lab("repro")
        lab.connect_machine_to_link("pc1", "A")

        packed = _archive.pack_lab(lab)

        self.assertEqual(b"\x1f\x8b", packed[:2], "the default encoding is gzip")
        self.assertEqual(0, int.from_bytes(packed[4:8], "little"))

        # Still a tar the far side (and the stdlib) reads back, uncompressed
        # form included.
        with tarfile.open(fileobj=io.BytesIO(packed), mode="r:gz") as tar:
            self.assertIn("lab.conf", tar.getnames())
        with tarfile.open(fileobj=io.BytesIO(_archive.pack_lab(lab, compress=False)), mode="r") as tar:
            self.assertIn("lab.conf", tar.getnames())

    def test_excluded_files_are_dropped(self):
        lab = Lab("excluded")
        machine = lab.new_machine("pc1")
        lab.connect_machine_to_link("pc1", "A")
        machine.create_file_from_string("junk", ".DS_Store")

        destination = self.unpack(lab)

        self.assertFalse(os.path.exists(os.path.join(destination, "pc1", ".DS_Store")))

    def test_archive_name_is_the_hash_seed(self):
        named = Lab("A scenario")
        self.assertEqual("A scenario", _archive.archive_name(named))

        path_lab = Lab(None, path=self.tmpdir)
        self.assertEqual(self.tmpdir, _archive.archive_name(path_lab))

        renamed = Lab(None, path=self.tmpdir)
        renamed.name = "Renamed"
        self.assertEqual("Renamed", _archive.archive_name(renamed))


class ArchiveRefusalTest(ArchiveTestCase):
    def test_tombstoned_interface_is_refused(self):
        # `Machine.remove_interface` nulls the slot but keeps the key, so the
        # model tolerates a hole that lab.conf cannot express.
        lab = Lab("tombstone")
        lab.connect_machine_to_link("pc1", "A")
        lab.connect_machine_to_link("pc1", "B")
        machine = lab.get_machine("pc1")
        machine.remove_interface(lab.get_link("A"))

        with self.assertRaises(NotSupportedError):
            _archive.pack_lab(lab)

    def test_value_with_a_quote_is_refused(self):
        lab = Lab("quotes")
        lab.connect_machine_to_link("pc1", "A")
        lab.get_machine("pc1").add_meta("image", 'kat"hara')

        with self.assertRaises(InvocationError):
            _archive.generate_lab_conf(lab)

    def test_empty_meta_value_is_refused(self):
        lab = Lab("empty")
        lab.connect_machine_to_link("pc1", "A")
        lab.get_machine("pc1").add_meta("image", "")

        with self.assertRaises(InvocationError):
            _archive.generate_lab_conf(lab)

    def test_an_unwritable_lab_name_is_skipped_not_refused(self):
        lab = Lab("a=b")
        lab.connect_machine_to_link("pc1", "A")

        lab_conf = _archive.generate_lab_conf(lab)

        self.assertNotIn("LAB_NAME", lab_conf)
        self.assertIn('pc1[0]="A"', lab_conf)
        self.assertEqual("a=b", _archive.archive_name(lab))

    def test_lab_metadata_with_equals_is_refused(self):

        lab = Lab("meta")
        lab.web = "https://example.org/?a=b"
        lab.connect_machine_to_link("pc1", "A")

        with self.assertRaises(InvocationError):
            _archive.generate_lab_conf(lab)


if __name__ == "__main__":
    unittest.main()
