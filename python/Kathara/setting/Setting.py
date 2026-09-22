from __future__ import annotations

import json
import os
import time
from typing import Any, Dict, Optional, List

from .. import utils
from ..exceptions import ClassNotFoundError, InstantiationError
from ..exceptions import SettingsError, SettingsNotFoundError
from ..foundation.setting.SettingsAddon import SettingsAddon
from ..setting.addon.DockerSettingsAddon import DockerSettingsAddon
from ..setting.addon.KubernetesSettingsAddon import KubernetesSettingsAddon

AVAILABLE_DEBUG_LEVELS: List[str] = ["CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG", "EXCEPTION"]
AVAILABLE_MANAGERS: List[str] = ["docker", "kubernetes"]

#: Formatted manager names exposed by the client.
FORMATTED_MANAGER_NAMES: Dict[str, str] = {
    "docker": "Docker (Kathara)",
    "kubernetes": "Kubernetes (Megalos)",
}

SETTINGS_ADDONS: Dict[str, type] = {
    "docker": DockerSettingsAddon,
    "kubernetes": KubernetesSettingsAddon,
}

ONE_WEEK: int = 604800

DEFAULTS: Dict[str, Any] = {
    "image": 'kathara/base',
    "manager_type": 'docker',
    "terminal": utils.exec_by_platform(lambda: '/usr/bin/xterm', lambda: '', lambda: 'Terminal'),
    "open_terminals": True,
    "device_shell": '/bin/bash',
    "net_prefix": 'kathara',
    "device_prefix": 'kathara',
    "debug_level": 'INFO',
    "print_startup_log": True,
    "enable_ipv6": False,
    "volume_mount_policy": "Always",
}
SETTINGS_FILENAME = "kathara.conf"
DEFAULT_SETTINGS_PATH: str = os.path.join(utils.get_current_user_home(), ".config", SETTINGS_FILENAME)


class Setting(object):
    """Class responsible for interacting with Kathara Settings."""

    __slots__ = ['image', 'manager_type', 'terminal', 'open_terminals', 'device_shell', 'net_prefix',
                 'device_prefix', 'debug_level', 'print_startup_log', 'enable_ipv6', 'volume_mount_policy',
                 'last_checked', 'addons']

    __instance: Setting = None

    @staticmethod
    def get_instance() -> Setting:
        """Return an instance of Setting.

        Returns:
            Kathara.setting.Setting: An instance of Setting.

        Raises:
            InstantiationError: If two instances of the class are created.
        """
        if Setting.__instance is None:
            Setting()

        return Setting.__instance

    def __init__(self) -> None:
        if Setting.__instance is not None:
            raise InstantiationError("This class is a singleton!")
        else:
            # Load default settings to use
            for (name, value) in DEFAULTS.items():
                setattr(self, name, value)

            self.addons: Optional[SettingsAddon] = None
            self.last_checked: float = time.time() - ONE_WEEK

            self.load_settings_addon()  # Load default addon

            Setting.__instance = self

    def __getattr__(self, item: str) -> Any:
        return self.addons.get(item)

    def __setattr__(self, name: str, value: Any) -> None:
        if name in self.__slots__:
            super(Setting, self).__setattr__(name, value)
            return

        setattr(self.addons, name, value)

    def load_from_disk(self, path: Optional[str] = None) -> None:
        """Load settings from a specific path on disk.

        Args:
            path (Optional[str]): A path where the kathara.conf file is stored. If None, default path is used.

        Returns:
            None

        Raises:
            SettingsNotFoundError: If the Settings file is not found in specified path.
            SettingsError: If the specified file is not a valid JSON.
        """
        settings_path = os.path.join(path, SETTINGS_FILENAME) if path is not None else DEFAULT_SETTINGS_PATH

        if not os.path.exists(settings_path):  # Requested settings file doesn't exist, throw exception
            raise SettingsNotFoundError(settings_path)
        else:  # Requested settings file exists, read it and check values
            settings = {}
            with open(settings_path, 'r') as settings_file:
                try:
                    settings = json.load(settings_file)
                except ValueError:
                    raise SettingsError("Not a valid JSON.")

            for name, value in settings.items():
                if hasattr(self, name):
                    setattr(self, name, value)

            self.load_settings_addon()  # Manager may be changed with loaded settings, reload addon
            self.addons.load(settings)  # Load values into the addon object

    def save_to_disk(self, path: Optional[str] = None) -> None:
        """Saves settings to a kathara.conf file in the specified path on disk.

        Args:
            path (Optional[str]): A path where the kathara.conf file will be stored. If None, default path is used.

        Returns:
            None
        """
        settings_path = os.path.join(path, SETTINGS_FILENAME) if path is not None else DEFAULT_SETTINGS_PATH
        settings_dirname = os.path.dirname(settings_path)

        if not os.path.isdir(settings_dirname):  # Create folder if it doesn't exist
            os.mkdir(settings_dirname)

        to_save = self.addons.merge(self._to_dict())

        with open(settings_path, 'w') as settings_file:
            settings_file.write(json.dumps(to_save, indent=True))

        def unix_permissions():
            (uid, gid) = utils.get_current_user_uid_gid()

            os.chmod(settings_path, 0o600)
            os.chown(settings_path, uid, gid)

        # If Linux or Mac, set the right permissions and ownership to the settings file.
        utils.exec_by_platform(unix_permissions, lambda: None, unix_permissions)

    def load_from_dict(self, settings: Dict[str, Any]) -> None:
        """Load settings from a dict.

        Args:
            settings (Dict[str, Any]): A dict containing the settings name as key and its value.

        Returns:
            None
        """
        for name, value in settings.items():
            if hasattr(self, name):
                setattr(self, name, value)

        self.load_settings_addon()  # Manager may be changed with loaded settings, reload addon
        self.addons.load(settings)  # Load values into the addon object

    @staticmethod
    def wipe_from_disk() -> None:
        """Remove settings from the default settings path on disk.

        Returns:
            None
        """
        if os.path.exists(DEFAULT_SETTINGS_PATH):
            os.remove(DEFAULT_SETTINGS_PATH)

    def check(self) -> None:
        """Check the correctness and validity of the settings.

        Check that the selected manager is available and that net_prefix,
        device_prefix and debug_level are valid.

        Returns:
            None

        Raises:
            SettingsError: If the Manager Type is not allowed.
            SettingsError: If the Networks Prefix does not contain only lowercase letters and underscore.
            SettingsError: If the Device Prefix does not contain only lowercase letters and underscore.
            SettingsError: If the Debug Level specified is not allowed.
        """
        self._check_manager()

        try:
            utils.re_search_fail(r"^[a-z]+_?[a-z_]+$", self.net_prefix)
        except ValueError:
            raise SettingsError("Networks Prefix must only contain lowercase letters and underscore.")

        try:
            utils.re_search_fail(r"^[a-z]+_?[a-z_]+$", self.device_prefix)
        except ValueError:
            raise SettingsError("Device Prefix must only contain lowercase letters and underscore.")

        if self.debug_level not in AVAILABLE_DEBUG_LEVELS:
            raise SettingsError("Debug Level must be one of the following: %s." % (", ".join(AVAILABLE_DEBUG_LEVELS)))

    def _check_manager(self) -> None:
        """Check if the selected manager is available.

        Returns:
            None

        Raises:
            SettingsError: If the Manager Type is not allowed.
        """
        if self.manager_type not in AVAILABLE_MANAGERS:
            raise SettingsError("Manager Type not allowed.")

    def check_image(self, image: str = None) -> None:
        """Check if the specified image is valid."""
        image = self.image if not image else image

        # Required to import here because otherwise there is a cyclic dependency
        from ..manager.Kathara import Kathara
        Kathara.get_instance().check_image(image)

    def check_terminal(self, terminal: str = None) -> bool:
        """Check that the selected terminal is available.

        Args:
            terminal (str): The selected terminal path. If None, check the availability of the default terminal.

        Returns:
            bool: True if the selected terminal is TMUX (that do not require path), else False.

        Raises:
            SettingsError: If the terminal emulator specified is not found.
        """
        terminal = self.terminal if not terminal else terminal

        # Skip check for TMUX (special value)
        if terminal == "TMUX":
            return True

        def check_unix():
            return os.path.isfile(terminal) and os.access(terminal, os.X_OK)

        def check_osx():
            return bool(terminal)

        if not utils.exec_by_platform(check_unix, lambda: True, check_osx):
            raise SettingsError("Terminal Emulator `%s` not valid! Install it before using it." % terminal)

        return True

    def load_settings_addon(self) -> None:
        """Load a setting addon to the base settings.

        Returns:
            None

        Raises:
            ClassNotFoundError: If the manager type has no settings addon.
        """
        addon_class = SETTINGS_ADDONS.get(self.manager_type)
        if addon_class is None:
            # v3.8.3 reaches this through `SettingsAddonFactory`, whose dynamic
            # import fails with a bare `ClassNotFoundError`
            # (`foundation/factory/Factory.py:24`). `Setting.check()` is where an
            # unknown manager gets its `SettingsError`, and it still does.
            raise ClassNotFoundError()

        self.addons = addon_class()

    def _to_dict(self) -> Dict[str, Any]:
        return {
            "image": self.image,
            "manager_type": self.manager_type,
            "terminal": self.terminal,
            "open_terminals": self.open_terminals,
            "device_shell": self.device_shell,
            "net_prefix": self.net_prefix,
            "device_prefix": self.device_prefix,
            "debug_level": self.debug_level,
            "print_startup_log": self.print_startup_log,
            "enable_ipv6": self.enable_ipv6,
            "volume_mount_policy": self.volume_mount_policy,
            "last_checked": self.last_checked
        }
