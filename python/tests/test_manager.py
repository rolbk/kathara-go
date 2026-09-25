"""The manager facade against a fake binary: argv, stdin, stdout, exit codes."""

import io
import json
import os
import tarfile
import unittest

from _support import FakeBinaryTestCase

from Kathara.exceptions import (
    InstantiationError,
    InvocationError,
    KatharaError,
    LabNotFoundError,
    MachineBinaryError,
    MachineCollisionDomainError,
    MachineNotFoundError,
    MachineNotRunningError,
    NotSupportedError,
)
from Kathara.manager import Kathara as manager_module
from Kathara.manager.Kathara import Kathara
from Kathara.model.Lab import Lab
from Kathara.utils import generate_urlsafe_hash

INVENTORY = [
    {"network_scenario_id": "H1", "name": "pc1", "container_name": "kathara_u_pc1_H1",
     "user": "u", "status": "running", "image": "kathara/base"},
    {"network_scenario_id": "H1", "name": "pc2", "container_name": "kathara_u_pc2_H1",
     "user": "u", "status": "running", "image": "kathara/base"},
    {"network_scenario_id": "H2", "name": "r1", "container_name": "kathara_u_r1_H2",
     "user": "u", "status": "running", "image": "kathara/frr"},
]


def build_lab():
    lab = Lab("Test scenario")
    pc1 = lab.new_machine("pc1", **{"image": "kathara/base"})
    lab.connect_machine_to_link("pc1", "A")
    lab.connect_machine_to_link("pc2", "A")
    lab.create_startup_file_from_list(pc1, ["ip addr add 10.0.0.1/24 dev eth0"])
    return lab


class SingletonTest(unittest.TestCase):
    def test_second_instantiation_raises(self):
        # v3.8.3 parity: the facade is a singleton.
        saved = Kathara._Kathara__instance
        try:
            Kathara._Kathara__instance = None
            first = Kathara.get_instance()
            self.assertIs(first, Kathara.get_instance())
            with self.assertRaises(InstantiationError):
                Kathara()
        finally:
            Kathara._Kathara__instance = saved


class DeployTest(FakeBinaryTestCase):
    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    @staticmethod
    def deployed_name(argv):
        """The value of `--name`, which the client always writes as `--name=X`."""
        for token in argv:
            if token.startswith("--name="):
                return token[len("--name="):]
        raise AssertionError("%r carries no --name" % argv)

    def test_deploy_lab_invokes_lstart_from_archive(self):
        lab = build_lab()
        self.plan_result({"lab": {"name": "Test scenario", "hash": lab.hash, "path": None},
                          "dry_run": False, "machines": ["pc1", "pc2"], "links": ["A"]})

        self.manager.deploy_lab(lab)

        argv = self.last_argv()
        self.assertEqual("lstart", argv[0])
        self.assertArgvContains(argv, ["--format", "json"])
        self.assertArgvContains(argv, ["--from-archive", "-"])
        self.assertEqual("Test scenario", self.deployed_name(argv))

    def test_deploy_lab_name_reproduces_the_lab_hash(self):
        lab = Lab(None, path=self.tmpdir)
        lab.new_machine("pc1")
        self.manager.deploy_lab(lab)

        self.assertEqual(lab.hash, generate_urlsafe_hash(self.deployed_name(self.last_argv())))

    def test_deploy_lab_name_starting_with_a_dash_is_not_a_flag(self):
        # `--name -x` would reach the binary's parser as a flag; `--name=-x` is
        # the same value and cannot be mistaken for one.
        lab = Lab("-x")
        lab.new_machine("pc1")
        self.manager.deploy_lab(lab)

        self.assertEqual("-x", self.deployed_name(self.last_argv()))

    def test_deploy_lab_forwards_general_options(self):
        # v3.8.3 honours these at deploy (`DockerMachine.py:161,302-310`); the
        # binary hears about them through the flags `lstart` fills them from.
        lab = build_lab()
        lab.add_option("hosthome_mount", True)
        lab.add_option("shared_mount", False)

        self.manager.deploy_lab(lab)

        argv = self.last_argv()
        self.assertIn("--hosthome", argv)
        self.assertIn("--no-shared", argv)

    def test_deploy_lab_refuses_an_option_lstart_cannot_express(self):
        lab = build_lab()
        lab.add_option("_mount_volumes", False)

        with self.assertRaises(NotSupportedError) as caught:
            self.manager.deploy_lab(lab)
        self.assertIn("_mount_volumes", str(caught.exception))

    def test_deploy_lab_forwards_global_machine_metadata(self):
        # `-o key=value` per pair, which is exactly what `OptionParser` fills
        # `global_machine_metadata` from (`LstartCommand.py:187`).
        lab = build_lab()
        lab.add_global_machine_metadata("image", "kathara/frr")
        lab.add_global_machine_metadata("mem", "512m")
        lab.add_global_machine_metadata("privileged", True)

        self.manager.deploy_lab(lab)

        argv = self.last_argv()
        self.assertArgvContains(argv, ["-o", "image=kathara/frr"])
        self.assertArgvContains(argv, ["-o", "mem=512m"])
        self.assertIn("--privileged", argv)

    def test_deploy_lab_refuses_inexpressible_global_metadata(self):
        lab = build_lab()
        lab.add_global_machine_metadata("ipv6", False)
        with self.assertRaises(NotSupportedError):
            self.manager.deploy_lab(lab)

        other = build_lab()
        other.add_global_machine_metadata("exec", "echo a=b")
        with self.assertRaises(InvocationError):
            self.manager.deploy_lab(other)

    def test_deploy_lab_ships_a_lab_dep_when_the_scenario_has_dependencies(self):
        # v3.8.3 deploys sequentially when the scenario has dependencies
        # (`DockerMachine.py:174-183`); the only way to ask for that is a lab.dep.
        lab = build_lab()
        lab.connect_machine_to_link("pc3", "A")
        lab.apply_dependencies(["pc3", "pc1"])

        self.manager.deploy_lab(lab)

        with tarfile.open(fileobj=io.BytesIO(self.last_stdin()), mode="r:*") as tar:
            self.assertIn("lab.dep", tar.getnames())

    def test_deploy_lab_without_dependencies_ships_no_lab_dep(self):
        self.manager.deploy_lab(build_lab())

        with tarfile.open(fileobj=io.BytesIO(self.last_stdin()), mode="r:*") as tar:
            self.assertNotIn("lab.dep", tar.getnames())

    def test_deploy_lab_of_a_device_less_scenario_is_a_no_op(self):
        # v3.8.3 succeeds and deploys nothing; an empty lab.conf would instead
        # be `lab.conf file is empty.` on the far side.
        self.manager.deploy_lab(Lab("empty"))
        self.assertEqual([], self.calls())

    def test_deploy_lab_of_a_device_less_scenario_still_validates_the_selection(self):
        # `DockerManager.py:149-155`: a selection that is not in the scenario is
        # an error, and no lab.conf ever reaches the binary to say so.
        with self.assertRaises(MachineNotFoundError) as caught:
            self.manager.deploy_lab(Lab("empty"), selected_machines={"pc1"})
        self.assertEqual(
            "The following devices are not in the network scenario: {'pc1'}.", str(caught.exception)
        )
        self.assertEqual([], self.calls())

    def test_deploy_lab_of_a_links_only_scenario_is_refused(self):
        lab = Lab("links only")
        lab.get_or_new_link("A")

        with self.assertRaises(NotSupportedError):
            self.manager.deploy_lab(lab)

    def test_deploy_lab_streams_a_tar_containing_the_generated_lab_conf(self):
        lab = build_lab()
        self.manager.deploy_lab(lab)

        payload = self.last_stdin()
        self.assertTrue(payload, "the archive must be piped to stdin")

        with tarfile.open(fileobj=io.BytesIO(payload), mode="r:*") as tar:
            names = tar.getnames()
            self.assertIn("lab.conf", names)
            self.assertIn("pc1.startup", names)

            lab_conf = tar.extractfile("lab.conf").read().decode("utf-8")

        self.assertIn('pc1[0]="A"', lab_conf)
        self.assertIn('pc2[0]="A"', lab_conf)
        self.assertIn('pc1[image]="kathara/base"', lab_conf)

    def test_deploy_lab_selection_and_exclusion(self):
        lab = build_lab()

        self.manager.deploy_lab(lab, selected_machines={"pc1"})
        self.assertIn("pc1", self.last_argv())

        self.manager.deploy_lab(lab, excluded_machines={"pc2"})
        self.assertArgvContains(self.last_argv(), ["--exclude", "pc2"])

    def test_deploy_lab_rejects_both_selection_and_exclusion(self):
        lab = build_lab()
        with self.assertRaises(InvocationError):
            self.manager.deploy_lab(lab, selected_machines={"pc1"}, excluded_machines={"pc2"})

    def test_deploy_machine_deploys_only_that_device(self):
        lab = build_lab()
        self.manager.deploy_machine(lab.get_machine("pc1"))

        argv = self.last_argv()
        self.assertEqual("lstart", argv[0])
        self.assertIn("pc1", argv)
        self.assertNotIn("pc2", argv)

    def test_deploy_machine_without_lab_raises(self):
        lab = build_lab()
        machine = lab.get_machine("pc1")
        machine.lab = None
        with self.assertRaises(LabNotFoundError):
            self.manager.deploy_machine(machine)

    def test_deploy_link_uses_api_bridge(self):
        lab = build_lab()
        self.manager.deploy_link(lab.get_link("A"))
        self.assertEqual("api", self.last_argv()[0])
        self.assertIn("deploy-link", self.last_argv())
        payload = json.loads(self.last_stdin())
        self.assertEqual(lab.hash, payload["lab_hash"])
        self.assertEqual("A", payload["link_name"])


class UndeployTest(FakeBinaryTestCase):
    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    def test_undeploy_lab_by_hash(self):
        self.manager.undeploy_lab(lab_hash="H1")
        argv = self.last_argv()
        self.assertEqual("lclean", argv[0])
        self.assertArgvContains(argv, ["--lab-hash", "H1"])
        self.assertArgvContains(argv, ["--format", "json"])

    def test_undeploy_lab_by_name_hashes_the_name(self):
        self.manager.undeploy_lab(lab_name="Test scenario")
        argv = self.last_argv()
        self.assertArgvContains(argv, ["--lab-hash", generate_urlsafe_hash("Test scenario")])

    def test_undeploy_lab_by_object(self):
        lab = build_lab()
        self.manager.undeploy_lab(lab=lab)
        self.assertArgvContains(self.last_argv(), ["--lab-hash", lab.hash])

    def test_undeploy_lab_requires_a_target(self):
        with self.assertRaises(InvocationError):
            self.manager.undeploy_lab()

    def test_undeploy_lab_rejects_two_targets(self):
        with self.assertRaises(InvocationError):
            self.manager.undeploy_lab(lab_hash="H1", lab_name="x")

    def test_undeploy_lab_selection(self):
        self.manager.undeploy_lab(lab_hash="H1", selected_machines={"pc1", "pc2"})
        argv = self.last_argv()
        self.assertEqual(["pc1", "pc2"], argv[-2:])

    def test_undeploy_lab_rejects_selected_links(self):
        with self.assertRaises(NotSupportedError):
            self.manager.undeploy_lab(lab_hash="H1", selected_links={"A"})

    def test_undeploy_machine(self):
        lab = build_lab()
        self.manager.undeploy_machine(lab.get_machine("pc1"))
        argv = self.last_argv()
        self.assertEqual("lclean", argv[0])
        self.assertArgvContains(argv, ["--lab-hash", lab.hash])
        self.assertIn("pc1", argv)

    def test_undeploy_machine_keep_links_is_not_supported(self):
        lab = build_lab()
        with self.assertRaises(NotSupportedError):
            self.manager.undeploy_machine(lab.get_machine("pc1"), keep_links=True)

    def test_wipe_always_forces(self):
        self.manager.wipe()
        self.assertIn("-f", self.last_argv())
        self.assertNotIn("-a", self.last_argv())

        self.manager.wipe(all_users=True)
        self.assertIn("-a", self.last_argv())


class ConfigureTest(FakeBinaryTestCase):
    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    def test_connect_machine_to_link(self):
        lab = build_lab()
        lab.get_or_new_link("B")
        self.manager.connect_machine_to_link(lab.get_machine("pc1"), lab.get_link("B"))
        argv = self.last_argv()
        self.assertEqual("lconfig", argv[0])
        self.assertArgvContains(argv, ["--lab-hash", lab.hash])
        self.assertArgvContains(argv, ["-n", "pc1"])
        self.assertArgvContains(argv, ["--add", "B"])

    def test_connect_machine_to_link_with_mac(self):
        lab = build_lab()
        lab.get_or_new_link("B")
        self.manager.connect_machine_to_link(
            lab.get_machine("pc1"), lab.get_link("B"), mac_address="00:11:22:33:44:55"
        )
        self.assertArgvContains(self.last_argv(), ["--add", "B/00:11:22:33:44:55"])

    def test_connect_machine_to_link_updates_the_model(self):
        # v3.8.3 wires the model as part of the operation (`DockerManager.py:221`);
        # a client that only shelled out would leave every later read — a
        # redeploy, an incremental diff — looking at the pre-connect topology.
        lab = build_lab()
        machine, link = lab.get_machine("pc1"), lab.get_or_new_link("B")

        self.manager.connect_machine_to_link(machine, link, mac_address="00:11:22:33:44:55")

        self.assertEqual(["A", "B"], [iface.link.name for iface in machine.interfaces.values()])
        self.assertEqual("00:11:22:33:44:55", machine.interfaces[1].mac_address)
        self.assertIn("pc1", link.machines)

    def test_connect_machine_to_a_link_it_is_on_is_refused_locally(self):
        # `DockerManager.py:207-210`, before any backend call.
        lab = build_lab()
        with self.assertRaises(MachineCollisionDomainError):
            self.manager.connect_machine_to_link(lab.get_machine("pc1"), lab.get_link("A"))
        self.assertEqual([], self.calls())

    def test_disconnect_machine_from_link(self):
        lab = build_lab()
        self.manager.disconnect_machine_from_link(lab.get_machine("pc1"), lab.get_link("A"))
        self.assertArgvContains(self.last_argv(), ["--rm", "A"])

    def test_disconnect_machine_from_link_updates_the_model(self):
        lab = build_lab()
        machine, link = lab.get_machine("pc1"), lab.get_link("A")

        self.manager.disconnect_machine_from_link(machine, link)

        # `remove_interface` tombstones the slot instead of renumbering.
        self.assertEqual({0: None}, dict(machine.interfaces))
        self.assertNotIn("pc1", link.machines)

    def test_disconnect_from_a_link_the_device_is_not_on_is_refused_locally(self):
        # `DockerManager.py:259-262`.
        lab = build_lab()
        lab.get_or_new_link("B")
        with self.assertRaises(MachineCollisionDomainError):
            self.manager.disconnect_machine_from_link(lab.get_machine("pc1"), lab.get_link("B"))
        self.assertEqual([], self.calls())

    def test_disconnect_keep_link_is_not_supported(self):
        lab = build_lab()
        with self.assertRaises(NotSupportedError):
            self.manager.disconnect_machine_from_link(lab.get_machine("pc1"), lab.get_link("A"), keep_link=True)


class ExecTest(FakeBinaryTestCase):
    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    def test_stream_yields_demux_tuples(self):
        self.plan_exec([
            {"type": "stdout", "data": "hello "},
            {"type": "stdout", "data": "world\n"},
            {"type": "stderr", "data": "warn\n"},
            {"type": "exit", "code": 0},
        ])

        stream = self.manager.exec("pc1", "echo hello", lab_hash="H1")
        chunks = list(stream)

        self.assertEqual(
            [(b"hello ", None), (b"world\n", None), (None, b"warn\n")], chunks
        )
        self.assertEqual(0, stream.exit_code())

    def test_stream_next_matches_v383_iteration(self):
        self.plan_exec([{"type": "stdout", "data": "a"}, {"type": "exit", "code": 0}])
        stream = self.manager.exec("pc1", "true", lab_hash="H1")

        self.assertEqual((b"a", None), next(stream))
        with self.assertRaises(StopIteration):
            next(stream)

    def test_exit_code_is_the_remote_code(self):
        self.plan_exec([{"type": "stdout", "data": "x"}, {"type": "exit", "code": 42}])
        stream = self.manager.exec("pc1", "false", lab_hash="H1")
        # exit_code() drains the stream if the caller did not.
        self.assertEqual(42, stream.exit_code())

    def test_non_stream_returns_the_v383_tuple(self):
        self.plan_exec([
            {"type": "stdout", "data": "out1"},
            {"type": "stderr", "data": "err1"},
            {"type": "stdout", "data": "out2"},
            {"type": "exit", "code": 7},
        ])

        stdout, stderr, code = self.manager.exec("pc1", "cmd", lab_hash="H1", stream=False)
        self.assertEqual(b"out1out2", stdout)
        self.assertEqual(b"err1", stderr)
        self.assertEqual(7, code)

    def test_non_stream_empty_sides_are_none(self):
        # v3.8.3 returns the raw demux tuple (`DockerMachine.py:818`), whose
        # empty sides are None, and lab-checker's guards are `if stdout`.
        self.plan_exec([{"type": "stdout", "data": "only out"}, {"type": "exit", "code": 0}])

        stdout, stderr, code = self.manager.exec("pc1", "cmd", lab_hash="H1", stream=False)
        self.assertEqual(b"only out", stdout)
        self.assertIsNone(stderr)
        self.assertEqual(0, code)

    def test_error_event_raises_the_mapped_exception(self):
        self.plan_exec([
            {"type": "stdout", "data": "partial"},
            {"type": "error", "error": {"code": "MachineBinary",
                                        "message": "Binary `frr` not found in device `pc1`.",
                                        "binary": "frr", "machine": "pc1"}},
        ])

        stream = self.manager.exec("pc1", "frr", lab_hash="H1")
        self.assertEqual((b"partial", None), next(stream))
        with self.assertRaises(MachineBinaryError) as caught:
            next(stream)
        self.assertEqual("frr", caught.exception.binary)

    def test_interrupted_event_raises_keyboard_interrupt(self):
        self.plan_exec([{"type": "stdout", "data": "x"}, {"type": "interrupted"}])

        stream = self.manager.exec("pc1", "sleep 100", lab_hash="H1")
        next(stream)
        with self.assertRaises(KeyboardInterrupt):
            next(stream)

    def test_unknown_event_types_are_skipped(self):
        self.plan_exec([
            {"type": "stats", "sample": {"cpu": 1}},
            {"type": "stdout", "data": "ok"},
            {"type": "exit", "code": 0},
        ])

        stdout, _, code = self.manager.exec("pc1", "cmd", lab_hash="H1", stream=False)
        self.assertEqual(b"ok", stdout)
        self.assertEqual(0, code)

    def test_empty_data_events_are_not_yielded(self):
        self.plan_exec([
            {"type": "stdout", "data": ""},
            {"type": "stdout", "data": "real"},
            {"type": "exit", "code": 0},
        ])

        stream = self.manager.exec("pc1", "cmd", lab_hash="H1")
        self.assertEqual([(b"real", None)], list(stream))

    def test_a_missing_device_raises_at_call_time_not_at_iteration_time(self):
        # v3.8.3 lists the containers before it builds the stream
        # (`DockerMachine.py:779-781`), and kathara-lab-checker wraps only the
        # `exec(...)` call in try/except (e.g. `DNSAuthorityCheck.py:22-30`),
        # calling `get_output(...)` outside it. A deferred raise turns a recorded
        # FailedCheck into a failure of the whole run.
        self.plan([self.probe_response(machines=[])])

        with self.assertRaises(MachineNotRunningError) as caught:
            self.manager.exec("pc1", "true", lab_hash="H1")
        self.assertEqual("Device `pc1` is not running.", str(caught.exception))

        # Same on the non-streaming path, and no exec was ever spawned.
        self.plan([self.probe_response(machines=[])])
        with self.assertRaises(MachineNotRunningError):
            self.manager.exec("pc1", "true", lab_hash="H1", stream=False)
        self.assertEqual(["list"], sorted({call["argv"][0] for call in self.calls()}))

    def test_the_probe_is_filtered_by_device_and_scenario(self):
        self.plan_exec([{"type": "exit", "code": 0}])
        self.manager.exec("pc1", "true", lab_hash="H1", stream=False)

        probe = self.calls()[0]["argv"]
        self.assertEqual("list", probe[0])
        self.assertArgvContains(probe, ["-n", "pc1"])

    def test_a_device_of_another_scenario_does_not_satisfy_the_probe(self):
        other = dict(INVENTORY[2])
        self.plan([self.probe_response(machines=[other])])

        with self.assertRaises(MachineNotRunningError):
            self.manager.exec("r1", "true", lab_hash="H1")

    def test_a_stopped_device_still_reaches_the_binary(self):
        # v3.8.3 lists with `all=True` (`DockerMachine.py:1018`): a stopped
        # container is found, and the failure comes from the exec itself.
        stopped = dict(INVENTORY[0], status="exited")
        self.plan([
            self.probe_response(machines=[stopped]),
            self.events_response([
                {"type": "error", "error": {"code": "MachineNotRunning",
                                            "message": "Device `pc1` is not running.", "machine": "pc1"}},
            ], exit_code=1),
        ])

        stream = self.manager.exec("pc1", "true", lab_hash="H1")
        with self.assertRaises(MachineNotRunningError):
            next(stream)

    def test_a_usage_error_is_not_a_successful_empty_result(self):
        self.plan([self.probe_response(), {"stdout": "", "stderr": "unknown flag: --nope\n", "exit": 2}])

        with self.assertRaises(InvocationError) as caught:
            self.manager.exec("pc1", "true", lab_hash="H1", stream=False)
        self.assertIn("unknown flag", str(caught.exception))

    def test_abandoning_a_stream_early_raises_nothing(self):
        # The terminal-event check must not fire while the generator is being
        # closed: a consumer that stops reading (`break`) is not an error, and
        # raising out of GeneratorExit would be one.
        self.plan_exec([
            {"type": "stdout", "data": "one"},
            {"type": "stdout", "data": "two"},
            {"type": "exit", "code": 0},
        ])

        stream = self.manager.exec("pc1", "yes", lab_hash="H1")
        for chunk in stream:
            self.assertEqual((b"one", None), chunk)
            break

        del stream
        import gc
        gc.collect()

    def test_a_stream_without_a_terminal_event_is_an_error(self):
        self.plan([
            self.probe_response(),
            self.events_response([{"type": "stdout", "data": "half"}], exit_code=1),
        ])

        stream = self.manager.exec("pc1", "true", lab_hash="H1")
        self.assertEqual((b"half", None), next(stream))
        with self.assertRaises(KatharaError):
            next(stream)

    def test_a_flood_of_stderr_does_not_deadlock_the_stream(self):
        import concurrent.futures
        import json as _json

        events = [{"type": "stdout", "data": "done\n"}, {"type": "exit", "code": 0}]
        self.plan([self.probe_response(), {
            "stdout_lines": [_json.dumps(event) for event in events],
            "stderr_bytes": 1 << 20,
            "stderr_first": True,
            "exit": 0,
        }])

        stream = self.manager.exec("pc1", "deploy", lab_hash="H1")

        with concurrent.futures.ThreadPoolExecutor(max_workers=1) as pool:
            future = pool.submit(list, stream)
            try:
                chunks = future.result(timeout=30)
            except concurrent.futures.TimeoutError:
                stream._process.kill()
                self.fail("the stream deadlocked on the binary's stderr")

        self.assertEqual([(b"done\n", None)], chunks)
        self.assertEqual(0, stream.exit_code())

    def test_string_command_is_shlex_split(self):
        # v3.8.3 parity (`DockerMachine.py:803`).
        self.plan_exec([{"type": "exit", "code": 0}])
        self.manager.exec("pc1", "bash -c 'echo hi'", lab_hash="H1", stream=False)

        argv = self.last_argv()
        self.assertEqual(["pc1", "--", "bash", "-c", "echo hi"], argv[-5:])

    def test_list_command_is_passed_through(self):
        self.plan_exec([{"type": "exit", "code": 0}])
        self.manager.exec("pc1", ["ping", "-c", "1", "8.8.8.8"], lab_hash="H1", stream=False)

        argv = self.last_argv()
        self.assertEqual(["pc1", "--", "ping", "-c", "1", "8.8.8.8"], argv[-6:])

    def test_jsonl_format_and_scenario_addressing(self):
        self.plan_exec([{"type": "exit", "code": 0}])
        self.manager.exec("pc1", "true", lab_hash="H1", stream=False)

        argv = self.last_argv()
        self.assertEqual("exec", argv[0])
        self.assertArgvContains(argv, ["--format", "jsonl"])
        self.assertArgvContains(argv, ["--lab-hash", "H1"])

    def test_wait_true_adds_the_flag(self):
        self.plan_exec([{"type": "exit", "code": 0}])
        self.manager.exec("pc1", "true", lab_hash="H1", wait=True, stream=False)
        self.assertIn("--wait", self.last_argv())

    def test_wait_tuple_is_not_supported(self):
        # v3.8.3 validates `wait` *after* the container lookup, so the device has
        # to exist for this to be the error the caller sees.
        self.plan([self.probe_response()])
        with self.assertRaises(NotSupportedError):
            self.manager.exec("pc1", "true", lab_hash="H1", wait=(5, 1.0))

    def test_wait_garbage_raises_value_error(self):
        self.plan([self.probe_response()])
        with self.assertRaises(ValueError):
            self.manager.exec("pc1", "true", lab_hash="H1", wait="soon")

    def test_exec_requires_a_scenario(self):
        with self.assertRaises(InvocationError):
            self.manager.exec("pc1", "true")
        self.assertEqual([], self.calls())

    def test_exec_obj_uses_the_machine_scenario(self):
        lab = build_lab()
        self.plan_exec([{"type": "exit", "code": 0}], machines=[
            dict(INVENTORY[0], network_scenario_id=lab.hash),
        ])
        self.manager.exec_obj(lab.get_machine("pc1"), "true", stream=False)
        self.assertArgvContains(self.last_argv(), ["--lab-hash", lab.hash])


class InventoryTest(FakeBinaryTestCase):
    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    def test_get_machines_api_objects_filters_by_scenario(self):
        self.plan_result({"machines": INVENTORY})
        objects = self.manager.get_machines_api_objects(lab_hash="H1")

        self.assertEqual(["pc1", "pc2"], [m["name"] for m in objects])
        argv = self.last_argv()
        self.assertEqual("list", argv[0])
        self.assertArgvContains(argv, ["--format", "json"])

    def test_get_machines_api_objects_all_users_flag(self):
        self.plan_result({"machines": INVENTORY})
        self.manager.get_machines_api_objects(all_users=True)
        self.assertIn("-a", self.last_argv())

    def test_get_machine_api_object(self):
        self.plan_result({"machines": [INVENTORY[0]]})
        obj = self.manager.get_machine_api_object("pc1", lab_hash="H1")

        self.assertEqual("kathara_u_pc1_H1", obj["container_name"])
        self.assertArgvContains(self.last_argv(), ["-n", "pc1"])

    def test_get_machine_api_object_missing_raises(self):
        self.plan_result({"machines": []})
        with self.assertRaises(MachineNotFoundError):
            self.manager.get_machine_api_object("pc9", lab_hash="H1")

    def test_get_machines_stats_yields_inventory_keyed_by_container(self):
        self.plan_result({"machines": INVENTORY})
        stats = next(self.manager.get_machines_stats(lab_hash="H1"))

        self.assertEqual({"kathara_u_pc1_H1", "kathara_u_pc2_H1"}, set(stats))
        self.assertEqual(
            ["network_scenario_id", "name", "container_name", "user", "status", "image"],
            list(stats["kathara_u_pc1_H1"]),
        )

    def test_get_machine_stats_returns_one_device(self):
        self.plan_result({"machines": [INVENTORY[0]]})
        stats = next(self.manager.get_machine_stats("pc1", lab_hash="H1"))
        self.assertEqual("pc1", stats["name"])

    def test_get_machine_stats_returns_none_when_absent(self):
        self.plan_result({"machines": []})
        self.assertIsNone(next(self.manager.get_machine_stats("pc9", lab_hash="H1")))

    def test_release_version_and_manager_name_come_from_check(self):
        report = {"manager": "Docker (Kathara)", "manager_version": "27.3.1", "runtime_version": "go1.24.1",
                  "kathara_version": "1.0.0", "os_version": "Linux-x", "container_test":
                      {"image": "kathara/base", "ok": True, "error": None}}
        self.plan([{"stdout": json.dumps(report), "exit": 0}])
        manager_module._CHECK_REPORT.clear()
        self.addCleanup(manager_module._CHECK_REPORT.clear)

        self.assertEqual("27.3.1", self.manager.get_release_version())
        self.assertEqual("check", self.last_argv()[0])

        self.assertEqual("Docker (Kathara)", self.manager.get_formatted_manager_name())
        self.assertEqual(1, len(self.calls()))

    def test_available_managers_need_no_subprocess(self):
        names = Kathara.get_available_managers_name()
        self.assertEqual({"docker": "Docker (Kathara)", "kubernetes": "Kubernetes (Megalos)"}, names)
        self.assertEqual([], self.calls())


class UnsupportedSurfaceTest(FakeBinaryTestCase):

    def setUp(self):
        super().setUp()
        self.manager = Kathara.get_instance()

    def test_unsupported_methods(self):
        lab = build_lab()
        machine = lab.get_machine("pc1")
        link = lab.get_link("A")

        cases = {
            "get_link_api_object": lambda: self.manager.get_link_api_object("A", lab_hash="H1"),
            "get_links_api_objects": lambda: self.manager.get_links_api_objects(lab_hash="H1"),
            "get_lab_from_api": lambda: self.manager.get_lab_from_api(lab_hash="H1"),
            "update_lab_from_api": lambda: self.manager.update_lab_from_api(lab),
            "get_links_stats": lambda: self.manager.get_links_stats(lab_hash="H1"),
            "get_link_stats": lambda: self.manager.get_link_stats("A", lab_hash="H1"),
            "get_link_stats_obj": lambda: self.manager.get_link_stats_obj(link),
        }

        for name, call in cases.items():
            with self.subTest(method=name):
                with self.assertRaises(NotSupportedError):
                    call()

        self.assertEqual([], self.calls(), "an unsupported call must not spawn the binary")

    def test_copy_files_uses_api_bridge(self):
        lab = build_lab()
        machine = lab.get_machine("pc1")
        self.manager.copy_files(machine, {"/etc/message": io.BytesIO(b"hello\x00"),
                                          "/etc/hosts": "/tmp/hosts"})
        self.assertIn("copy-files", self.last_argv())
        payload = json.loads(self.last_stdin())
        self.assertEqual(lab.hash, payload["lab_hash"])
        self.assertEqual("pc1", payload["machine_name"])
        self.assertEqual("/tmp/hosts", payload["files"][1]["host_path"])
        self.assertEqual("aGVsbG8A", payload["files"][0]["content_base64"])

    def test_retrieve_files_uses_api_bridge(self):
        lab = build_lab()
        self.manager.retrieve_files(lab.get_machine("pc1"), "/etc/hosts", "/tmp/hosts")
        self.assertIn("retrieve-files", self.last_argv())
        payload = json.loads(self.last_stdin())
        self.assertEqual("/etc/hosts", payload["src"])
        self.assertEqual("/tmp/hosts", payload["dst"])

    def test_undeploy_link_and_check_image_use_api_bridge(self):
        lab = build_lab()
        self.manager.undeploy_link(lab.get_link("A"))
        self.assertIn("undeploy-link", self.last_argv())
        self.assertEqual("A", json.loads(self.last_stdin())["link_name"])
        self.manager.check_image("kathara/base")
        self.assertIn("check-image", self.last_argv())
        self.assertEqual("kathara/base", json.loads(self.last_stdin())["image_name"])

    def test_connect_tty_by_hash_alone_is_not_supported(self):
        with self.assertRaises(NotSupportedError):
            self.manager.connect_tty("pc1", lab_hash="H1")
        self.assertEqual([], self.calls())

    def test_connect_tty_uses_the_directory_when_there_is_one(self):
        lab = Lab(None, path=self.tmpdir)
        lab.new_machine("pc1")
        self.plan([{"stdout": "", "exit": 0}])

        self.manager.connect_tty("pc1", lab=lab, shell="/bin/sh", logs=True)

        argv = self.last_argv()
        self.assertEqual("connect", argv[0])
        self.assertNotIn("--format", argv)
        self.assertArgvContains(argv, ["--shell", "/bin/sh"])
        self.assertIn("-l", argv)
        self.assertEqual("pc1", argv[-1])

    def test_connect_tty_of_a_named_scenario_ignores_its_directory(self):
        directory = os.path.join(self.tmpdir, "scenario")
        os.makedirs(directory)
        with open(os.path.join(directory, "lab.conf"), "w") as lab_conf:
            # A stale on-disk name, i.e. the identity the directory carries and
            # the deployed scenario does not.
            lab_conf.write('LAB_NAME=Something Else\npc1[0]="A"\n')

        lab = Lab("BGP Announcement", path=directory)
        lab.new_machine("router1")
        self.assertTrue(lab.has_host_path())

        self.plan([{"stdout": "", "exit": 0}])
        seen = {}

        original = manager_module._proc.run_interactive

        def capture(command, args):
            passed = args[args.index("-d") + 1]
            with open(os.path.join(passed, "lab.conf")) as lab_conf:
                seen["lab_conf"] = lab_conf.read()
            seen["directory"] = passed
            return original(command, args)

        manager_module._proc.run_interactive = capture
        try:
            self.manager.connect_tty("router1", lab=lab)
        finally:
            manager_module._proc.run_interactive = original

        # Not the scenario's own directory: a synthesised one carrying the name
        # the scenario was deployed under, and nothing else.
        self.assertNotEqual(directory, seen["directory"].rstrip(os.sep))
        self.assertEqual("LAB_NAME=BGP Announcement\n", seen["lab_conf"])
        self.assertEqual(lab.hash, generate_urlsafe_hash("BGP Announcement"))
        self.assertFalse(os.path.exists(seen["directory"]))

    def test_connect_tty_by_name_ignores_a_directory_the_lab_also_has(self):
        # The same rule reached through `lab_name=`, which carries no directory
        # at all: unchanged, and pinned here so the branch order cannot be
        # re-derived from the `lab=` case alone.
        self.plan([{"stdout": "", "exit": 0}])
        self.manager.connect_tty("pc1", lab_name="Test scenario")

        argv = self.last_argv()
        self.assertEqual("connect", argv[0])
        self.assertIn("-d", argv)
        self.assertNotIn("-v", argv)

    def test_connect_tty_vlab(self):
        self.plan([{"stdout": "", "exit": 0}])
        self.manager.connect_tty("pc1", lab_name="kathara_vlab")
        self.assertIn("-v", self.last_argv())

    def test_connect_tty_by_name_addresses_the_scenario_through_lab_name(self):
        self.plan([{"stdout": "", "exit": 0}])
        seen = {}

        original = manager_module._proc.run_interactive

        def capture(command, args):
            directory = args[args.index("-d") + 1]
            with open(os.path.join(directory, "lab.conf")) as lab_conf:
                seen["lab_conf"] = lab_conf.read()
            seen["directory"] = directory
            return original(command, args)

        manager_module._proc.run_interactive = capture
        try:
            self.manager.connect_tty("router1", lab_name="BGP Announcement")
        finally:
            manager_module._proc.run_interactive = original

        self.assertEqual("LAB_NAME=BGP Announcement\n", seen["lab_conf"])
        # The scenario directory is a temporary: it must not outlive the call.
        self.assertFalse(os.path.exists(seen["directory"]))

        argv = self.last_argv()
        self.assertEqual("connect", argv[0])
        self.assertEqual("router1", argv[-1])
        self.assertNotIn("--format", argv)

    def test_connect_tty_by_lab_object_without_a_path(self):
        lab = build_lab()  # memory FS, no host path, but it has a name
        self.plan([{"stdout": "", "exit": 0}])

        self.manager.connect_tty("pc1", lab=lab)

        argv = self.last_argv()
        self.assertEqual("connect", argv[0])
        self.assertIn("-d", argv)

    def test_connect_tty_to_an_unwritable_name_is_refused(self):

        with self.assertRaises(NotSupportedError):
            self.manager.connect_tty("pc1", lab_name="a=b")
        self.assertEqual([], self.calls())

    def test_connect_tty_to_a_name_that_does_not_survive_stripping_is_refused(self):
        # A `LAB_NAME=` value is `.strip()`ed by both parsers (`LabParser.py:52`,
        # `labfile/labconf.go` `applyLabMetadata`), so ` foo ` written into the
        # synthesised lab.conf comes back as `foo`: the scenario is deployed
        # under `hash(" foo ")` and `connect -d <dir>` would address
        # `hash("foo")`. Same misaddressing class as an unwritable name, so the
        # same refusal — including for a whitespace-only name, which strips to
        # the empty string.
        for name in (" foo ", "foo\t", "\nfoo", " "):
            with self.subTest(name=name):
                with self.assertRaises(NotSupportedError):
                    self.manager.connect_tty("pc1", lab_name=name)
                self.assertEqual([], self.calls())


if __name__ == "__main__":
    unittest.main()
