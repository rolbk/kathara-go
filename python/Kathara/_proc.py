"""Subprocess plumbing for Kathará's JSON command interface."""

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

USAGE_EXIT_CODE: int = 2


CODE_TO_EXCEPTION: Dict[str, type] = {
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
    'Syntax': SyntaxError,
    'Value': ValueError,
    # `IOError` is an alias of `OSError` in Python 3, so one code, one class.
    'OS': OSError,
    'FileNotFound': FileNotFoundError,
    # Semantically inverted in Python (raised when the path does NOT exist);

    'FileExists': FileExistsError,
    'NotADirectory': NotADirectoryError,
    'Permission': PermissionError,
    'Connection': ConnectionError,
    'DockerAPI': KatharaError,
    'KubernetesAPI': KatharaError,

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
    """Build the exception object for one ``error`` object of the JSON envelope."""
    if not isinstance(error, dict):
        return KatharaError("Malformed error envelope: `error` must be an object, got %r." % (error,))

    code = error.get("code")
    message = error.get("message", "")
    fields = _fields(error)

    cls = CODE_TO_EXCEPTION.get(code) if isinstance(code, str) else None
    if cls is None or cls is KatharaError:
        return KatharaError(message, code=code, fields=fields)

    # These classes render structured attributes, so populate them through
    # their normal constructors.
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
        raise exception_from_error(document["error"])

    if document.get("interrupted") is True:
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
    return [_bin.binary_path(), command, "--format", fmt] + list(args)


def _usage_error(argv: List[str], stderr: str, returncode: int) -> InvocationError:
    detail = stderr.strip() or "no diagnostic on stderr"
    return InvocationError(
        "`%s` rejected the invocation (exit %d): %s" % (" ".join(argv[1:]), returncode, detail)
    )


def _malformed_event_error(argv: List[str], detail: str, stderr: str) -> KatharaError:
    """Build the error for an invalid `jsonl` event."""
    suffix = " (stderr: %s)" % stderr.strip() if stderr.strip() else ""
    return KatharaError("Malformed event from `%s`: %s%s" % (" ".join(argv[1:]), detail, suffix))


def _protocol_error(argv: List[str], stdout: str, stderr: str, returncode: int) -> KatharaError:
    detail = stderr.strip() or stdout.strip() or "no output"
    return KatharaError(
        "Unexpected output from `%s` (exit %d): %s" % (" ".join(argv[1:]), returncode, detail)
    )


def run_json(command: str, args: List[str], stdin_data: Optional[bytes] = None) -> Dict[str, Any]:
    """Run a command in ``--format json`` mode and return its result envelope."""
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

    _raise_for_error(document)

    return document


def run_interactive(command: str, args: List[str]) -> int:
    """Run a human-mode command with the parent's stdio attached."""
    argv = [_bin.binary_path(), command] + list(args)
    return subprocess.call(argv, env=os.environ.copy())


class ExecStream(object):
    """A ``kathara exec --format jsonl`` stream."""
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
        """Return the exit code of the execution."""
        while not self._finished:
            try:
                next(self._stream)
            except StopIteration:
                break

        if self._exit_code is not None:
            return self._exit_code

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
            _logger.debug("kathara: unparseable jsonl line %r", line)
            continue
        if isinstance(event, dict):
            yield event


def stream_jsonl(command: str, args: List[str]) -> ExecStream:
    """Run a command in ``--format jsonl`` mode and return its event stream.

    Args:
        command (str): The Kathará subcommand. ``exec`` is currently the only
            streaming consumer.
        args (List[str]): The remaining argv tokens.

    Returns:
        ExecStream: The stream object, an ``IExecStream`` work-alike.
    """
    argv = _argv(command, args, "jsonl")

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
                    _logger.debug("kathara: skipping unknown event type %r", event_type)

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
