from __future__ import annotations

import contextlib
import base64
import io
import json
import os
import shlex
import tempfile
from typing import Set, Dict, Generator, Any, Iterator, Tuple, List, Optional, Union

from .. import _archive
from .. import _proc
from .. import utils
from .._proc import ExecStream
from ..exceptions import (
    InstantiationError,
    InvocationError,
    LabNotFoundError,
    MachineCollisionDomainError,
    MachineNotFoundError,
    MachineNotRunningError,
    NotSupportedError,
)
from ..model.Lab import Lab
from ..model.Link import Link
from ..model.Machine import Machine
from ..setting.Setting import AVAILABLE_MANAGERS, FORMATTED_MANAGER_NAMES
from ..utils import check_required_single_not_none_var, check_single_not_none_var

#: The fixed scenario name of the `vstart`/`vclean`/`vconfig` device family.
VLAB_NAME: str = "kathara_vlab"

#: `lstart` flags for the two `Lab.general_options` keys the deploy path reads
#: (`DockerMachine.py:161,302-310`), as ``key -> (flag when true, when false)``.
LAB_OPTION_FLAGS: Dict[str, Tuple[str, str]] = {
    "hosthome_mount": ("--hosthome", "--no-hosthome"),
    "shared_mount": ("--shared", "--no-shared"),
}

_CHECK_REPORT: Dict[str, Any] = {}


def _not_supported(operation: str, reason: str) -> NotSupportedError:
    """Build the error for an operation the command interface cannot express."""
    return NotSupportedError(f"`{operation}` is not available in this release. {reason}")


class Kathara(object):
    """Facade class for interacting with Kathara."""
    __slots__ = []

    __instance: Kathara = None

    @staticmethod
    def get_instance() -> Kathara:
        """Get an instance of Kathara.

        Returns:
            Kathara: instance of Kathara.

        Raises:
            InstantiationError: If two instances of the class are created.
        """
        if Kathara.__instance is None:
            Kathara()

        return Kathara.__instance

    def __init__(self) -> None:
        if Kathara.__instance is not None:
            raise InstantiationError("This class is a singleton!")
        else:
            Kathara.__instance = self

    # ------------------------------------------------------------------ #
    # Internal helpers
    # ------------------------------------------------------------------ #

    @staticmethod
    def _resolve_hash(lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                      lab: Optional[Lab] = None) -> str:
        """Resolve the scenario identity the way every v3.8.3 manager method does."""
        if lab:
            return lab.hash
        if lab_name:
            return utils.generate_urlsafe_hash(lab_name)
        return lab_hash

    @staticmethod
    def _selection_args(selected_machines: Optional[Set[str]] = None,
                        excluded_machines: Optional[Set[str]] = None) -> List[str]:
        """Render device selection as CLI tokens.

        Both `lstart` and `lclean` take positional device names plus
        ``--exclude``. Names are sorted so an invocation is reproducible from
        a `set`, which is what the API takes.
        """
        if selected_machines and excluded_machines:
            # The message users observe on this path in v3.8.3: `DockerManager`
            # guards `deploy_lab`/`undeploy_lab` before `DockerMachine` ever runs
            # (`DockerManager.py:147,342`), so its wording is the reachable one.

            raise InvocationError("You can either select or exclude devices.")

        args: List[str] = []
        if excluded_machines:
            args += ["--exclude"] + sorted(excluded_machines)
        if selected_machines:
            args += sorted(selected_machines)

        return args

    @staticmethod
    def _lab_option_args(lab: Lab) -> List[str]:
        """Render ``lab.general_options`` as `lstart` tokens.

        v3.8.3 reads two keys out of this dict while deploying — ``shared_mount``
        (`DockerMachine.py:161`) and ``hosthome_mount`` (`:302-310`) — and both
        have a flag pair on `lstart`. Anything else the binary has no way to
        receive, so it is refused rather than dropped.
        """
        args: List[str] = []
        unsupported: List[str] = []

        for key, value in lab.general_options.items():
            flags = LAB_OPTION_FLAGS.get(key)
            if flags is None:
                unsupported.append(key)
                continue
            # Truthiness, like the manager's own `if shared_mount:` reads.
            args.append(flags[0] if value else flags[1])

        if unsupported:
            raise _not_supported(
                "deploy_lab with general option(s) `%s`" % "`, `".join(sorted(unsupported)),
                "`lstart` can carry `hosthome_mount` and `shared_mount` only; the rest of `general_options` is "
                "internal to the v3.8.3 managers and has no place on the command line."
            )

        return args

    @staticmethod
    def _global_metadata_args(lab: Lab) -> List[str]:
        """Render ``lab.global_machine_metadata`` as `lstart` tokens.

        The CLI fills that same dict from ``-o/--pass`` through `OptionParser`
        (`LstartCommand.py:187`) and from ``--privileged``
        (`LstartCommand.py:228`), so this is the inverse of both. One ``-o`` per
        pair: the flag repeats, it does not take a list, and a shared
        ``-o k=v k2=v2`` run would be indistinguishable from device names.
        """
        args: List[str] = []

        for key, value in lab.global_machine_metadata.items():
            if key == "privileged":
                if value:
                    args.append("--privileged")
                    continue
                raise _not_supported(
                    "deploy_lab with `privileged` global machine metadata set to %r" % (value,),
                    "`lstart --privileged` is the only spelling of this metadata and it can only turn privileged "
                    "mode on."
                )

            if isinstance(value, bool):
                raise _not_supported(
                    "deploy_lab with boolean global machine metadata `%s`" % key,
                    "`lstart -o %s=%s` would reach the devices as the *string* `%s`, which the meta accessors read "
                    "as true whatever it says. Set the meta on the devices instead." % (key, value, value)
                )

            text = str(value)
            if "=" in key or "=" in text or '"' in text or "'" in text or '"' in key or "'" in key:
                # `OptionParser.parse` strips every quote and then splits on a
                # single `=`; neither survives the round trip.
                raise InvocationError(
                    "Cannot pass global machine metadata `%s=%s`: `-o` values cannot contain `=` or a quote." %
                    (key, text)
                )

            args += ["-o", "%s=%s" % (key, text)]

        return args

    @staticmethod
    def _machine_lab(machine: Machine) -> Lab:
        if not machine.lab:
            raise LabNotFoundError(f"Device `{machine.name}` is not associated to a network scenario.")
        return machine.lab

    @staticmethod
    def _link_lab(link: Link) -> Lab:
        if not link.lab:
            raise LabNotFoundError(f"Collision domain `{link.name}` is not associated to a network scenario.")
        return link.lab

    @staticmethod
    def _list_machines(lab_hash: Optional[str] = None, machine_name: Optional[str] = None,
                       all_users: bool = False) -> List[Dict[str, Any]]:
        """Return the machine inventory, optionally filtered."""
        args: List[str] = []
        if machine_name:
            args += ["-n", machine_name]
        if all_users:
            args += ["-a"]

        result = _proc.run_json("list", args)
        machines = result.get("machines", [])

        if lab_hash is not None:
            machines = [m for m in machines if m.get("network_scenario_id") == lab_hash]

        return machines

    # ------------------------------------------------------------------ #
    # Deploy
    # ------------------------------------------------------------------ #

    def deploy_machine(self, machine: Machine) -> None:
        """Deploy a Kathara device.

        Args:
            machine (Kathara.model.Machine): A Kathara machine object.

        Returns:
            None

        Raises:
            LabNotFoundError: If the specified device is not associated to any network scenario.
        """
        lab = self._machine_lab(machine)

        # A single-device deploy is `lstart <device>` over the whole scenario
        # archive: the binary needs the full lab.conf anyway to know which
        # collision domains the device attaches to.
        self.deploy_lab(lab, selected_machines={machine.name})

    def deploy_link(self, link: Link) -> None:
        """Deploy a Kathara collision domain.

        Args:
            link (Kathara.model.Link): A Kathara collision domain object.

        Returns:
            None

        Raises:
            LabNotFoundError: If the collision domain has no scenario.
        """
        lab = self._link_lab(link)
        payload = {
            "lab_hash": lab.hash,
            "link_name": link.name,
            "external": [{"interface": item.interface, "vlan": item.vlan or 0} for item in link.external],
        }
        _proc.run_json("api", ["deploy-link"], stdin_data=json.dumps(payload).encode("utf-8"))

    def deploy_lab(self, lab: Lab, selected_machines: Optional[Set[str]] = None,
                   excluded_machines: Optional[Set[str]] = None) -> None:
        """Deploy a Kathara network scenario."""
        # Rendered first: it carries the both-given guard
        # (`DockerManager.py:146-147`). v3.8.3 runs `lab.check_integrity()`
        # ahead of it (`:144`), which the client cannot: integrity is validated
        # by the binary, on the lab.conf this call is about to ship. So a
        # scenario that is *both* mis-numbered and asked to select and exclude
        # devices answers `InvocationError` here where v3.8.3 answered
        # `NonSequentialMachineInterfaceError` — same two refusals, opposite
        # order, and every single-fault case is unchanged.
        selection_args = self._selection_args(selected_machines, excluded_machines)

        if not lab.machines:
            # The binary validates a selection against the lab.conf it is sent,
            # but a device-less scenario never reaches it, so the check
            # `DockerManager.py:149-155` does happens here instead.
            missing = set(selected_machines or excluded_machines or ())
            if missing:
                raise MachineNotFoundError(f"The following devices are not in the network scenario: {missing}.")

            if lab.links:

                raise _not_supported(
                    "deploy_lab of a network scenario with collision domains but no devices",
                    "`lstart` deploys the collision domains its devices declare; there is no device-less deploy."
                )

            # v3.8.3 parity: a device-less scenario deploys nothing and succeeds.
            # An empty lab.conf, by contrast, is an error on the far side
            # (`LabParser.py:29`, `lab.conf file is empty.`).
            return

        name = _archive.archive_name(lab)
        if not name:
            raise InvocationError("The network scenario has no name and no path, so it has no identity to deploy under.")

        # `--name=` rather than `--name `: a scenario legitimately named `-x`
        # would otherwise reach the binary's flag parser as a flag.
        args = ["--from-archive", "-", "--name=%s" % name]
        args += self._lab_option_args(lab)
        args += self._global_metadata_args(lab)
        args += selection_args

        _proc.run_json("lstart", args, stdin_data=_archive.pack_lab(lab))

    def connect_machine_to_link(self, machine: Machine, link: Link, mac_address: Optional[str] = None) -> None:
        """Connect a Kathara device to a collision domain.

        The device model is updated too, exactly as v3.8.3 does
        (`DockerManager.py:221`): the caller's ``machine.interfaces`` and
        ``link.machines`` must show the new wiring, or a later ``deploy_lab`` (or
        any topology read) would work from the pre-connect model. The one
        ordering difference is deliberate: v3.8.3 adds the interface *before* the
        backend call and keeps it when that call fails, which across a process
        boundary would leave the model claiming an interface the binary never
        created, so the model is updated after the command succeeds.

        Args:
            machine (Kathara.model.Machine): A Kathara machine object.
            link (Kathara.model.Link): A Kathara collision domain object.
            mac_address (Optional[str]): The MAC address to assign to the interface.

        Returns:
            None

        Raises:
            LabNotFoundError: If the device specified is not associated to any network scenario.
            LabNotFoundError: If the collision domain is not associated to any network scenario.
            MachineCollisionDomainError: If the device is already connected to the collision domain.
        """
        lab = self._machine_lab(machine)
        self._link_lab(link)

        if machine.name in link.machines:
            raise MachineCollisionDomainError(
                f"Device `{machine.name}` is already connected to collision domain `{link.name}`."
            )

        value = link.name if not mac_address else "%s/%s" % (link.name, mac_address)

        _proc.run_json("lconfig", ["--lab-hash", lab.hash, "-n", machine.name, "--add", value])

        machine.add_interface(link, mac_address=mac_address)

    def disconnect_machine_from_link(self, machine: Machine, link: Link, keep_link: bool = False) -> None:
        """Disconnect a Kathara device from a collision domain.

        The device model is updated on success, as in v3.8.3
        (`DockerManager.py:264`): ``Machine.remove_interface`` nulls the slot and
        keeps the key, so the interface numbering of the surviving interfaces
        does not shift.

        Args:
            machine (Kathara.model.Machine): A Kathara machine object.
            link (Kathara.model.Link): The Kathara collision domain from which disconnect the device.
            keep_link (bool): Keep collision domain. Default: False.

        Returns:
            None

        Raises:
            LabNotFoundError: If the device specified is not associated to any network scenario.
            LabNotFoundError: If the collision domain is not associated to any network scenario.
            MachineCollisionDomainError: If the device is not connected to the collision domain.
            NotSupportedError: If keep_link is True (`lconfig --rm` has no such flag).
        """
        lab = self._machine_lab(machine)
        self._link_lab(link)

        if keep_link:
            raise _not_supported(
                "disconnect_machine_from_link(keep_link=True)",
                "`lconfig --rm` always garbage-collects a collision domain that has no devices left."
            )

        if machine.name not in link.machines:
            raise MachineCollisionDomainError(
                f"Device `{machine.name}` is not connected to collision domain `{link.name}`."
            )

        _proc.run_json("lconfig", ["--lab-hash", lab.hash, "-n", machine.name, "--rm", link.name])

        machine.remove_interface(link)

    # ------------------------------------------------------------------ #
    # Undeploy
    # ------------------------------------------------------------------ #

    def undeploy_machine(self, machine: Machine, keep_links: bool = False) -> None:
        """Undeploy a Kathara device.

        Args:
            machine (Kathara.model.Machine): A Kathara machine object.
            keep_links (bool): Keep device's collision domains when undeploying. Default: False.

        Returns:
            None

        Raises:
            LabNotFoundError: If the device specified is not associated to any network scenario.
            NotSupportedError: If keep_links is True (`lclean` has no such flag).
        """
        lab = self._machine_lab(machine)

        if keep_links:
            raise _not_supported(
                "undeploy_machine(keep_links=True)",
                "`lclean` always removes the collision domains left without devices."
            )

        self.undeploy_lab(lab_hash=lab.hash, selected_machines={machine.name})

    def undeploy_link(self, link: Link) -> None:
        """Undeploy a Kathara collision domain.

        Args:
            link (Kathara.model.Link): A Kathara collision domain object.

        Returns:
            None

        Raises:
            LabNotFoundError: If the collision domain has no scenario.
        """
        lab = self._link_lab(link)
        payload = {"lab_hash": lab.hash, "link_name": link.name}
        _proc.run_json("api", ["undeploy-link"], stdin_data=json.dumps(payload).encode("utf-8"))

    def undeploy_lab(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None, lab: Optional[Lab] = None,
                     selected_machines: Optional[Set[str]] = None,
                     excluded_machines: Optional[Set[str]] = None,
                     selected_links: Optional[Set[str]] = None) -> None:
        """Undeploy a Kathara network scenario.

        Args:
            lab_hash (Optional[str]): The hash of the network scenario.
                Can be used as an alternative to lab_name and lab. If None, lab_name or lab should be set.
            lab_name (Optional[str]): The name of the network scenario.
                Can be used as an alternative to lab_hash and lab. If None, lab_hash or lab should be set.
            lab (Optional[Kathara.model.Lab]): The network scenario object.
                Can be used as an alternative to lab_hash and lab_name. If None, lab_hash or lab_name should be set.
            selected_machines (Optional[Set[str]]): If not None, undeploy only the specified devices.
            excluded_machines (Optional[Set[str]]): If not None, exclude devices from being undeployed.
            selected_links (Optional[Set[str]]): If not None, undeploy only the specified collision domains.

        Returns:
            None

        Raises:
            InvocationError: If a running network scenario hash or name is not specified.
            NotSupportedError: If selected_links is specified (`lclean` selects devices only).
        """
        check_required_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        if selected_links:
            raise _not_supported(
                "undeploy_lab(selected_links=...)",
                "`lclean` selects devices, not collision domains."
            )

        args = ["--lab-hash", self._resolve_hash(lab_hash, lab_name, lab)]
        args += self._selection_args(selected_machines, excluded_machines)

        _proc.run_json("lclean", args)

    def wipe(self, all_users: bool = False) -> None:
        """Undeploy all the running network scenarios.

        Args:
            all_users (bool): If false, undeploy only the current user network scenarios. If true, undeploy the
                running network scenarios of all users.

        Returns:
            None

        Raises:
            PrivilegeError: If all_users is True and the user does not have root privileges.
        """
        args = ["-f"]
        if all_users:
            args += ["-a"]

        _proc.run_json("wipe", args)

    # ------------------------------------------------------------------ #
    # Interactive / exec
    # ------------------------------------------------------------------ #

    def connect_tty(self, machine_name: str, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                    lab: Optional[Lab] = None, shell: str = None, logs: bool = False,
                    wait: Union[bool, Tuple[int, float]] = True) -> None:
        """Connect to a device in a running network scenario, using the specified shell."""
        check_required_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        name = lab.name if lab is not None else lab_name

        tail: List[str] = []
        if shell:
            tail += ["--shell", shell]
        if logs:
            tail += ["-l"]
        tail += [machine_name]

        if name == VLAB_NAME:
            _proc.run_interactive("connect", ["-v"] + tail)
            return

        if name:
            with self._named_scenario_dir(name) as directory:
                _proc.run_interactive("connect", ["-d", directory] + tail)
            return

        # No name: the hash `Lab.__init__` computed is the one derived from this
        # very directory, so handing the directory back reproduces it — the one
        # case `-d <lab path>` is right for. (Residual, unreachable through
        # `LabParser`, which would have set the name: a hand-built
        # `Lab(None, path=d)` over a `d/lab.conf` that *does* carry a `LAB_NAME`
        # line lands on that name's hash instead.)
        if lab is not None and lab.has_host_path():
            _proc.run_interactive("connect", ["-d", lab.fs_path()] + tail)
            return

        raise _not_supported(
            "connect_tty by hash",
            "`connect` takes `-d`/`-v` only; `--lab-hash`/`--lab-name` are available to `exec`, `lclean` "
            "and `lconfig`, and a hash cannot be turned back into the name it was derived from. Pass the "
            "network scenario object or its name, or use `exec`."
        )

    @staticmethod
    @contextlib.contextmanager
    def _named_scenario_dir(name: str) -> Iterator[str]:
        """Yield a throwaway scenario directory whose identity is ``name``.

        Args:
            name (str): The name of the network scenario.

        Yields:
            str: The path of the directory, removed when the block ends.

        Raises:
            NotSupportedError: If the name cannot be written as a `LAB_NAME` line.
        """
        if not _archive._is_expressible_metadata(name):
            raise _not_supported(
                "connect_tty to a network scenario named `%s`" % name,
                "`connect` addresses a nameless scenario through a `LAB_NAME=` line, and a lab.conf metadata "
                "value cannot contain `=`, a quote or a newline."
            )

        # Leading or trailing whitespace does not round-trip either, and it is
        # the quieter failure: both parsers `.strip()` the metadata value
        # (`LabParser.py:52`; `labfile/labconf.go`'s `applyLabMetadata`
        # through `pyStrip`), so a scenario deployed as `--name=" foo "` lives
        # under `hash(" foo ")` while the synthesised directory resolves to
        # `hash("foo")` — `connect` would reach a different scenario, or none,
        # with no error of its own. Refusing here keeps the guarantee this
        # helper exists for: the directory's identity IS `name`. Whitespace-only
        # names fall in the same way, `" "` stripping to `""`.
        if name != name.strip():
            raise _not_supported(
                "connect_tty to a network scenario named `%s`" % name,
                "`connect` addresses a nameless scenario through a `LAB_NAME=` line, and the parser strips a "
                "lab.conf metadata value, so a name with leading or trailing whitespace would resolve to a "
                "different network scenario."
            )

        with tempfile.TemporaryDirectory(prefix="kathara-connect-") as directory:
            with open(os.path.join(directory, "lab.conf"), "w") as lab_conf:
                lab_conf.write("LAB_NAME=%s\n" % name)

            yield directory

    def connect_tty_obj(self, machine: Machine, shell: str = None, logs: bool = False,
                        wait: Union[bool, Tuple[int, float]] = True) -> None:
        """Connect to a device in a running network scenario, using the specified shell.

        Args:
            machine (Machine): The device to connect.
            shell (str): The name of the shell to use for connecting.
            logs (bool): If True, print startup logs on stdout.
            wait (Union[bool, Tuple[int, float]]): Ignored, see `connect_tty`.

        Returns:
            None

        Raises:
            LabNotFoundError: If the specified device is not associated to any network scenario.
        """
        lab = self._machine_lab(machine)

        self.connect_tty(machine.name, lab=lab, shell=shell, logs=logs, wait=wait)

    @staticmethod
    def _wait_args(wait: Union[bool, Tuple[int, float]]) -> List[str]:
        if isinstance(wait, bool):
            return ["--wait"] if wait else []

        if isinstance(wait, tuple):
            if len(wait) != 2:

                raise ValueError("Invalid `wait` value.")
            raise _not_supported(
                "exec(wait=(retries, interval))",
                "`exec --wait` waits indefinitely and cannot express a retry budget. "
                "Pass wait=True or poll yourself."
            )

        raise ValueError("Invalid `wait` value.")

    def exec(self, machine_name: str, command: Union[List[str], str], lab_hash: Optional[str] = None,
             lab_name: Optional[str] = None, lab: Optional[Lab] = None, wait: Union[bool, Tuple[int, float]] = False,
             stream: bool = True) -> Union[ExecStream, Tuple[bytes, bytes, int]]:
        """Exec a command on a device in a running network scenario.

        Args:
            machine_name (str): The name of the device to connect.
            command (Union[List[str], str]): The command to exec on the device.
            lab_hash (Optional[str]): The hash of the network scenario.
                Can be used as an alternative to lab_name and lab. If None, lab_name or lab should be set.
            lab_name (Optional[str]): The name of the network scenario.
                Can be used as an alternative to lab_hash and lab. If None, lab_hash or lab should be set.
            lab (Optional[Kathara.model.Lab]): The network scenario object.
                Can be used as an alternative to lab_hash and lab_name. If None, lab_hash or lab_name should be set.
            wait (Union[bool, Tuple[int, float]]): If True, wait until the end of the startup commands
                execution before executing the command. Default is False.
            stream (bool): If True, return an ExecStream object. If False,
                returns a tuple containing the complete stdout, the stderr, and the return code of the command.

        Returns:
            Union[ExecStream, Tuple[bytes, bytes, int]]: An ExecStream object or
            a tuple containing the stdout, the stderr and the return code of the command.

        Raises:
            InvocationError: If a running network scenario hash or name is not specified.
            MachineNotRunningError: If the specified device is not running.
            MachineBinaryError: If the binary of the command is not found.
            ValueError: If the wait value is neither a boolean nor a 2-tuple.

        Note:
            ``MachineNotRunningError`` is raised **by this call**, not by the
            first iteration of the returned stream: v3.8.3 lists the containers
            before it creates the stream (`DockerMachine.py:779-781`), and
            kathara-lab-checker wraps only the call in ``try/except`` while
            iterating outside it. A device that dies between this check and the
            exec itself still surfaces as an error event mid-stream, exactly as
            a device that dies mid-command does in v3.8.3.
        """
        check_required_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        # v3.8.3 parity: a string command is shlex-split by the manager
        # (`DockerMachine.py:803`), a list is passed through untouched.
        tokens = shlex.split(command) if type(command) is str else list(command)

        resolved_hash = self._resolve_hash(lab_hash, lab_name, lab)

        # The same probe v3.8.3 does, with the same condition: it lists with
        # `all=True` (`DockerMachine.py:1018`), so a stopped device is *found*
        # and only a missing one raises here.
        if not self._list_machines(lab_hash=resolved_hash, machine_name=machine_name):
            raise MachineNotRunningError(machine_name)

        args = ["--lab-hash", resolved_hash]
        args += self._wait_args(wait)
        args += [machine_name, "--"] + tokens

        exec_stream = _proc.stream_jsonl("exec", args)

        if stream:
            return exec_stream

        return _proc.collect_exec(exec_stream)

    def exec_obj(self, machine: Machine, command: Union[List[str], str], wait: Union[bool, Tuple[int, float]] = False,
                 stream: bool = True) -> Union[ExecStream, Tuple[bytes, bytes, int]]:
        """Exec a command on a device in a running network scenario.

        Args:
            machine (Machine): The device to connect.
            command (Union[List[str], str]): The command to exec on the device.
            wait (Union[bool, Tuple[int, float]]): If True, wait until the end of the startup commands
                execution before executing the command. Default is False.
            stream (bool): If True, return an ExecStream object. If False,
                returns a tuple containing the complete stdout, the stderr, and the return code of the command.

        Returns:
            Union[ExecStream, Tuple[bytes, bytes, int]]: An ExecStream object or
            a tuple containing the stdout, the stderr and the return code of the command.

        Raises:
            LabNotFoundError: If the specified device is not associated to any network scenario.
            MachineNotRunningError: If the specified device is not running.
            MachineBinaryError: If the binary of the command is not found.
        """
        lab = self._machine_lab(machine)

        return self.exec(machine.name, command, lab=lab, wait=wait, stream=stream)

    def copy_files(self, machine: Machine, guest_to_host: Dict[str, Union[str, io.IOBase]]) -> None:
        """Copy files on a running device in the specified paths.

        Args:
            machine (Kathara.model.Machine): A running device object.
            guest_to_host (Dict[str, Union[str, io.IOBase]]): A dict containing the device path as key and a
                fileobj to copy in path as value or a path to a file.

        Returns:
            None

        Raises:
            LabNotFoundError: If the device has no scenario.
        """
        lab = self._machine_lab(machine)
        files = []
        for guest_path, source in guest_to_host.items():
            if isinstance(source, str):
                files.append({"guest_path": guest_path, "host_path": source})
            elif isinstance(source, io.IOBase):
                content = source.read()
                if isinstance(source, io.TextIOBase):
                    content = content.encode("utf-8")
                source.seek(0, 2)
                source.seek(0)
                files.append({"guest_path": guest_path, "is_content": True,
                              "content_base64": base64.b64encode(content).decode("ascii")})
            else:
                raise ValueError("File type %s not supported" % type(source))
        payload = {"lab_hash": lab.hash, "machine_name": machine.name, "files": files}
        _proc.run_json("api", ["copy-files"], stdin_data=json.dumps(payload).encode("utf-8"))

    def retrieve_files(self, machine: Machine, src: str, dst: str) -> None:
        """Copy files from a running device path to the host.

        Args:
            machine (Kathara.model.Machine): A running device object.
            src (str): The path of the file or folder to copy from the device.
            dst (str): The destination path on the host.

        Returns:
            None

        Raises:
            LabNotFoundError: If the device has no scenario.
        """
        lab = self._machine_lab(machine)
        payload = {"lab_hash": lab.hash, "machine_name": machine.name, "src": src, "dst": dst}
        _proc.run_json("api", ["retrieve-files"], stdin_data=json.dumps(payload).encode("utf-8"))

    # ------------------------------------------------------------------ #
    # Inventory
    # ------------------------------------------------------------------ #

    def get_machine_api_object(self, machine_name: str, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                               lab: Optional[Lab] = None, all_users: bool = False) -> Any:
        """Return the inventory object of a running device in a network scenario."""
        check_required_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        machines = self._list_machines(
            lab_hash=self._resolve_hash(lab_hash, lab_name, lab), machine_name=machine_name, all_users=all_users
        )

        if not machines:
            raise MachineNotFoundError(f"Device `{machine_name}` not found.")

        return machines[0]

    def get_machines_api_objects(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                                 lab: Optional[Lab] = None, all_users: bool = False) -> List[Any]:
        """Return the inventory objects of running devices.

        Args:
            lab_hash (Optional[str]): The hash of the network scenario.
                Can be used as an alternative to lab_name and lab.
            lab_name (Optional[str]): The name of the network scenario.
                Can be used as an alternative to lab_hash and lab.
            lab (Optional[Kathara.model.Lab]): The network scenario object.
                Can be used as an alternative to lab_hash and lab_name.
            all_users (bool): If True, return information about devices of all users.

        Returns:
            List[Dict[str, Any]]: The inventory objects of the devices.

        Raises:
            InvocationError: If more than one param among lab_hash, lab_name and lab is specified.
        """
        check_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        return self._list_machines(lab_hash=self._resolve_hash(lab_hash, lab_name, lab), all_users=all_users)

    def get_link_api_object(self, link_name: str, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                            lab: Optional[Lab] = None, all_users: bool = False) -> Any:
        """Return the corresponding API object of a collision domain in a network scenario.

        Returns:
            Any: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_link_api_object",
            "`kathara list` is a device inventory and exposes no collision-domain listing."
        )

    def get_links_api_objects(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                              lab: Optional[Lab] = None, all_users: bool = False) -> List[Any]:
        """Return API objects of collision domains in a network scenario.

        Returns:
            List[Any]: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_links_api_objects",
            "`kathara list` is a device inventory and exposes no collision-domain listing."
        )

    def get_lab_from_api(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None) -> Lab:
        """Return the network scenario (specified by the hash or name), building it from API objects.

        Returns:
            Lab: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_lab_from_api",
            "Rebuilding a network scenario needs per-device interface data, which the inventory output does not carry."
        )

    def update_lab_from_api(self, lab: Lab) -> None:
        """Update the passed network scenario from API objects.

        Returns:
            None: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "update_lab_from_api",
            "Rebuilding a network scenario needs per-device interface data, which the inventory output does not carry."
        )

    # ------------------------------------------------------------------ #
    # Stats
    # ------------------------------------------------------------------ #

    def get_machines_stats(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                           lab: Optional[Lab] = None, machine_name: str = None, all_users: bool = False) \
            -> Generator[Dict[str, Dict[str, Any]], None, None]:
        """Return information about the running devices."""
        check_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        resolved_hash = self._resolve_hash(lab_hash, lab_name, lab)

        def generator() -> Generator[Dict[str, Dict[str, Any]], None, None]:
            machines = self._list_machines(lab_hash=resolved_hash, machine_name=machine_name, all_users=all_users)
            yield {m.get("container_name"): m for m in machines}

        return generator()

    def get_machine_stats(self, machine_name: str, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                          lab: Optional[Lab] = None, all_users: bool = False) \
            -> Generator[Optional[Dict[str, Any]], None, None]:
        """Return information of the specified device in a specified network scenario.

        Args:
            machine_name (str): The name of the device for which statistics are requested.
            lab_hash (Optional[str]): The hash of the network scenario.
                Can be used as an alternative to lab_name and lab. If None, lab_name or lab should be set.
            lab_name (Optional[str]): The name of the network scenario.
                Can be used as an alternative to lab_hash and lab. If None, lab_hash or lab should be set.
            lab (Optional[Kathara.model.Lab]): The network scenario object.
                Can be used as an alternative to lab_hash and lab_name. If None, lab_hash or lab_name should be set.
            all_users (bool): If True, search the device among all the users devices.

        Returns:
            Generator[Optional[Dict[str, Any]], None, None]: A generator containing the device inventory dict.
            Yields None if the device is not found.

        Raises:
            InvocationError: If a running network scenario hash or name is not specified.
            PrivilegeError: If all_users is True and the user does not have root privileges.
        """
        check_required_single_not_none_var(lab_hash=lab_hash, lab_name=lab_name, lab=lab)

        machines_stats = self.get_machines_stats(lab_hash=lab_hash, lab_name=lab_name, lab=lab,
                                                 machine_name=machine_name, all_users=all_users)
        machines_stats_next = next(machines_stats)
        if machines_stats_next:
            (_, machine_stats) = machines_stats_next.popitem()
            yield machine_stats
        else:
            yield None

    def get_machine_stats_obj(self, machine: Machine, all_users: bool = False) \
            -> Generator[Optional[Dict[str, Any]], None, None]:
        """Return information of the specified device in a specified network scenario.

        Args:
            machine (Machine): The device for which statistics are requested.
            all_users (bool): If True, search the device among all the users devices.

        Returns:
            Generator[Optional[Dict[str, Any]], None, None]: A generator containing the device inventory dict.

        Raises:
            LabNotFoundError: If the specified device is not associated to any network scenario.
            PrivilegeError: If all_users is True and the user does not have root privileges.
        """
        lab = self._machine_lab(machine)

        return self.get_machine_stats(machine.name, lab=lab, all_users=all_users)

    def get_links_stats(self, lab_hash: Optional[str] = None, lab_name: Optional[str] = None, lab: Optional[Lab] = None,
                        link_name: str = None, all_users: bool = False) -> Generator[Dict[str, Any], None, None]:
        """Return information about deployed networks.

        Returns:
            Generator: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_links_stats",
            "Resource statistics sampling and collision-domain inventory are not available."
        )

    def get_link_stats(self, link_name: str, lab_hash: Optional[str] = None, lab_name: Optional[str] = None,
                       lab: Optional[Lab] = None, all_users: bool = False) -> Generator[Any, None, None]:
        """Return information of the specified deployed network in a specified network scenario.

        Returns:
            Generator: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_link_stats",
            "Resource statistics sampling and collision-domain inventory are not available."
        )

    def get_link_stats_obj(self, link: Link, all_users: bool = False) -> Generator[Any, None, None]:
        """Return information of the specified deployed network in a specified network scenario.

        Returns:
            Generator: Never returns.

        Raises:
            NotSupportedError: Always, in this release.
        """
        raise _not_supported(
            "get_link_stats_obj",
            "Resource statistics sampling and collision-domain inventory are not available."
        )

    # ------------------------------------------------------------------ #
    # Environment
    # ------------------------------------------------------------------ #

    def check_image(self, image_name: str) -> None:
        """Check if the specified image is valid.

        Args:
            image_name (str): The name of the image

        Returns:
            None

        Raises:
            DockerImageNotFoundError: If the image is unavailable.
        """
        _proc.run_json("api", ["check-image"],
                       stdin_data=json.dumps({"image_name": image_name}).encode("utf-8"))

    @staticmethod
    def _check_report() -> Dict[str, Any]:
        """Return the `check` envelope, running the command at most once."""
        if not _CHECK_REPORT:
            _CHECK_REPORT.update(_proc.run_json("check", []))

        return _CHECK_REPORT

    def get_release_version(self) -> str:
        """Return the current manager version.

        Returns:
            str: The current manager version.
        """
        return self._check_report()["manager_version"]

    def get_formatted_manager_name(self) -> str:
        """Return a formatted string containing the current manager name.

        Returns:
            str: A formatted string containing the current manager name.
        """
        return self._check_report()["manager"]

    @staticmethod
    def get_available_managers_name() -> Dict[str, str]:
        """Return a dict containing the available manager names.

        Returns:
            Dict[str, str]: keys are the manager names, values are the formatted manager names.
        """
        return {name: FORMATTED_MANAGER_NAMES[name] for name in AVAILABLE_MANAGERS}
