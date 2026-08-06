#!/usr/bin/env python3
"""A fake Kathará binary that speaks the JSON CLI contract.

The client's whole job is to build argv, feed stdin, and decode stdout, so the
unit tests replace the Go binary with this script (via ``$KATHARA_BIN``) and
assert on both halves of the conversation.

Environment:

``FAKE_KATHARA_PLAN``
    Path to a JSON plan file::

        {"responses": [{"stdout": "...", "stderr": "...", "exit": 0}, ...]}

    Responses are consumed in order across invocations; the last one repeats if
    the client calls more often than the plan provides. ``stdout`` may also be
    given as ``stdout_lines`` (a list) for ``jsonl`` mode, which is joined with
    newlines. Omitting the plan entirely yields ``{}`` on stdout and exit 0.

``FAKE_KATHARA_LOG``
    Path to a JSONL file the fake appends one record per invocation to::

        {"argv": [...], "stdin_b64": "...", "stdin_len": 12}

Only the standard library is used, and nothing here imports Kathara: the fake
must stay honest about being a separate process.
"""

import base64
import json
import os
import sys


def _load_plan():
    path = os.environ.get("FAKE_KATHARA_PLAN")
    if not path or not os.path.exists(path):
        return {"responses": [{"stdout": "{}", "exit": 0}]}

    with open(path, "r") as plan_file:
        return json.load(plan_file)


def _consume_index(plan_path, count):
    """Return the index of the response to use for this invocation."""
    counter_path = (plan_path or "fake-kathara") + ".count"
    index = 0
    if os.path.exists(counter_path):
        with open(counter_path, "r") as counter_file:
            try:
                index = int(counter_file.read().strip() or "0")
            except ValueError:
                index = 0

    with open(counter_path, "w") as counter_file:
        counter_file.write(str(index + 1))

    return min(index, count - 1)


def main() -> int:
    argv = sys.argv[1:]

    stdin_data = b""
    if not sys.stdin.isatty():
        try:
            stdin_data = sys.stdin.buffer.read()
        except (OSError, ValueError):
            stdin_data = b""

    log_path = os.environ.get("FAKE_KATHARA_LOG")
    if log_path:
        record = {
            "argv": argv,
            "stdin_b64": base64.b64encode(stdin_data).decode("ascii"),
            "stdin_len": len(stdin_data),
        }
        with open(log_path, "a") as log_file:
            log_file.write(json.dumps(record) + "\n")

    plan = _load_plan()
    responses = plan.get("responses") or [{"stdout": "{}", "exit": 0}]
    response = responses[_consume_index(os.environ.get("FAKE_KATHARA_PLAN"), len(responses))]

    if "stdout_lines" in response:
        stdout = "".join(line + "\n" for line in response["stdout_lines"])
    else:
        stdout = response.get("stdout", "")
        if stdout and not stdout.endswith("\n"):
            stdout += "\n"

    stderr = response.get("stderr", "")
    if "stderr_bytes" in response:
        # A log flood, for the stderr-buffering test.
        stderr = "x" * int(response["stderr_bytes"])

    def write_stderr():
        if stderr:
            sys.stderr.write(stderr if stderr.endswith("\n") else stderr + "\n")
            sys.stderr.flush()

    # `stderr_first` makes the fake log before it emits any protocol, which is
    # what a real deploy does and what deadlocks a client that reads stdout to
    # exhaustion before touching stderr.
    if response.get("stderr_first"):
        write_stderr()

    if stdout:
        sys.stdout.write(stdout)
        sys.stdout.flush()

    if not response.get("stderr_first"):
        write_stderr()

    return int(response.get("exit", 0))


if __name__ == "__main__":
    sys.exit(main())
