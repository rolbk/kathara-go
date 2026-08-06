"""Locate the bundled Kathará Go binary.

`PORT_SPEC.md` §7.3: the wheels ship the binary in the wheel's
``.data/scripts/`` directory, so ``pip install kathara`` drops it straight onto
``PATH`` next to the interpreter (the ``ruff``/``uv`` pattern). Resolution
order, first hit wins:

1. ``$KATHARA_BIN`` — an explicit override, also what the test-suite uses to
   point the client at a fake binary;
2. the scripts directory of the running interpreter (``sysconfig`` schemes),
   which is where the wheel put it — checked before ``PATH`` so a venv install
   is never shadowed by a system-wide Kathará;
3. ``PATH`` (``shutil.which``), which covers editable installs, distro packages
   and developers running the client against a hand-built binary;
4. ``Kathara/bin/kathara`` inside the installed package, the layout sketched in
   spec §7.1, kept as a fallback for anyone repackaging that way.
"""

import os
import shutil
import sys
import sysconfig
from typing import List, Optional

__all__ = ['BINARY_NAME', 'ENV_OVERRIDE', 'binary_path', 'find_binary', 'clear_cache']

#: Name of the Go binary, sans extension.
BINARY_NAME: str = "kathara"

#: Environment variable that overrides discovery entirely.
ENV_OVERRIDE: str = "KATHARA_BIN"

_cached: Optional[str] = None


def _exe_names() -> List[str]:
    if sys.platform == "win32":
        return [BINARY_NAME + ".exe", BINARY_NAME]
    return [BINARY_NAME]


def _is_executable(path: str) -> bool:
    return os.path.isfile(path) and os.access(path, os.X_OK)


def _scripts_dirs() -> List[str]:
    dirs = []
    for scheme_getter in (
            lambda: sysconfig.get_path("scripts"),
            lambda: sysconfig.get_path("scripts", "user"),
    ):
        try:
            path = scheme_getter()
        except Exception:  # pragma: no cover - exotic sysconfig schemes
            path = None
        if path:
            dirs.append(path)

    # A venv's scripts dir is where the interpreter lives; sysconfig usually
    # agrees, but a relocated venv can disagree, so add it explicitly.
    dirs.append(os.path.dirname(os.path.abspath(sys.executable)))

    seen = set()
    unique = []
    for directory in dirs:
        if directory not in seen:
            seen.add(directory)
            unique.append(directory)
    return unique


def find_binary() -> Optional[str]:
    """Return the absolute path of the Kathará binary, or None if not found.

    Returns:
        Optional[str]: The absolute path of the binary, None if it is not found.
    """
    override = os.environ.get(ENV_OVERRIDE)
    if override:
        # An override that does not resolve is an error, never a reason to fall
        # back to a different Kathara: the point of setting it is to pin one.
        candidate = os.path.abspath(override)
        return candidate if _is_executable(candidate) else None

    for directory in _scripts_dirs():
        for name in _exe_names():
            candidate = os.path.join(directory, name)
            if _is_executable(candidate):
                return os.path.abspath(candidate)

    for name in _exe_names():
        which_path = shutil.which(name)
        if which_path:
            return os.path.abspath(which_path)

    package_bin = os.path.join(os.path.dirname(os.path.abspath(__file__)), "bin")
    for name in _exe_names():
        candidate = os.path.join(package_bin, name)
        if _is_executable(candidate):
            return os.path.abspath(candidate)

    return None


def binary_path() -> str:
    """Return the absolute path of the Kathará binary.

    Returns:
        str: The absolute path of the binary.

    Raises:
        FileNotFoundError: If the binary cannot be found. The message is the one
            Kathará v3.8.3 used when it could not locate itself
            (`ERROR_CODES.md` §2, code `FileNotFound`).
    """
    global _cached

    # The override is re-read every call: tests flip it between invocations.
    override = os.environ.get(ENV_OVERRIDE)
    if override:
        candidate = os.path.abspath(override)
        if not _is_executable(candidate):
            raise FileNotFoundError("Unable to find Kathara.")
        return candidate

    if _cached is not None:
        return _cached

    path = find_binary()
    if path is None:
        raise FileNotFoundError("Unable to find Kathara.")

    _cached = path
    return path


def clear_cache() -> None:
    """Forget the memoized binary path.

    Returns:
        None
    """
    global _cached
    _cached = None
