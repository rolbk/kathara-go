# DIVERGENCES

Python bugs found during the port. Behaviour is ported as-is (spec §0.1); fixes are post-port commits.

## From Layer B vector generation (parser, validated against 3.8.3)

All ported as-is; vectors in `labfile/testdata/vectors/` pin the behaviour.

1. **`bridged_iface` in lab.conf is always fatal.** Stored as string, then `check()` compares it against ints → `TypeError`; with no interfaces → `NonSequentialMachineInterfaceError`. Docstrings imply it fills an interface slot; it cannot.
2. **Ulimit meta errors name the meta, not the device** — `add_meta` interpolates its `name` parameter instead of `self.name`.
3. **`pc1[ipv6]=false` stores the string `"false"` (truthy)**, while the API path stores a real bool for the same key. lab.conf and API disagree on the type.
4. **Trailing `#` comments only work after quoted values**; after unquoted values the comment text is swallowed into the value (or errors on interface lines).
5. **`LAB_*` values containing `=` crash** with an uncaught `ValueError: too many values to unpack`.
6. **A UTF-8 BOM breaks parsing of line 1** (`﻿` survives `strip()`), rejecting Windows "UTF-8 with BOM" lab.conf files.
7. **One badly-named directory (e.g. `Docs/`) kills the whole FolderParser run** — `lstart -F` fails outright on labs with any non-device folder next to the devices.
8. Cosmetic: invalid-volume-mode message ends with a trailing space; `OptionParser` errors embed CPython-runtime text (only the outer frame is portable — noted in ERROR_CODES).
