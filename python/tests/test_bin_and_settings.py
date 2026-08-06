"""Binary discovery (`PORT_SPEC.md` §7.3) and the settings surface (§0.4)."""

import json
import os
import shutil
import stat
import tempfile
import unittest

import _support  # noqa: F401  (puts the client package on sys.path)

from Kathara import _bin
from Kathara.exceptions import ClassNotFoundError, SettingsError, SettingsNotFoundError
from Kathara.setting.Setting import DEFAULTS, SETTINGS_FILENAME, Setting


class BinaryDiscoveryTest(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-bin-test-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)
        self._saved = os.environ.get("KATHARA_BIN")
        self.addCleanup(self._restore)
        _bin.clear_cache()
        self.addCleanup(_bin.clear_cache)

    def _restore(self):
        if self._saved is None:
            os.environ.pop("KATHARA_BIN", None)
        else:
            os.environ["KATHARA_BIN"] = self._saved

    def test_env_override_wins(self):
        target = os.path.join(self.tmpdir, "kathara")
        with open(target, "w") as handle:
            handle.write("#!/bin/sh\n")
        os.chmod(target, os.stat(target).st_mode | stat.S_IEXEC)

        os.environ["KATHARA_BIN"] = target
        self.assertEqual(target, _bin.binary_path())

    def test_missing_binary_raises_the_v383_message(self):
        # `ERROR_CODES.md` §2, code FileNotFound.
        os.environ.pop("KATHARA_BIN", None)
        original_scripts, original_which = _bin._scripts_dirs, _bin.shutil.which
        _bin._scripts_dirs = lambda: [self.tmpdir]
        _bin.shutil.which = lambda name: None
        try:
            _bin.clear_cache()
            with self.assertRaises(FileNotFoundError) as caught:
                _bin.binary_path()
            self.assertEqual("Unable to find Kathara.", str(caught.exception))
        finally:
            _bin._scripts_dirs, _bin.shutil.which = original_scripts, original_which

    def test_a_stale_override_is_an_error_not_a_fallback(self):
        # A `$KATHARA_BIN` that does not resolve must fail with the frozen
        # message (`ERROR_CODES.md` §2), not reach subprocess as an OS error and
        # not silently pick a different Kathara.
        os.environ["KATHARA_BIN"] = os.path.join(self.tmpdir, "gone")

        _bin.clear_cache()
        with self.assertRaises(FileNotFoundError) as caught:
            _bin.binary_path()
        self.assertEqual("Unable to find Kathara.", str(caught.exception))
        self.assertIsNone(_bin.find_binary())

    def test_a_non_executable_override_is_an_error(self):
        target = os.path.join(self.tmpdir, "kathara")
        with open(target, "w") as handle:
            handle.write("#!/bin/sh\n")
        os.chmod(target, 0o644)

        os.environ["KATHARA_BIN"] = target
        _bin.clear_cache()
        with self.assertRaises(FileNotFoundError):
            _bin.binary_path()

    def test_scripts_dir_is_preferred_over_path(self):
        # A venv install must never be shadowed by a system-wide Kathara.
        scripts = os.path.join(self.tmpdir, "scripts")
        os.makedirs(scripts)
        target = os.path.join(scripts, "kathara")
        with open(target, "w") as handle:
            handle.write("#!/bin/sh\n")
        os.chmod(target, os.stat(target).st_mode | stat.S_IEXEC)

        os.environ.pop("KATHARA_BIN", None)
        original_scripts, original_which = _bin._scripts_dirs, _bin.shutil.which
        _bin._scripts_dirs = lambda: [scripts]
        _bin.shutil.which = lambda name: "/usr/bin/kathara"
        try:
            _bin.clear_cache()
            self.assertEqual(target, _bin.binary_path())
        finally:
            _bin._scripts_dirs, _bin.shutil.which = original_scripts, original_which


class SettingTest(unittest.TestCase):
    def setUp(self):
        self.tmpdir = tempfile.mkdtemp(prefix="kathara-settings-test-")
        self.addCleanup(shutil.rmtree, self.tmpdir, True)
        # The singleton is process-wide; hand each test a fresh one.
        self._saved = Setting._Setting__instance
        Setting._Setting__instance = None
        self.addCleanup(self._restore)
        self.setting = Setting.get_instance()

    def _restore(self):
        Setting._Setting__instance = self._saved

    def test_defaults_match_v383(self):
        for name, value in DEFAULTS.items():
            self.assertEqual(value, getattr(self.setting, name), name)

    def test_docker_addon_is_the_default(self):
        self.assertEqual("Prompt", self.setting.image_update_policy)
        self.assertEqual("kathara/katharanp_vde", self.setting.network_plugin)

    def test_round_trip_through_the_same_file_name(self):
        self.setting.image = "kathara/frr"
        self.setting.save_to_disk(self.tmpdir)

        path = os.path.join(self.tmpdir, SETTINGS_FILENAME)
        self.assertTrue(os.path.exists(path))

        with open(path) as handle:
            saved = json.load(handle)
        self.assertEqual("kathara/frr", saved["image"])
        # The addon keys are merged into the same flat document as v3.8.3.
        self.assertIn("hosthome_mount", saved)

        Setting._Setting__instance = None
        reloaded = Setting.get_instance()
        reloaded.load_from_disk(self.tmpdir)
        self.assertEqual("kathara/frr", reloaded.image)

    def test_missing_file_raises_settings_not_found(self):
        with self.assertRaises(SettingsNotFoundError) as caught:
            self.setting.load_from_disk(self.tmpdir)
        self.assertEqual(
            "Settings file not found in path `%s`." % os.path.join(self.tmpdir, SETTINGS_FILENAME),
            str(caught.exception),
        )

    def test_invalid_json_raises_settings_error(self):
        with open(os.path.join(self.tmpdir, SETTINGS_FILENAME), "w") as handle:
            handle.write("{not json")

        with self.assertRaises(SettingsError) as caught:
            self.setting.load_from_disk(self.tmpdir)
        self.assertEqual(
            "Settings file is not valid: Not a valid JSON. Fix it or delete it before launching.",
            str(caught.exception),
        )

    def test_load_from_dict_is_what_lab_checker_uses(self):
        self.setting.load_from_dict({"image": "kathara/frr"})
        self.assertEqual("kathara/frr", self.setting.image)

    def test_check_validates_prefixes_and_debug_level(self):
        self.setting.check()

        self.setting.net_prefix = "Kathara"
        with self.assertRaises(SettingsError) as caught:
            self.setting.check()
        self.assertEqual(
            "Settings file is not valid: Networks Prefix must only contain lowercase letters and underscore. "
            "Fix it or delete it before launching.",
            str(caught.exception),
        )

        self.setting.net_prefix = "kathara"
        self.setting.device_prefix = "Kathara"
        with self.assertRaises(SettingsError):
            self.setting.check()

        self.setting.device_prefix = "kathara"
        self.setting.debug_level = "LOUD"
        with self.assertRaises(SettingsError) as caught:
            self.setting.check()
        self.assertEqual(
            "Settings file is not valid: Debug Level must be one of the following: "
            "CRITICAL, ERROR, WARNING, INFO, DEBUG, EXCEPTION. Fix it or delete it before launching.",
            str(caught.exception),
        )

    def test_check_rejects_an_unknown_manager(self):
        self.setting.manager_type = "podman"
        with self.assertRaises(SettingsError) as caught:
            self.setting.check()
        self.assertEqual(
            "Settings file is not valid: Manager Type not allowed. Fix it or delete it before launching.",
            str(caught.exception),
        )

    def test_kubernetes_addon_swaps_in(self):
        self.setting.load_from_dict({"manager_type": "kubernetes"})
        self.assertEqual("IfNotPresent", self.setting.image_pull_policy)

    def test_an_unknown_manager_has_no_addon(self):
        # v3.8.3 reaches this through `SettingsAddonFactory`, whose failed
        # dynamic import is a bare `ClassNotFoundError`
        # (`foundation/factory/Factory.py:24`); `check()` is what produces the
        # `SettingsError`, and it still does (see above).
        with self.assertRaises(ClassNotFoundError):
            self.setting.load_from_dict({"manager_type": "podman"})


if __name__ == "__main__":
    unittest.main()
