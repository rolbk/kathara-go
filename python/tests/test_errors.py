"""Error-envelope decoding: `ERROR_CODES.md` is the whole test oracle here.

The registry is FROZEN and §1 requires the class↔code mapping to be exhaustive
and injective. These tests assert exactly that, code by code, through a real
subprocess emitting a real envelope.
"""

import unittest

from _support import FakeBinaryTestCase

from Kathara import _proc
from Kathara.exceptions import (
    InvalidImageArchitectureError,
    InvocationError,
    KatharaError,
    MachineBinaryError,
    MachineCollisionDomainError,
    MachineNotFoundError,
    MachineNotRunningError,
    NotSupportedError,
    SettingsError,
)
from Kathara.manager.Kathara import Kathara

#: Every code of `ERROR_CODES.md` §1 with a representative message drawn from
#: the §2 template catalogue, and the structured fields §5.4 pins for it.
CODE_FIXTURES = [
    # -- §1.1 the 33 Kathara exception classes ------------------------------
    ("ClassNotFound", "Unrecognized command `bogus`.", {}),
    ("HTTPConnection", "Connection to GitHub failed.", {}),
    ("Instantiation", "This class is a singleton!", {}),
    ("Invocation", "You can either select or exclude devices.", {}),
    ("Settings", "Settings file is not valid: Not a valid JSON. Fix it or delete it before launching.", {}),
    ("SettingsNotFound", "Settings file not found in path `/x/kathara.conf`.", {"path": "/x/kathara.conf"}),
    ("DockerDaemonConnection",
     "Cannot connect to Docker Daemon, this may indicate that it is not running. connection refused", {}),
    ("NotSupported", "Not Supported: Unable to update a running device.", {}),
    ("Privilege", "You must be root in order to wipe all Kathara devices of all users.", {}),
    ("InterfaceNotFound", "Interface not found.", {}),
    ("HostArchitecture", "Not implemented for host architecture `ppc64le`.", {"arch": "ppc64le"}),
    ("LabAlreadyExists", "Previous network scenario execution is still terminating. Please wait.", {}),
    ("LabNotFound", "Device `pc1` is not associated to a network scenario.", {"machine": "pc1"}),
    ("EmptyLab", "No devices in the current network scenario.", {}),
    ("MachineDependency", "Machines' dependency loop in lab.dep file.", {}),
    ("MountDenied", "Host drive is not shared with Docker.", {}),
    ("MachineAlreadyExists", "Device with name `pc1` already exists.", {"machine": "pc1"}),
    ("NonSequentialMachineInterface", "Interface `1` missing on device `pc1`.", {"iface": 1, "machine": "pc1"}),
    ("MachineOption", "Memory value not valid on `pc1`.", {"machine": "pc1", "option": "mem"}),
    ("MachineCollisionDomain", "Device `pc1` is already connected to collision domain `A`.",
     {"machine": "pc1", "link": "A"}),
    ("MachineNotFound", "Device `pc1` not found.", {"machine": "pc1"}),
    ("MachineNotRunning", "Device `pc1` is not running.", {"machine": "pc1"}),
    ("MachineNotReady", "Device `pc1` is not ready.", {"machine": "pc1"}),
    ("MachineBinary", "Binary `frr` not found in device `pc1`.", {"binary": "frr", "machine": "pc1"}),
    ("InterfaceMacAddress", "MAC address 00:zz on interface `0` of device `pc1` is invalid.",
     {"mac": "00:zz", "iface": 0, "machine": "pc1"}),
    ("LinkNotFound", "Collision Domain `A` not found.", {"link": "A"}),
    ("LinkAlreadyExists", "Collision domain A is already the network scenario.", {"link": "A"}),
    ("Test", "Test failure.", {}),
    ("MachineSignatureNotFound", "Signature for device `pc1` not found!", {"machine": "pc1"}),
    ("InvalidImageArchitecture",
     "Docker Image `kathara/base` is not compatible with your host architecture `arm64`",
     {"image": "kathara/base", "arch": "arm64"}),
    ("DockerImageNotFound",
     "Docker Image `kathara/nope` is not available neither on Docker Hub nor in local repository!",
     {"image": "kathara/nope"}),
    ("DockerPlugin", "Kathara Network Plugin not found on remote Docker connection.", {}),
    ("KubernetesConfigMap", "Unable to upload device folder. Maximum supported size: 3.0 MB. Current: 4.0 MB.", {}),
    # -- §1.2 the 8 user-reachable builtins ---------------------------------
    ("Syntax", "In lab.conf - Line 3: `pc1[0]=A/`.", {"file": "lab.conf", "line": 3}),
    ("Value", "In lab.conf - Line 1: `shared` is a reserved name, you can not use it for a device.",
     {"file": "lab.conf", "line": 1}),
    ("OS", "No lab.conf in given directory.", {}),
    ("FileNotFound", "Cannot find `iptables` in the host.", {}),
    ("FileExists", "Path `/nope` does not exist.", {"path": "/nope"}),
    ("NotADirectory", "Path `/etc/hosts` must be a directory.", {"path": "/etc/hosts"}),
    ("Permission",
     "To mount volume `/a` in `/b` you miss the following permissions: `read (r)`.", {}),
    ("Connection", "Cannot read Kubernetes configuration.", {}),
    # -- §1.3 third-party passthrough ---------------------------------------
    ("DockerAPI", "500 Server Error: Internal Server Error", {}),
    ("KubernetesAPI", "(403) Reason: Forbidden", {}),
    # -- §1.4 port-new codes ------------------------------------------------
    ("FeatureNotAvailable", "lab.ext external links are not supported in this release. Use Kathará 3.8.x.",
     {"feature": "lab.ext"}),
    ("InternalError", "something went sideways", {}),
    ("ConfirmationRequired", "Confirmation required: re-run with `--force` to wipe Kathara.", {}),
]

#: `ERROR_CODES.md` §1: "46 codes total".
EXPECTED_CODE_COUNT = 46


class RegistryShapeTest(unittest.TestCase):
    def test_registry_is_complete(self):
        self.assertEqual(EXPECTED_CODE_COUNT, len(_proc.CODE_TO_EXCEPTION))

    def test_every_code_has_a_fixture(self):
        self.assertEqual(
            sorted(_proc.CODE_TO_EXCEPTION), sorted(code for code, _, _ in CODE_FIXTURES)
        )

    def test_mapping_is_injective_except_the_generic_bucket(self):
        # §4: "the mapping is injective". The only many-to-one entries are the
        # codes with no v3.8.3 class, which all fall into KatharaError, plus
        # FeatureNotAvailable, which §1.4 explicitly routes to NotSupportedError.
        seen = {}
        for code, cls in _proc.CODE_TO_EXCEPTION.items():
            if cls in (KatharaError,):
                continue
            if cls is NotSupportedError and code == "FeatureNotAvailable":
                continue
            self.assertNotIn(cls, seen, "%s and %s share a class" % (code, seen.get(cls)))
            seen[cls] = code

    def test_generic_bucket_membership(self):
        generic = {code for code, cls in _proc.CODE_TO_EXCEPTION.items() if cls is KatharaError}
        self.assertEqual({"DockerAPI", "KubernetesAPI", "InternalError", "ConfirmationRequired"}, generic)


class ErrorEnvelopeTest(FakeBinaryTestCase):
    def test_every_code_maps_to_its_class_with_exact_message(self):
        manager = Kathara.get_instance()

        for code, message, fields in CODE_FIXTURES:
            with self.subTest(code=code):
                self.plan_error(code, message, **fields)
                expected = _proc.CODE_TO_EXCEPTION[code]

                with self.assertRaises(expected) as caught:
                    manager.undeploy_lab(lab_hash="deadbeef")

                # `ERROR_CODES.md` §4: str(e) parity is load-bearing.
                self.assertEqual(message, str(caught.exception))

    def test_machine_binary_error_attributes(self):
        # `ERROR_CODES.md` §4 "Attributes, for API parity".
        manager = Kathara.get_instance()
        self.plan_error("MachineBinary", "Binary `frr` not found in device `pc1`.", binary="frr", machine="pc1")

        with self.assertRaises(MachineBinaryError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertEqual("frr", caught.exception.binary)
        self.assertEqual("pc1", caught.exception.machine_name)

    def test_invalid_image_architecture_attributes_and_base_class(self):
        manager = Kathara.get_instance()
        self.plan_error(
            "InvalidImageArchitecture",
            "Docker Image `kathara/base` is not compatible with your host architecture `arm64`",
            image="kathara/base", arch="arm64",
        )

        with self.assertRaises(ValueError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertIsInstance(caught.exception, InvalidImageArchitectureError)
        self.assertEqual("kathara/base", caught.exception.image_name)
        self.assertEqual("arm64", caught.exception.arch)

    def test_settings_error_message_is_not_wrapped_twice(self):
        # SettingsError.__init__ builds "Settings file is not valid: ... " itself.
        manager = Kathara.get_instance()
        message = "Settings file is not valid: Not a valid JSON. Fix it or delete it before launching."
        self.plan_error("Settings", message)

        with self.assertRaises(SettingsError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertEqual(message, str(caught.exception))

    def test_unknown_code_falls_back_to_kathara_error(self):
        # `JSON_CLI_CONTRACT.md` §9.2: clients MUST map unknown codes to a
        # generic error rather than crashing.
        manager = Kathara.get_instance()
        self.plan_error("SomeFutureCode", "a thing from the future", detail="x")

        with self.assertRaises(KatharaError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertEqual("a thing from the future", str(caught.exception))
        self.assertEqual("SomeFutureCode", caught.exception.code)
        self.assertEqual({"detail": "x"}, caught.exception.fields)

    def test_lab_checker_catches_its_four_classes_by_name(self):
        # `ERROR_CODES.md` §4: the compatibility-critical set.
        manager = Kathara.get_instance()
        pairs = [
            ("MachineNotRunning", MachineNotRunningError),
            ("MachineNotFound", MachineNotFoundError),
            ("MachineBinary", MachineBinaryError),
            ("MachineCollisionDomain", MachineCollisionDomainError),
        ]

        classes = [cls for _, cls in pairs]
        self.assertEqual(len(set(classes)), len(classes), "the four classes must be distinct")

        for code, cls in pairs:
            with self.subTest(code=code):
                self.plan_error(code, "boom", binary="b", machine="pc1")
                with self.assertRaises(cls):
                    manager.undeploy_lab(lab_hash="deadbeef")

    def test_sibling_errors_array_is_ignored(self):
        # `ERROR_CODES.md` §6.5: the client raises the primary error only.
        manager = Kathara.get_instance()
        self.plan([{
            "exit": 1,
            "stdout": (
                '{"error":{"code":"MachineBinary","message":"Binary `frr` not found in device `pc1`.",'
                '"binary":"frr","machine":"pc1"},'
                '"errors":[{"code":"MachineBinary","message":"Binary `frr` not found in device `pc1`.",'
                '"binary":"frr","machine":"pc1"},{"code":"DockerAPI","message":"other"}]}'
            ),
        }])

        with self.assertRaises(MachineBinaryError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertEqual("Binary `frr` not found in device `pc1`.", str(caught.exception))

    def test_interrupt_envelope_raises_keyboard_interrupt(self):
        # Contract §6.2: exit 0 with {"interrupted":true}.
        manager = Kathara.get_instance()
        self.plan([{"stdout": '{"interrupted":true}', "exit": 0}])

        with self.assertRaises(KeyboardInterrupt):
            manager.undeploy_lab(lab_hash="deadbeef")

    def test_usage_error_raises_invocation_error(self):
        # Contract §5.5: exit 2, usage text on stderr, nothing on stdout.
        manager = Kathara.get_instance()
        self.plan([{"stdout": "", "stderr": "usage: kathara lclean ...", "exit": 2}])

        with self.assertRaises(InvocationError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertIn("usage: kathara lclean", str(caught.exception))

    def test_garbage_on_stdout_raises_kathara_error(self):
        manager = Kathara.get_instance()
        self.plan([{"stdout": "not json at all", "exit": 0}])

        with self.assertRaises(KatharaError):
            manager.undeploy_lab(lab_hash="deadbeef")

    def test_error_envelope_that_is_not_an_object_raises_kathara_error(self):
        # Contract §5.1 types `error` as an object. A build that sends a bare
        # string is not speaking the contract, and the client must answer that
        # the way §9.2 answers any envelope it cannot read — with the base
        # `KatharaError` — not with an `AttributeError` from its own decoder.
        manager = Kathara.get_instance()
        self.plan([{"stdout": '{"error":"boom"}', "exit": 1}])

        with self.assertRaises(KatharaError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertIn("boom", str(caught.exception))
        self.assertIsNone(caught.exception.code)

    def test_error_envelope_with_a_non_string_code_raises_kathara_error(self):
        # `dict.get` on an unhashable key raises `TypeError`; an unknown code
        # buckets to `KatharaError` (§9.2) and so must an unreadable one.
        manager = Kathara.get_instance()
        self.plan([{"stdout": '{"error":{"code":["MachineNotFound"],"message":"boom"}}', "exit": 1}])

        with self.assertRaises(KatharaError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertEqual("boom", str(caught.exception))

    def test_jsonl_error_event_that_is_not_an_object_raises_kathara_error(self):
        # The same shape guard on the streaming side (§4.1's `error` event).
        manager = Kathara.get_instance()
        self.plan([
            self.probe_response(),
            self.events_response([{"type": "error", "error": "boom"}], exit_code=1),
        ])

        stream = manager.exec("pc1", ["true"], lab_hash="H1")
        with self.assertRaises(KatharaError):
            list(stream)

    def test_jsonl_exit_event_with_a_null_code_is_a_protocol_error(self):
        # §4.1 types the `exit` event's `code` as an int, so `null` cannot come
        # from a conforming binary — but `int(None)` would answer it with a
        # `TypeError` raised from inside the client's own generator.
        manager = Kathara.get_instance()
        self.plan([
            self.probe_response(),
            self.events_response([{"type": "stdout", "data": "hi"}, {"type": "exit", "code": None}]),
        ])

        stream = manager.exec("pc1", ["true"], lab_hash="H1")
        with self.assertRaises(KatharaError) as caught:
            list(stream)

        self.assertIn("`exit` event", str(caught.exception))

    def test_jsonl_exit_event_with_a_string_code_is_a_protocol_error(self):
        manager = Kathara.get_instance()
        self.plan([
            self.probe_response(),
            self.events_response([{"type": "exit", "code": "0"}]),
        ])

        stream = manager.exec("pc1", ["true"], lab_hash="H1")
        with self.assertRaises(KatharaError):
            list(stream)

    def test_empty_stdout_with_nonzero_exit_raises_kathara_error(self):
        manager = Kathara.get_instance()
        self.plan([{"stdout": "", "stderr": "panic: boom", "exit": 3}])

        with self.assertRaises(KatharaError) as caught:
            manager.undeploy_lab(lab_hash="deadbeef")

        self.assertIn("panic: boom", str(caught.exception))


if __name__ == "__main__":
    unittest.main()
