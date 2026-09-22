"""Shared scaffolding for the client unit tests.

Every test that touches the wire layer runs against :mod:`fake_kathara` — a
stdlib-only script that speaks the JSON command protocol — installed as the binary
through ``$KATHARA_BIN``. The tests therefore exercise a real subprocess, a real
pipe and a real JSON decode, and never monkeypatch :mod:`subprocess`.
"""

import base64
import json
import os
import shutil
import stat
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
PACKAGE_ROOT = os.path.dirname(HERE)

if PACKAGE_ROOT not in sys.path:
    sys.path.insert(0, PACKAGE_ROOT)

FAKE_SCRIPT = os.path.join(HERE, "fake_kathara.py")


class FakeBinaryTestCase(unittest.TestCase):
    """Base class that installs the fake binary for the duration of a test."""

    def setUp(self) -> None:
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-client-test-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)

        # A launcher with an explicit interpreter, so the test does not depend
        # on `python3` being the right interpreter on PATH.
        self.binary = os.path.join(self.tmpdir, "kathara")
        with open(self.binary, "w") as launcher:
            launcher.write("#!/bin/sh\nexec %s %s \"$@\"\n" % (
                json.dumps(sys.executable).strip('"'), json.dumps(FAKE_SCRIPT).strip('"')
            ))
        os.chmod(self.binary, os.stat(self.binary).st_mode | stat.S_IEXEC | stat.S_IXGRP | stat.S_IXOTH)

        self.plan_path = os.path.join(self.tmpdir, "plan.json")
        self.log_path = os.path.join(self.tmpdir, "calls.jsonl")

        self._saved_env = {
            key: os.environ.get(key)
            for key in ("KATHARA_BIN", "FAKE_KATHARA_PLAN", "FAKE_KATHARA_LOG")
        }
        os.environ["KATHARA_BIN"] = self.binary
        os.environ["FAKE_KATHARA_PLAN"] = self.plan_path
        os.environ["FAKE_KATHARA_LOG"] = self.log_path
        self.addCleanup(self._restore_env)

        self.plan([{"stdout": "{}", "exit": 0}])

    def _restore_env(self) -> None:
        for key, value in self._saved_env.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value

    # -- plan / log helpers ------------------------------------------------

    def plan(self, responses) -> None:
        """Set the fake binary's scripted responses."""
        with open(self.plan_path, "w") as plan_file:
            json.dump({"responses": responses}, plan_file)

        counter = self.plan_path + ".count"
        if os.path.exists(counter):
            os.remove(counter)

    def plan_result(self, document) -> None:
        """Plan one successful ``--format json`` result envelope."""
        self.plan([{"stdout": json.dumps(document), "exit": 0}])

    def plan_error(self, code, message, **fields) -> None:
        """Plan one ``--format json`` error envelope."""
        error = {"code": code, "message": message}
        error.update(fields)
        self.plan([{"stdout": json.dumps({"error": error}), "exit": 1}])

    def plan_events(self, events) -> None:
        """Plan one ``--format jsonl`` event stream."""
        self.plan([self.events_response(events)])

    @staticmethod
    def events_response(events, exit_code=0):
        """One planned response carrying a ``--format jsonl`` event stream."""
        return {"stdout_lines": [json.dumps(event) for event in events], "exit": exit_code}

    @staticmethod
    def probe_response(machines=None):
        """The `kathara list` response `exec` makes before it opens a stream.

        v3.8.3 looks the container up before creating the stream
        (`DockerMachine.py:779-781`), so the client does too and every `exec`
        costs two invocations of the binary.
        """
        if machines is None:
            machines = [{
                "network_scenario_id": "H1", "name": "pc1", "container_name": "kathara_u_pc1_H1",
                "user": "u", "status": "running", "image": "kathara/base",
            }]
        return {"stdout": json.dumps({"machines": machines}), "exit": 0}

    def plan_exec(self, events, machines=None) -> None:
        """Plan both halves of one ``exec`` call: the probe, then the stream."""
        self.plan([self.probe_response(machines), self.events_response(events)])

    def calls(self):
        """Return every invocation of the fake binary, in order."""
        if not os.path.exists(self.log_path):
            return []

        records = []
        with open(self.log_path, "r") as log_file:
            for line in log_file:
                line = line.strip()
                if line:
                    records.append(json.loads(line))
        return records

    def last_call(self):
        records = self.calls()
        self.assertTrue(records, "the binary was never invoked")
        return records[-1]

    def last_argv(self):
        return self.last_call()["argv"]

    def last_stdin(self) -> bytes:
        return base64.b64decode(self.last_call()["stdin_b64"])

    # -- assertions --------------------------------------------------------

    def assertArgvContains(self, argv, sequence):
        """Assert that `sequence` appears as a contiguous run inside `argv`."""
        for index in range(len(argv) - len(sequence) + 1):
            if argv[index:index + len(sequence)] == list(sequence):
                return
        self.fail("%r does not contain %r" % (argv, list(sequence)))
