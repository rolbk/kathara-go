"""Subprocess plumbing for the Kathará JSON CLI contract.

This module is the whole wire layer of the Python client: it spawns the Go
``kathara`` binary, decodes ``--format json`` / ``--format jsonl`` stdout, and
turns ``{"error":{"code":...}}`` envelopes back into the v3.8.3 exception
classes.

Normative references (both FROZEN):

* ``docs/port/JSON_CLI_CONTRACT.md`` — envelopes, exit codes, stream discipline;
* ``docs/port/ERROR_CODES.md`` — the ``code`` → exception-class mapping, which
  §1 requires to be exhaustive and injective.

Stream discipline implemented here (contract §1.3): the child's **stdout is
protocol** and is parsed; the child's **stderr is logs** and is captured, never
forwarded to the parent's stderr, and re-emitted through :mod:`logging` at DEBUG
level so a library consumer's own output stays clean.
"""

import json
import logging
import os
import subprocess
import tempfile
from typing import Any, Dict, Generator, IO, List, Optional, Tuple

from . import _bin
from .exceptions import (
    ClassNotFoundError,
    DockerDaemonConnectionError,
    DockerImageNotFoundError,
    DockerPluginError,
    EmptyLabError,
    HostArchitectureError,
    HTTPConnectionError,
    InstantiationError,
    InterfaceMacAddressError,
    InterfaceNotFoundError,
    InvalidImageArchitectureError,
    InvocationError,
    KatharaError,
    KubernetesConfigMapError,
    LabAlreadyExistsError,
    LabNotFoundError,
    LinkAlreadyExistsError,
    LinkNotFoundError,
    MachineAlreadyExistsError,
    MachineBinaryError,
    MachineCollisionDomainError,
    MachineDependencyError,
    MachineNotFoundError,
    MachineNotReadyError,
    MachineNotRunningError,
    MachineOptionError,
    MachineSignatureNotFoundError,
    MountDeniedError,
    NonSequentialMachineInterfaceError,
    NotSupportedError,
    PrivilegeError,
    SettingsError,
    SettingsNotFoundError,
    TestError,
)

__all__ = [
    'CODE_TO_EXCEPTION', 'exception_from_error', 'run_json', 'stream_jsonl', 'run_interactive',
    'collect_exec', 'ExecStream', 'USAGE_EXIT_CODE',
]

#: argparse/cobra usage failures. Contract §5.5: no JSON on stdout, ever.
USAGE_EXIT_CODE: int = 2

#: `ERROR_CODES.md` §1 — the complete registry, 46 codes.
#:
#: * §1.1 the 33 Kathará exception classes, 1:1;
#: * §1.2 the 8 user-reachable builtins;
#: * §1.3 the 2 third-party passthrough codes and §1.4 the 3 port-new codes,
#:   none of which has a v3.8.3 class — they raise :class:`KatharaError` with
#:   ``code`` populated, which is also what §9.2 mandates for codes added by a
#:   newer binary.
CODE_TO_EXCEPTION: Dict[str, type] = {
    # -- §1.1 Kathara exception classes (33) --------------------------------
    'ClassNotFound': ClassNotFoundError,
    'HTTPConnection': HTTPConnectionError,
    'Instantiation': InstantiationError,
    'Invocation': InvocationError,
    'Settings': SettingsError,
    'SettingsNotFound': SettingsNotFoundError,
    'DockerDaemonConnection': DockerDaemonConnectionError,
    'NotSupported': NotSupportedError,
    'Privilege': PrivilegeError,
    'InterfaceNotFound': InterfaceNotFoundError,
    'HostArchitecture': HostArchitectureError,
    'LabAlreadyExists': LabAlreadyExistsError,
    'LabNotFound': LabNotFoundError,
    'EmptyLab': EmptyLabError,
    'MachineDependency': MachineDependencyError,
    'MountDenied': MountDeniedError,
    'MachineAlreadyExists': MachineAlreadyExistsError,
    'NonSequentialMachineInterface': NonSequentialMachineInterfaceError,
    'MachineOption': MachineOptionError,
    'MachineCollisionDomain': MachineCollisionDomainError,
    'MachineNotFound': MachineNotFoundError,
    'MachineNotRunning': MachineNotRunningError,
    'MachineNotReady': MachineNotReadyError,
    'MachineBinary': MachineBinaryError,
    'InterfaceMacAddress': InterfaceMacAddressError,
    'LinkNotFound': LinkNotFoundError,
    'LinkAlreadyExists': LinkAlreadyExistsError,
    'Test': TestError,
    'MachineSignatureNotFound': MachineSignatureNotFoundError,
    'InvalidImageArchitecture': InvalidImageArchitectureError,
    'DockerImageNotFound': DockerImageNotFoundError,
    'DockerPlugin': DockerPluginError,
    'KubernetesConfigMap': KubernetesConfigMapError,
    # -- §1.2 builtins that reach users (8) ---------------------------------
    'Syntax': SyntaxError,
    'Value': ValueError,
    # `IOError` is an alias of `OSError` in Python 3, so one code, one class.
    'OS': OSError,
    'FileNotFound': FileNotFoundError,
    # Semantically inverted in Python (raised when the path does NOT exist);
    # kept for message parity, `ERROR_CODES.md` §0.2.
    'FileExists': FileExistsError,
    'NotADirectory': NotADirectoryError,
    'Permission': PermissionError,
    'Connection': ConnectionError,
    # -- §1.3 third-party passthrough (2) -----------------------------------
    'DockerAPI': KatharaError,
    'KubernetesAPI': KatharaError,
    # -- §1.4 port-new codes (3) --------------------------------------------
    # `ERROR_CODES.md` §5: "The Python client raises
    # Kathara.exceptions.NotSupportedError with the message verbatim."
    'FeatureNotAvailable': NotSupportedError,
    'InternalError': KatharaError,
    'ConfirmationRequired': KatharaError,
}

#: Keys of the error object that are not structured fields.
_ERROR_ENVELOPE_KEYS = ('code', 'message')

_logger = logging.getLogger("Kathara")


def _fields(error: Dict[str, Any]) -> Dict[str, Any]:
    return {k: v for k, v in error.items() if k not in _ERROR_ENVELOPE_KEYS}


def exception_from_error(error: Dict[str, Any]) -> BaseException:
    """Build the exception object for one ``error`` object of the JSON envelope.

    Args:
        error (Dict[str, Any]): The inner object of ``{"error": {...}}``
            (`JSON_CLI_CONTRACT.md` §5.1): ``code``, ``message`` and the
            per-code structured fields of §5.4.

    Returns:
        BaseException: The exception instance to raise. ``str(e)`` is the
            envelope's ``message`` verbatim — `ERROR_CODES.md` §4 makes that a
            contract, because kathara-lab-checker embeds it in its reports.
    """
    if not isinstance(error, dict):
        # §5.1 pins `error` as an object. A build that sends anything else is
        # not speaking the contract, and the same rule §9.2 gives for a code
        # this client cannot read applies to a shape it cannot read: the base
        # `KatharaError`, never an `AttributeError` from inside the decoder.
        return KatharaError("Malformed error envelope: `error` must be an object, got %r." % (error,))

    code = error.get("code")
    message = error.get("message", "")
    fields = _fields(error)

    # §9.2: an unknown code buckets to `KatharaError`, and so does a `code` that
    # is not a string at all — `dict.get` would raise `TypeError` on an
    # unhashable one before any of that could happen.
    cls = CODE_TO_EXCEPTION.get(code) if isinstance(code, str) else None
    if cls is None or cls is KatharaError:
        return KatharaError(message, code=code, fields=fields)

    # The three data-bearing classes rebuild their own message from attributes
    # (`__str__` overrides), so they must be constructed through `__init__` and
    # get the `ERROR_CODES.md` §4 attribute surface populated.
    if cls is MachineBinaryError:
        return MachineBinaryError(fields.get("binary", ""), fields.get("machine", ""))
    if cls is InvalidImageArchitectureError:
        return InvalidImageArchitectureError(fields.get("image", ""), fields.get("arch", ""))
    if cls is MachineSignatureNotFoundError:
        return MachineSignatureNotFoundError(fields.get("machine", ""))

    # Every other class either takes the message straight or synthesises it
    # from constructor arguments we do not have. Bypassing `__init__` and
    # setting `args` directly is what keeps `str(e)` byte-identical to the
    # envelope instead of double-wrapping (e.g. `SettingsError` would prepend
    # "Settings file is not valid: " a second time).
    exc = cls.__new__(cls)
    Exception.__init__(exc, message)

    if str(exc) != message:
        # A builtin whose `__str__` reads structured attributes the bypass never
        # set — `SyntaxError` renders `.msg`, not `args[0]`, and would print
        # "None". Its plain constructor takes the message verbatim, so it is
        # both safe and sufficient here.
        try:
            exc = cls(message)
        except Exception:  # pragma: no cover - a class that refuses one arg
            pass

    return exc


def _raise_for_error(document: Dict[str, Any]) -> None:
    """Raise if the decoded document is an error or interrupt envelope."""
    if "error" in document:
        # §6.5: the sibling `errors` array is ignored in 1.0; the client raises
        # the primary error only, which is exactly the Python behaviour.
        raise exception_from_error(document["error"])

    if document.get("interrupted") is True:
        # §6.2: the binary took a SIGINT and exited 0. The parent almost always
        # took the same SIGINT, so surfacing it as KeyboardInterrupt keeps the
        # Python-level semantics ("the user hit Ctrl-C") intact.
        raise KeyboardInterrupt()


def _decode_stderr(raw: Optional[bytes]) -> str:
    if not raw:
        return ""
    return raw.decode("utf-8", errors="replace")


def _log_stderr(stderr: str) -> None:
    for line in stderr.splitlines():
        if line.strip():
            _logger.debug("kathara: %s", line)


def _argv(command: str, args: List[str], fmt: str) -> List[str]:
    # §1.1: `--format` is a per-command flag, so it goes after the command word.
    return [_bin.binary_path(), command, "--format", fmt] + list(args)


def _usage_error(argv: List[str], stderr: str, returncode: int) -> InvocationError:
    detail = stderr.strip() or "no diagnostic on stderr"
    return InvocationError(
        "`%s` rejected the invocation (exit %d): %s" % (" ".join(argv[1:]), returncode, detail)
    )


def _malformed_event_error(argv: List[str], detail: str, stderr: str) -> KatharaError:
    """Build the error for a `jsonl` event whose shape the contract forbids."""
    suffix = " (stderr: %s)" % stderr.strip() if stderr.strip() else ""
    return KatharaError("Malformed event from `%s`: %s%s" % (" ".join(argv[1:]), detail, suffix))


def _protocol_error(argv: List[str], stdout: str, stderr: str, returncode: int) -> KatharaError:
    detail = stderr.strip() or stdout.strip() or "no output"
    return KatharaError(
        "Unexpected output from `%s` (exit %d): %s" % (" ".join(argv[1:]), returncode, detail)
    )


def run_json(command: str, args: List[str], stdin_data: Optional[bytes] = None) -> Dict[str, Any]:
    """Run a command in ``--format json`` mode and return its result envelope.

    Args:
        command (str): The Kathará subcommand (``lstart``, ``lclean``, ...).
        args (List[str]): The remaining argv tokens.
        stdin_data (Optional[bytes]): Bytes to feed to the child's stdin. Used
            by ``lstart --from-archive -`` (`JSON_CLI_CONTRACT.md` §7).

    Returns:
        Dict[str, Any]: The decoded result envelope.

    Raises:
        KeyboardInterrupt: If the binary reported ``{"interrupted": true}``.
        InvocationError: On a usage error (exit 2, no JSON on stdout).
        Exception: The class mapped from the error envelope's ``code``.
    """
    argv = _argv(command, args, "json")

    completed = subprocess.run(
        argv,
        input=stdin_data if stdin_data is not None else b"",
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=os.environ.copy(),
    )

    stderr = _decode_stderr(completed.stderr)
    _log_stderr(stderr)
    stdout = completed.stdout.decode("utf-8", errors="replace").strip()

    if not stdout:
        if completed.returncode == USAGE_EXIT_CODE:
            raise _usage_error(argv, stderr, completed.returncode)
        raise _protocol_error(argv, stdout, stderr, completed.returncode)

    try:
        document = json.loads(stdout)
    except ValueError:
        raise _protocol_error(argv, stdout, stderr, completed.returncode)

    if not isinstance(document, dict):
        raise _protocol_error(argv, stdout, stderr, completed.returncode)

    # The envelope is authoritative, not the exit status: `exec` exits with the
    # remote command's code (§3.6) while still emitting a success envelope.
    _raise_for_error(document)

    return document


def run_interactive(command: str, args: List[str]) -> int:
    """Run a human-mode command with the parent's stdio attached.

    Used for ``connect``, which `JSON_CLI_CONTRACT.md` §1.1 marks human-only and
    interactive: there is no envelope to decode, the child owns the terminal.

    Args:
        command (str): The Kathará subcommand.
        args (List[str]): The remaining argv tokens.

    Returns:
        int: The child's exit status.
    """
    argv = [_bin.binary_path(), command] + list(args)
    return subprocess.call(argv, env=os.environ.copy())


class ExecStream(object):
    """A ``kathara exec --format jsonl`` stream.

    Reproduces the v3.8.3 ``IExecStream`` surface: iterating yields
    ``(stdout, stderr)`` tuples of ``bytes | None`` exactly like the Docker SDK
    demux did, and :meth:`exit_code` returns the remote command's status.

    Per `JSON_CLI_CONTRACT.md` §4.2 an event exists only for a **non-empty**
    chunk side, so every yielded tuple has exactly one non-None side — which is
    what the Python demux produced too. Chunk boundaries are transport
    artefacts: consumers must concatenate.

    Attributes:
        _stream (Generator): The generator yielding the output of the stream exec.
        _stream_api_object (Any): The subprocess backing the stream.
    """
    __slots__ = ['_stream', '_stream_api_object', '_process', '_exit_code', '_finished']

    def __init__(self, process: subprocess.Popen, stream: Generator) -> None:
        self._process: subprocess.Popen = process
        self._stream_api_object: subprocess.Popen = process
        self._stream: Generator = stream
        self._exit_code: Optional[int] = None
        self._finished: bool = False

    def stream_next(self) -> Tuple[Optional[bytes], Optional[bytes]]:
        """Return the next ``(stdout, stderr)`` chunk pair from the stream.

        Returns:
            Tuple[Optional[bytes], Optional[bytes]]: The next chunk pair.

        Raises:
            StopIteration: When the stream is exhausted.
        """
        return next(self._stream)

    def __next__(self) -> Tuple[Optional[bytes], Optional[bytes]]:
        return self.stream_next()

    def __iter__(self) -> 'ExecStream':
        return self

    def exit_code(self) -> int:
        """Return the exit code of the execution.

        If the stream has not been consumed yet it is drained first: the remote
        exit code arrives *in* the stream, as the terminal ``exit`` event
        (`JSON_CLI_CONTRACT.md` §4.1), and there is no second channel to ask.

        **The drained chunks are discarded** (DIVERGENCES.md 126): nothing
        buffers them, so calling this before iterating leaves an exhausted
        stream and no output. v3.8.3 consumed nothing here —
        ``int(exec_inspect(...)['ExitCode'])`` (`DockerExecStream.py:38`) — and
        raised ``TypeError`` when the exec was still running, so a caller could
        not reach that state in the first place. Draining after the stream has
        been consumed, which is what ``collect_exec`` and every observed
        consumer do, is identical in both.

        Returns:
            int: The exit code of the execution.
        """
        while not self._finished:
            try:
                next(self._stream)
            except StopIteration:
                break

        if self._exit_code is not None:
            return self._exit_code

        # No `exit` event: the stream ended on `interrupted` (§6.2, the binary
        # exits 0) or abnormally, in which case draining has already raised and
        # this is a call from a caller that swallowed it. Reporting the child's
        # own status beats reporting a success that never happened.
        return self._process.returncode if self._process.returncode is not None else 1

    def _set_exit_code(self, code: int) -> None:
        self._exit_code = code

    def _set_finished(self) -> None:
        self._finished = True


def _iter_events(stdout: IO[bytes]) -> Generator[Dict[str, Any], None, None]:
    for raw_line in stdout:
        line = raw_line.decode("utf-8", errors="replace").strip()
        if not line:
            continue
        try:
            event = json.loads(line)
        except ValueError:
            # Not protocol: contract §1.3 forbids anything else on stdout, but
            # a broken build must not take the consumer down mid-stream.
            _logger.debug("kathara: unparseable jsonl line %r", line)
            continue
        if isinstance(event, dict):
            yield event


def stream_jsonl(command: str, args: List[str]) -> ExecStream:
    """Run a command in ``--format jsonl`` mode and return its event stream.

    Args:
        command (str): The Kathará subcommand (``exec`` is the only streaming
            consumer in 1.0, spec §5.2).
        args (List[str]): The remaining argv tokens.

    Returns:
        ExecStream: The stream object, an ``IExecStream`` work-alike.
    """
    argv = _argv(command, args, "jsonl")

    # stderr goes to a temporary file rather than a second pipe. The client
    # reads stdout event by event and only drains stderr at the end, so a pipe
    # would deadlock the moment the binary logged more than one pipe buffer
    # (contract §1.3 puts *every* log line on stderr, and a deploy is chatty).
    stderr_file = tempfile.TemporaryFile()

    process = subprocess.Popen(
        argv,
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=stderr_file,
        env=os.environ.copy(),
    )

    holder: Dict[str, Any] = {}

    def generator() -> Generator[Tuple[Optional[bytes], Optional[bytes]], None, None]:
        stream = holder['stream']
        try:
            for event in _iter_events(process.stdout):
                event_type = event.get("type")

                if event_type == "stdout":
                    data = event.get("data") or ""
                    if data:
                        yield data.encode("utf-8"), None
                elif event_type == "stderr":
                    data = event.get("data") or ""
                    if data:
                        yield None, data.encode("utf-8")
                elif event_type == "exit":
                    code = event.get("code")
                    if not isinstance(code, int) or isinstance(code, bool):
                        # §4.1 types the terminal `exit` event's `code` as an
                        # int, so `null` (or a string, or `true`) cannot be
                        # spoken by a conforming binary — but `int(None)` would
                        # answer that with a `TypeError` from the middle of the
                        # stream. A payload the contract forbids is a protocol
                        # error, reported like every other one.
                        stream._set_finished()
                        stderr = _finish(process, stderr_file)
                        raise _malformed_event_error(
                            argv, "the `exit` event's `code` must be an integer, got %r." % (code,), stderr
                        )
                    stream._set_exit_code(code)
                    stream._set_finished()
                    return
                elif event_type == "error":
                    stream._set_finished()
                    _finish(process, stderr_file)
                    raise exception_from_error(event.get("error", {}))
                elif event_type == "interrupted":
                    stream._set_finished()
                    _finish(process, stderr_file)
                    raise KeyboardInterrupt()
                else:
                    # §9.2: clients MUST skip unknown event types.
                    _logger.debug("kathara: skipping unknown event type %r", event_type)

            # §4.1: "Exactly one terminal event ends every jsonl stream." Falling
            # off the end of stdout without one means the invocation was rejected
            # (exit 2, usage text on stderr, §5.5) or the binary died — never a
            # successful run with no output.
            stream._set_finished()
            stderr = _finish(process, stderr_file)
            returncode = process.returncode
            if returncode == USAGE_EXIT_CODE:
                raise _usage_error(argv, stderr, returncode)
            raise _protocol_error(argv, "", stderr, returncode if returncode is not None else 1)
        finally:
            stream._set_finished()
            _finish(process, stderr_file)

    stream_obj = ExecStream(process, None)
    holder['stream'] = stream_obj
    stream_obj._stream = generator()
    return stream_obj


def _finish(process: subprocess.Popen, stderr_file: Optional[IO[bytes]] = None) -> str:
    """Close the child's pipes, reap it and log whatever it said on stderr.

    Returns:
        str: The child's stderr, so a caller diagnosing an abnormal exit can put
            the binary's own diagnostic in the exception. Empty on the second
            call, the file having been consumed and closed by the first.
    """
    if process.stdout is not None and not process.stdout.closed:
        try:
            process.stdout.close()
        except OSError:  # pragma: no cover - closing a dead pipe
            pass

    if process.poll() is None:
        process.wait()

    stderr = ""
    if stderr_file is not None and not stderr_file.closed:
        try:
            stderr_file.seek(0)
            stderr = _decode_stderr(stderr_file.read())
            _log_stderr(stderr)
        except (OSError, ValueError):  # pragma: no cover
            pass
        try:
            stderr_file.close()
        except OSError:  # pragma: no cover
            pass

    return stderr


def collect_exec(stream: ExecStream) -> Tuple[Optional[bytes], Optional[bytes], int]:
    """Drain an exec stream into the ``stream=False`` return shape.

    Args:
        stream (ExecStream): The stream to drain.

    Returns:
        Tuple[Optional[bytes], Optional[bytes], int]: The complete stdout, the
            complete stderr and the remote exit code — the v3.8.3
            ``exec(..., stream=False)`` shape. A stream side that produced no
            output is ``None``, not ``b""``: v3.8.3 returns the raw docker demux
            tuple (``DockerMachine.py:818``), whose empty sides are ``None``.
    """
    stdout_chunks: List[bytes] = []
    stderr_chunks: List[bytes] = []

    for (out, err) in stream:
        if out:
            stdout_chunks.append(out)
        if err:
            stderr_chunks.append(err)

    return (
        b"".join(stdout_chunks) if stdout_chunks else None,
        b"".join(stderr_chunks) if stderr_chunks else None,
        stream.exit_code(),
    )
