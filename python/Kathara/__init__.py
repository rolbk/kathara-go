"""Kathará Python client package.

This is the pure-Python half of the Kathará Go port (``PORT_SPEC.md`` §7):

* the **model** (:mod:`Kathara.model`) and the **netkit parsers**
  (:mod:`Kathara.parser.netkit`) are ported from Kathará v3.8.3 and stay in
  Python — they never touch Docker or Kubernetes;
* the **manager facade** (:class:`Kathara.manager.Kathara.Kathara`) keeps the
  same public method names but is backed by the Go ``kathara`` binary over the
  JSON CLI contract (``docs/port/JSON_CLI_CONTRACT.md``) instead of the Python
  Docker/Kubernetes SDKs.

The import surface is unchanged from v3.8.3, so existing consumers keep
working::

    from Kathara.model.Lab import Lab
    from Kathara.manager.Kathara import Kathara
    from Kathara.parser.netkit.LabParser import LabParser
    from Kathara.exceptions import MachineNotRunningError
    from Kathara.setting.Setting import Setting
"""

import warnings

# Suppress deprecation warning from fs library using pkg_resources
# See: https://github.com/PyFilesystem/pyfilesystem2/issues/577
warnings.filterwarnings("ignore", message="pkg_resources is deprecated as an API")
