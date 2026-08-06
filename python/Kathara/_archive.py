"""Serialise an in-memory :class:`~Kathara.model.Lab.Lab` to the deploy archive.

`PORT_SPEC.md` §5.4 / `JSON_CLI_CONTRACT.md` §7: ``deploy_lab`` ships the
scenario to the Go binary as **a tar of a normal Kathará scenario directory**,
rooted at the archive root, and the binary extracts it and proceeds exactly as
if ``-d <tmpdir>`` had been given. So the wire format needs no schema of its
own — it needs a faithful ``lab.conf``.

What goes in the archive:

* a ``lab.conf`` **regenerated from the model**, because the model — not the
  file the lab may once have been parsed from — is what the caller asked to
  deploy. Device order in the file is ``lab.machines`` insertion order, which
  is the deploy schedule order (`JSON_CLI_CONTRACT.md` §3.1) and already
  reflects any :meth:`Lab.apply_dependencies` reordering;
* every file and directory of ``lab.fs``: ``*.startup``, ``*.shutdown``,
  ``shared.startup``, per-device directory trees, ``kathara.conf``. This works
  identically for a memory FS (the tutorials' ``Lab("name")``) and an OS FS (a
  parsed scenario directory).
* a ``lab.dep`` **regenerated from the model** when — and only when — the
  scenario has dependencies. Ordering is already baked into the device order,
  but ``lab.dep`` carries a second, invisible semantic: v3.8.3 deploys devices
  **sequentially** when the scenario has one and in parallel chunks when it does
  not (``DockerMachine.py:174-183``). A chain (each device depending on its
  predecessor in the baked order) restores that without changing the order —
  ``depgen.flatten`` of a chain is the chain, so the far side's
  ``apply_dependencies`` is the identity.

What is deliberately left out:

* the original ``lab.conf`` and ``lab.dep`` in ``lab.fs``, superseded by the
  generated ones: the model — not the files the scenario may once have been
  parsed from — is what the caller asked to deploy.

``lab.ext`` is passed through unchanged, so a scenario that uses external links
gets the honest ``FeatureNotAvailable`` error from the binary (`ERROR_CODES.md`
§5) instead of being silently deployed without them.
"""

import io
import posixpath
import tarfile
from typing import Any, Dict, List, Optional, Tuple

from .exceptions import InvocationError, NotSupportedError
from .model import Lab as LabPackage
from .model import Machine as MachinePackage
from .utils import EXCLUDED_FILES

__all__ = ['generate_lab_conf', 'generate_lab_dep', 'pack_lab']

#: Files of ``lab.fs`` that the generated ``lab.conf`` supersedes.
GENERATED_FILES: Tuple[str, ...] = ("lab.conf", "lab.dep")

#: A device with no interfaces and no metas produces no ``lab.conf`` line, and
#: would therefore vanish from the archive. This meta reintroduces it: `bridged`
#: goes through ``strtobool``, and ``is_bridged()`` returns ``False`` both when
#: the meta is absent and when it is ``False``, so the line is a true no-op.
DEVICE_INTRODUCER_META: str = "bridged"
DEVICE_INTRODUCER_VALUE: str = "false"

#: `PORT_SPEC.md` §7.3 wheels are reproducible; so is this archive. A fixed
#: mtime keeps two packs of the same scenario byte-identical.
_FIXED_MTIME: int = 0


def _check_value(kind: str, name: str, value: str) -> str:
    """Reject values ``lab.conf`` cannot express.

    The device-line value class is ``[^"']+`` inside an optional matching quote
    pair (`LabParser.py:45`): quotes cannot appear in a value at all, the value
    cannot be empty, and a newline would split the line.
    """
    if not isinstance(value, str):
        value = str(value)

    if value == "":
        raise InvocationError(
            f"Cannot serialize {kind} `{name}`: lab.conf cannot express an empty value."
        )
    if '"' in value or "'" in value:
        raise InvocationError(
            f"Cannot serialize {kind} `{name}`: lab.conf cannot express a value containing a quote (`{value}`)."
        )
    if "\n" in value or "\r" in value:
        raise InvocationError(
            f"Cannot serialize {kind} `{name}`: lab.conf cannot express a value containing a newline."
        )

    return value


def _meta_lines(machine: 'MachinePackage.Machine') -> List[str]:
    """Render one device's metas back into ``lab.conf`` lines.

    The six structured metas are un-parsed back into the exact string forms
    ``Machine.add_meta`` accepts, so a round trip through the archive rebuilds
    the same ``meta`` dict on the far side.
    """
    name = machine.name
    lines: List[str] = []

    def emit(arg: str, value: Any) -> None:
        lines.append('%s[%s]="%s"' % (name, arg, _check_value("meta", "%s[%s]" % (name, arg), value)))

    meta: Dict[str, Any] = machine.meta

    for command in meta.get('exec_commands', []):
        emit("exec", command)

    for key, value in meta.get('sysctls', {}).items():
        emit("sysctl", "%s=%s" % (key, value))

    for key, value in meta.get('envs', {}).items():
        emit("env", "%s=%s" % (key, value))

    for (host_port, protocol), guest_port in meta.get('ports', {}).items():
        emit("port", "%s:%s/%s" % (host_port, guest_port, protocol))

    for key, value in meta.get('ulimits', {}).items():
        emit("ulimit", "%s=%s:%s" % (key, value['soft'], value['hard']))

    for host_path, value in meta.get('volumes', {}).items():
        emit("volume", "%s|%s|%s" % (host_path, value['guest_path'], value['mode']))

    for key, value in meta.items():
        if key in ('exec_commands', 'sysctls', 'envs', 'ports', 'ulimits', 'volumes'):
            continue
        if isinstance(value, bool):
            # `privileged` and `bridged` are the only real booleans a lab.conf
            # can produce; `strtobool` reads these spellings back.
            value = "true" if value else "false"
        emit(key, value)

    return lines


def _interface_lines(machine: 'MachinePackage.Machine') -> List[str]:
    lines: List[str] = []

    for number, interface in machine.interfaces.items():
        if interface is None:
            # A tombstoned slot (`Machine.remove_interface` keeps the key and
            # nulls the value). The model tolerates the resulting hole; a
            # lab.conf cannot express one, and the binary's `check_integrity`
            # would reject the file with NonSequentialMachineInterfaceError.
            raise NotSupportedError(
                f"Device `{machine.name}` has a disconnected interface {number}: a network scenario with a hole in "
                f"its interface numbering cannot be serialized to lab.conf. Rebuild the scenario without the "
                f"disconnected interface, or configure the running device with `connect_machine_to_link`."
            )

        value = interface.link.name
        if interface.mac_address:
            value = "%s/%s" % (value, interface.mac_address)

        lines.append('%s[%d]="%s"' % (
            machine.name, number, _check_value("interface", "%s[%d]" % (machine.name, number), value)
        ))

    return lines


def _is_expressible_metadata(value: str) -> bool:
    """Whether a ``LAB_*`` value survives the parser's metadata branch.

    A value containing ``=`` crashes it: the branch does
    ``(key, value) = line.split("=")`` and the Go port reproduces the crash
    (``DIVERGENCES.md`` item 5). Quotes are stripped and a newline splits the
    line, so neither round-trips either.
    """
    return "=" not in value and not any(character in value for character in ('\n', '\r', '"', "'"))


def _metadata_lines(lab: 'LabPackage.Lab') -> List[str]:
    """Render the ``LAB_*`` header lines.

    ``LAB_NAME`` is emitted only when the lab has one, and **skipped** rather
    than refused when its value is inexpressible: ``lstart --name`` wins over it
    in any case (`JSON_CLI_CONTRACT.md` §7.2, A8), so the line carries the
    extracted directory's own fidelity, never the identity. The other five keys
    have no such fallback and are refused.
    """
    lines: List[str] = []

    for key, value in (
            ("LAB_NAME", lab.name),
            ("LAB_DESCRIPTION", lab.description),
            ("LAB_VERSION", lab.version),
            ("LAB_AUTHOR", lab.author),
            ("LAB_EMAIL", lab.email),
            ("LAB_WEB", lab.web),
    ):
        if value is None:
            continue

        value = str(value)
        if not _is_expressible_metadata(value):
            if key == "LAB_NAME":
                continue
            if "=" in value:
                raise InvocationError(
                    f"Cannot serialize {key}: a lab.conf metadata value cannot contain `=` (`{value}`)."
                )
            raise InvocationError(
                f"Cannot serialize {key}: a lab.conf metadata value cannot contain a newline or a quote (`{value}`)."
            )

        lines.append("%s=%s" % (key, value))

    return lines


def generate_lab_conf(lab: 'LabPackage.Lab') -> str:
    """Render the network scenario model back into a ``lab.conf``.

    Args:
        lab (Kathara.model.Lab.Lab): The network scenario to render.

    Returns:
        str: The content of the ``lab.conf`` file.

    Raises:
        InvocationError: If a value cannot be expressed in the lab.conf grammar.
        NotSupportedError: If a device has a disconnected (tombstoned) interface.
    """
    lines: List[str] = _metadata_lines(lab)

    for machine in lab.machines.values():
        machine_lines = _interface_lines(machine) + _meta_lines(machine)

        if not machine_lines:
            machine_lines = ['%s[%s]="%s"' % (machine.name, DEVICE_INTRODUCER_META, DEVICE_INTRODUCER_VALUE)]

        lines.extend(machine_lines)

    return "\n".join(lines) + "\n" if lines else ""


def generate_lab_dep(lab: 'LabPackage.Lab') -> Optional[str]:
    """Render the boot dependencies of the network scenario as a ``lab.dep``.

    v3.8.3 branches on ``lab.has_dependencies`` at deploy time and starts the
    devices **one at a time** when it is set (`DockerMachine.py:174-183`). The
    only way to ask the binary for that is to ship a ``lab.dep``, so one is
    synthesised as a chain over the (already dependency-ordered) device list.

    Args:
        lab (Kathara.model.Lab.Lab): The network scenario to render.

    Returns:
        Optional[str]: The content of the ``lab.dep`` file, None when the
            scenario has no dependencies or has fewer than two devices (with one
            device there is nothing to sequence).
    """
    if not lab.has_dependencies:
        return None

    names = list(lab.machines.keys())
    if len(names) < 2:
        return None

    return "".join("%s: %s\n" % (names[index], names[index - 1]) for index in range(1, len(names)))


def _add_bytes(tar: tarfile.TarFile, name: str, payload: bytes, mode: int = 0o644) -> None:
    info = tarfile.TarInfo(name)
    info.size = len(payload)
    info.mode = mode
    info.mtime = _FIXED_MTIME
    info.type = tarfile.REGTYPE
    tar.addfile(info, io.BytesIO(payload))


def _add_dir(tar: tarfile.TarFile, name: str, mode: int = 0o755) -> None:
    info = tarfile.TarInfo(name.rstrip("/"))
    info.type = tarfile.DIRTYPE
    info.mode = mode
    info.mtime = _FIXED_MTIME
    tar.addfile(info)


def _file_mode(lab: 'LabPackage.Lab', path: str, default: int) -> int:
    """Preserve the executable bit when the filesystem exposes permissions.

    `JSON_CLI_CONTRACT.md` §7.4: "File modes are preserved; owners are ignored."
    A memory FS has no ``access`` namespace, hence the fallback.
    """
    try:
        info = lab.fs.getinfo(path, namespaces=['access'])
        permissions = info.permissions
        if permissions is not None:
            return int(permissions.mode) & 0o7777
    except Exception:
        pass

    return default


def pack_lab(lab: 'LabPackage.Lab', compress: bool = True) -> bytes:
    """Pack a network scenario into the ``--from-archive`` tar.

    Args:
        lab (Kathara.model.Lab.Lab): The network scenario to pack.
        compress (bool): If True, gzip the archive. Both encodings are accepted
            by the binary, which detects them by magic bytes (§7.4).

    Returns:
        bytes: The tar (or tar.gz) content, ready for the binary's stdin.

    Raises:
        InvocationError: If the scenario cannot be expressed as a lab.conf.
        NotSupportedError: If a device has a disconnected (tombstoned) interface.
    """
    lab_conf = generate_lab_conf(lab).encode("utf-8")
    lab_dep = generate_lab_dep(lab)

    buffer = io.BytesIO()
    mode = "w:gz" if compress else "w"

    with tarfile.open(fileobj=buffer, mode=mode, format=tarfile.PAX_FORMAT) as tar:
        _add_bytes(tar, "lab.conf", lab_conf)
        if lab_dep is not None:
            _add_bytes(tar, "lab.dep", lab_dep.encode("utf-8"))

        for path, is_dir in _walk(lab):
            arcname = path.lstrip("/")
            if not arcname or arcname in GENERATED_FILES:
                continue
            if posixpath.basename(arcname) in EXCLUDED_FILES:
                continue

            if is_dir:
                _add_dir(tar, arcname, _file_mode(lab, path, 0o755))
            else:
                with lab.fs.open(path, "rb") as source:
                    payload = source.read()
                _add_bytes(tar, arcname, payload, _file_mode(lab, path, 0o644))

    return buffer.getvalue()


def _walk(lab: 'LabPackage.Lab') -> List[Tuple[str, bool]]:
    """Return every path of ``lab.fs`` as ``(path, is_dir)``, parents first."""
    entries: List[Tuple[str, bool]] = []

    for step in lab.fs.walk(''):
        for directory in step.dirs:
            entries.append((directory.make_path(step.path), True))
        for file_info in step.files:
            entries.append((file_info.make_path(step.path), False))

    entries.sort(key=lambda item: item[0])
    return entries


def archive_name(lab: 'LabPackage.Lab') -> Optional[str]:
    """Return the ``--name`` value that makes the binary recompute ``lab.hash``.

    `JSON_CLI_CONTRACT.md` §7.2 pins ``hash = generate_urlsafe_hash(NAME)``, and
    `model/Lab.py` hashes either the name (when there is one) or the constructor
    path. The model records whichever string it hashed, so passing it back as
    ``--name`` reproduces the identical hash — which is what keeps a later
    ``exec(lab_hash=lab.hash)`` addressing the scenario that was just deployed.

    Args:
        lab (Kathara.model.Lab.Lab): The network scenario to deploy.

    Returns:
        Optional[str]: The ``--name`` value, None if the lab has no hash seed.
    """
    return lab.hash_seed
