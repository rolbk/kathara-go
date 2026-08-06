"""Model parity: the v3.8.3 behaviours consumers and the archive layer rely on.

`PORT_SPEC.md` §0.1 — bugs are ported as-is. Several of the assertions below
pin behaviour that looks wrong on purpose; each names the `DIVERGENCES.md` or
vectors-README entry it belongs to.
"""

import unittest

import _support  # noqa: F401  (puts the client package on sys.path)

from Kathara.exceptions import (
    InvocationError,
    LinkAlreadyExistsError,
    MachineAlreadyExistsError,
    MachineCollisionDomainError,
    MachineNotFoundError,
    MachineOptionError,
    NonSequentialMachineInterfaceError,
)
from Kathara.model.Lab import Lab
from Kathara.model.Machine import Machine
from Kathara.utils import generate_urlsafe_hash


class LabIdentityTest(unittest.TestCase):
    def test_named_lab_hashes_the_name(self):
        # Golden constants from `JSON_CLI_CONTRACT.md` §3.0.1.
        self.assertEqual("9pe3y6IDMwx4PfOPu5mbNg", Lab("Default scenario").hash)
        self.assertEqual("FwFaxbiuhvSWb2KpN5zw", generate_urlsafe_hash("default_scenario"))

    def test_name_setter_recomputes_hash_and_seed(self):
        lab = Lab("first")
        lab.name = "second"
        self.assertEqual(generate_urlsafe_hash("second"), lab.hash)
        self.assertEqual("second", lab.hash_seed)

    def test_named_lab_uses_a_memory_filesystem(self):
        lab = Lab("memory")
        self.assertEqual("memory", lab.fs_type())
        self.assertFalse(lab.has_host_path())


class MachineApiTest(unittest.TestCase):
    def test_new_machine_twice_raises(self):
        lab = Lab("x")
        lab.new_machine("pc1")
        with self.assertRaises(MachineAlreadyExistsError) as caught:
            lab.new_machine("pc1")
        self.assertEqual("Device with name `pc1` already exists.", str(caught.exception))

    def test_get_machine_missing_message_has_no_backticks(self):
        # `ERROR_CODES.md` §2 MachineNotFound, `model/Lab.py:268`.
        lab = Lab("x")
        with self.assertRaises(MachineNotFoundError) as caught:
            lab.get_machine("pc1")
        self.assertEqual("Device pc1 not in the network scenario.", str(caught.exception))

    def test_new_link_twice_keeps_the_typo(self):
        # `ERROR_CODES.md` §0.2: "is already the network scenario." (sic).
        lab = Lab("x")
        lab.new_link("A")
        with self.assertRaises(LinkAlreadyExistsError) as caught:
            lab.new_link("A")
        self.assertEqual("Collision domain A is already the network scenario.", str(caught.exception))

    def test_invalid_device_name_is_a_syntax_error(self):
        lab = Lab("x")
        with self.assertRaises(SyntaxError) as caught:
            lab.new_machine("PC1")
        self.assertEqual("Invalid device name `PC1`.", str(caught.exception))

    def test_interface_numbering_and_conflicts(self):
        lab = Lab("x")
        _, iface0 = lab.connect_machine_to_link("pc1", "A")
        _, iface1 = lab.connect_machine_to_link("pc1", "B")
        self.assertEqual(0, iface0.num)
        self.assertEqual(1, iface1.num)

        with self.assertRaises(MachineCollisionDomainError) as caught:
            lab.connect_machine_to_link("pc1", "C", machine_iface_number=0)
        self.assertEqual("Interface 0 already set on device `pc1`.", str(caught.exception))

        with self.assertRaises(MachineCollisionDomainError) as caught:
            lab.connect_machine_to_link("pc1", "A")
        self.assertEqual("Device `pc1` is already connected to collision domain `A`.", str(caught.exception))

    def test_remove_interface_tombstones_the_slot(self):
        # The slot key survives with a None value, which is why `check()` still
        # passes and why the archive layer has to refuse such a scenario.
        lab = Lab("x")
        lab.connect_machine_to_link("pc1", "A")
        lab.connect_machine_to_link("pc1", "B")
        machine = lab.get_machine("pc1")

        machine.remove_interface(lab.get_link("A"))

        self.assertEqual([0, 1], list(machine.interfaces.keys()))
        self.assertIsNone(machine.interfaces[0])
        self.assertIsNotNone(machine.interfaces[1])
        machine.check()  # a hole in numbering does NOT fail the integrity check

    def test_check_detects_a_missing_interface_number(self):
        lab = Lab("x")
        lab.connect_machine_to_link("pc1", "A", machine_iface_number=1)
        with self.assertRaises(NonSequentialMachineInterfaceError) as caught:
            lab.check_integrity()
        self.assertEqual("Interface `0` missing on device `pc1`.", str(caught.exception))

    def test_remove_machine_requires_a_target(self):
        lab = Lab("x")
        with self.assertRaises(InvocationError) as caught:
            lab.remove_machine()
        self.assertEqual("You must specify a device name or object.", str(caught.exception))


class MetaTest(unittest.TestCase):
    def test_ulimit_errors_name_the_meta_not_the_device(self):
        # DIVERGENCES.md item 2 / vectors SURPRISE 5: `add_meta` interpolates
        # its own `name` parameter, so the message says `ulimit`, not `pc1`.
        machine = Machine(Lab("x"), "pc1")
        with self.assertRaises(MachineOptionError) as caught:
            machine.add_meta("ulimit", "nofile=-2")
        self.assertEqual(
            "Invalid ulimit value (`nofile=-2`) on `ulimit`. Values must be >= -1.", str(caught.exception)
        )

    def test_volume_mode_message_keeps_its_trailing_space(self):
        # `ERROR_CODES.md` §0.2.
        machine = Machine(Lab("x"), "pc1")
        with self.assertRaises(MachineOptionError) as caught:
            machine.add_meta("volume", "/a|/b|zz")
        self.assertEqual(
            "Invalid volume mode `zz` on `/a` mount. Allowed values are ro, rw, rx. ", str(caught.exception)
        )

    def test_api_ipv6_stays_a_real_bool(self):
        # Vectors SURPRISE 7: lab.conf stores the string "false" for the same
        # key. The API path does not, and both must keep behaving that way.
        machine = Machine(Lab("x"), "pc1", **{"ipv6": False})
        self.assertIs(False, machine.meta["ipv6"])

    def test_sysctl_numeric_coercion(self):
        machine = Machine(Lab("x"), "pc1")
        machine.add_meta("sysctl", "net.ipv4.ip_forward=1")
        machine.add_meta("sysctl", "net.ipv4.conf.all.rp_filter=-1")
        machine.add_meta("sysctl", "net.ipv4.tcp_congestion_control=cubic")
        self.assertEqual(
            {"net.ipv4.ip_forward": 1, "net.ipv4.conf.all.rp_filter": -1,
             "net.ipv4.tcp_congestion_control": "cubic"},
            machine.get_sysctls(),
        )

    def test_add_meta_returns_the_previous_value(self):
        machine = Machine(Lab("x"), "pc1")
        self.assertIsNone(machine.add_meta("image", "a"))
        self.assertEqual("a", machine.add_meta("image", "b"))

    def test_port_defaults(self):
        machine = Machine(Lab("x"), "pc1")
        machine.add_meta("port", "8080")
        self.assertEqual({(3000, 'tcp'): 8080}, machine.get_ports())


class FilesystemTest(unittest.TestCase):
    def test_startup_file_from_list(self):
        # The tutorials' entry point.
        lab = Lab("tutorial")
        machine = lab.new_machine("pc1")
        lab.create_startup_file_from_list(machine, ["ip a", "ip r"])

        with lab.fs.open("pc1.startup") as handle:
            self.assertEqual("ip a\nip r\n", handle.read())

    def test_machine_file_creates_the_device_directory(self):
        lab = Lab("x")
        machine = lab.new_machine("pc1")
        self.assertIsNone(machine.fs)

        machine.create_file_from_string("hello\n", "/etc/motd")

        self.assertIsNotNone(machine.fs)
        self.assertTrue(lab.fs.exists("pc1/etc/motd"))

    def test_file_operations_without_a_filesystem_raise_invocation_error(self):
        lab = Lab("x")
        machine = Machine(lab, "pc1")
        machine.fs = None
        machine.lab = None
        with self.assertRaises(AttributeError):
            # No lab to create the directory in: v3.8.3 fails here too.
            machine.create_file_from_string("x", "/f")

    def test_lab_without_filesystem_raises_invocation_error(self):
        lab = Lab("x")
        lab.fs = None
        with self.assertRaises(InvocationError) as caught:
            lab.create_file_from_string("x", "/f")
        self.assertEqual("Cannot create a file if the filesystem is not set.", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
