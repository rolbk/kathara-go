"""The downstream import surface must resolve against this package."""

import unittest

import _support  # noqa: F401  (puts the client package on sys.path)


class LabCheckerImportSurfaceTest(unittest.TestCase):
    def test_every_import_line_resolves(self):
        from Kathara.exceptions import MachineNotFoundError  # noqa: F401
        from Kathara.exceptions import MachineNotRunningError  # noqa: F401
        from Kathara.exceptions import MachineNotRunningError, MachineBinaryError  # noqa: F401,F811
        from Kathara.exceptions import MachineCollisionDomainError  # noqa: F401
        from Kathara.manager.Kathara import Kathara  # noqa: F401
        from Kathara.model.Lab import Lab  # noqa: F401
        from Kathara.model.Machine import Machine  # noqa: F401
        from Kathara.parser.netkit.LabParser import LabParser  # noqa: F401
        from Kathara.setting.Setting import Setting  # noqa: F401

    def test_ioerror_catches_the_parser_file_errors(self):
        # `__main__.py:78` catches IOError around `LabParser.parse`.
        import tempfile

        from Kathara.parser.netkit.LabParser import LabParser

        with tempfile.TemporaryDirectory() as empty:
            with self.assertRaises(IOError) as caught:
                LabParser.parse(empty)
            self.assertEqual("No lab.conf in given directory.", str(caught.exception))

    def test_model_surface_lab_checker_reads(self):
        from Kathara.model.Lab import Lab

        lab = Lab("surface")
        lab.connect_machine_to_link("pc1", "A")
        device = lab.get_machine("pc1")
        device.add_meta("sysctl", "net.ipv4.ip_forward=1")

        self.assertTrue(lab.hash)
        self.assertIsNotNone(lab.fs)
        self.assertEqual(["A"], [i.link.name for i in device.interfaces.values()])
        self.assertEqual({"net.ipv4.ip_forward": 1}, device.get_sysctls())
        self.assertIn("sysctls", device.meta)

    def test_manager_singleton_accessor(self):
        from Kathara.manager.Kathara import Kathara

        self.assertIs(Kathara.get_instance(), Kathara.get_instance())


class TutorialSurfaceTest(unittest.TestCase):
    def test_getting_started_scenario_builds(self):
        from Kathara.model.Lab import Lab

        lab = Lab("Getting Started")

        pc1 = lab.new_machine("pc1", **{"image": "kathara/base"})
        pc2 = lab.new_machine("pc2", **{"image": "kathara/base"})
        lab.connect_machine_to_link(pc1.name, "A")
        lab.connect_machine_to_link(pc2.name, "A")

        lab.create_startup_file_from_list(pc1, ["ip address add 10.0.0.1/24 dev eth0"])
        lab.create_startup_file_from_list(pc2, ["ip address add 10.0.0.2/24 dev eth0"])
        pc1.create_file_from_string("hello\n", "/etc/motd")

        self.assertEqual(["pc1", "pc2"], list(lab.machines))
        self.assertEqual(["A"], list(lab.links))
        self.assertTrue(lab.fs.exists("pc1.startup"))
        self.assertTrue(lab.fs.exists("pc1/etc/motd"))


if __name__ == "__main__":
    unittest.main()
