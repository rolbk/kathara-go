# Kathara v3.8.3 — Complete User-Facing CLI Contract

Source of truth: `/root/kathara/kathara-python/src/kathara.py`,
`/root/kathara/kathara-python/src/Kathara/cli/command/*.py`,
`/root/kathara/kathara-python/src/Kathara/cli/ui/utils.py`,
`/root/kathara/kathara-python/src/Kathara/cli/ui/event/*.py`,
cross-checked against `/root/kathara/kathara-python/docs/*.ronn`.

Conventions used below:

- Every sub-command parser is built with `argparse.ArgumentParser(prog='kathara <cmd>', description=strings[<cmd>], epilog=wiki_description, add_help=False)` and then adds an explicit `-h, --help` (`action='help'`, `default=argparse.SUPPRESS`, help `"Show a help message and exit."`). The epilog on every command is:
  `For examples and further information visit: https://github.com/KatharaFramework/Kathara/wiki`
- "MEG" = argparse mutually-exclusive group. "req" = `required=True`.
- Unless stated, `default` is argparse's implicit `None` (or `False` for `store_true`).
- Parsed args are stashed in the `CliArgs` singleton (`vars(namespace)` dict); commands read them via `get_args()` — the Go port must preserve *dest* names only insofar as they map to behavior, the dests below are documented for traceability.
- Any argparse parse error (unknown flag, missing required, bad choice, `ArgumentTypeError` from a custom type) prints usage + error to **stderr** and exits with **code 2** (argparse default). This is part of the observable contract.
- `--` handling and prefix matching are stock argparse (abbreviated long flags are accepted, e.g. `--dir` for `--directory`, as long as unambiguous). The Go port must decide whether to reproduce abbreviation; golden tests only exercise full names.

---

## 0. Global entry point — `src/kathara.py`

### 0.1 Top-level parser

`argparse.ArgumentParser(description='A network emulation tool.', usage=description_msg, add_help=False)` where `description_msg` is:

```
kathara [-h] [-v] <command> [<args>]

Possible Kathara commands are:

<two-column table rendered by rich (no borders): command name, description from strings dict>
```

The strings dict (order matters — it is the display order):
`vstart, vclean, vconfig, lstart, lclean, linfo, lrestart, lconfig, connect, exec, wipe, list, settings, check`.

| Arg | Details |
|---|---|
| `-h, --help` | `action='help'`, `default=SUPPRESS`, help `"Show an help message and exit."` (note: "an help" — sic, differs from sub-commands' "a help") |
| `command` | positional, `nargs='?'`, help `"Command to run."` |
| `-v, --version` | `action='store_true'`, `required=False`, help `"Print the current Kathara version."` |

Only `sys.argv[1:2]` is parsed at top level (exactly one token); everything from `sys.argv[2:]` is passed verbatim to the sub-command's own parser. Consequence: `kathara -v` works, `kathara lstart -v` does *not* hit the global version flag.

### 0.2 Dispatch sequence (order is observable)

1. If `-v/--version`: print `Current version: 3.8.3` (format `'Current version: %s' % CURRENT_VERSION`) to stdout, exit **0**.
2. If `command` is `None` **or** `not command.islower()`: print top-level help, exit **1**. (So `kathara LSTART` → help + exit 1, not "unrecognized".)
3. Unless `"settings" in command` (substring test): `Setting.get_instance().check()` runs. On `SettingsError` / `DockerDaemonConnectionError`: `logging.critical("({type}) {msg}")`, exit **1**.
4. Command lookup: `CommandFactory().create_instance(class_args=(command.capitalize(),))` → imports `Kathara.cli.command.<Name>Command`. On `ClassNotFoundError`: `logging.error("Unrecognized command \`<cmd>\`.")` + top-level help, exit **1**. On `ImportError`: `logging.critical("({type}) \`{e.name}\` is not installed in your system")`, exit **1**.
5. `exit_code = command_object.run(os.getcwd(), sys.argv[2:])`; process exits with that code.

### 0.3 Exception / signal contract

- `KeyboardInterrupt` during a command: if command **not in** `['exec', 'linfo', 'list', 'settings']`, emit `logging.warning("You interrupted Kathara during a command. The system may be in an inconsistent state! If you encounter any problem please run `kathara wipe`.")`. Exit **0** in all cases (including the exempt four).
- Any other `Exception`: if `debug_level == "EXCEPTION"` → `logging.exception(...)` (full rich traceback), else `logging.critical("({ExcType}) {msg}")`. Exit **1**.
- `SystemExit` propagates untouched (used by wipe's declined confirmation → exit 0, argparse errors → exit 2).

### 0.4 Startup side effects (before dispatch)

- Settings loaded from disk; if `SettingsNotFoundError`, defaults are written to disk first.
- `logging.basicConfig(level=<debug_level or DEBUG>, format="%(message)s", handlers=[RichHandler(rich_tracebacks=True, show_time=False, show_path=False)])`. `debug_level == "EXCEPTION"` maps to `DEBUG` for the logger.
- **All `logging` output goes to stdout** (RichHandler's default `Console` writes to stdout), not stderr. Same for all rich `Console.print`, panels, tables, progress bars, prompts, and live screens. The only writer of **stderr** in the whole CLI is `exec`'s pass-through of the remote command's stderr, plus argparse's own error messages. (Port spec §5 changes this for json/jsonl modes; the `human` mode contract is "everything on stdout".)
- CLI event handlers registered (progress bars etc., see §14) and unregistered before every `sys.exit`.
- `utils.check_python_version()`: Python < 3.9 → `logging.critical("Python version should be at least 3.9")`, exit **1**.
- On Linux, effective privileges are dropped (`PrivilegeHandler`) — auth/ is deferred scope, but note the CLI calls it.

### 0.5 Common per-command helpers

- `_load_custom_configuration(lab_path)`: every lab-path-taking command checks for `<lab_path>/kathara.conf`; if present logs `logging.info('Loading custom kathara.conf file from path \`<path>\`...')` and loads it. Runs *after* `-d` resolution, before parsing the lab.
- Lab-path resolution idiom (lstart/lclean/lrestart/linfo/lconfig/connect/exec): `args['directory'].replace('"','').replace("'",'')` if given else `current_path`, then `utils.get_absolute_path()` (realpath, following symlinks).
- Each command's `console` is a rich `Console` with a theme mapping `kathara.lab_*` styles to `bold dark_orange3`.

### 0.6 Custom argparse types (`cli/ui/utils.py`) — exact error text

| Type fn | Used by | Returns | Errors |
|---|---|---|---|
| `alphanumeric(value)` | `--rm` (vconfig, lconfig) | value, must match `^\w+$` | `ArgumentTypeError("invalid alphanumeric value")` → argparse error, exit 2 |
| `interface_cd_mac(value)` | vstart `--eth` | tuple `(n, cd, mac)` from `N:CD[/MAC]` | `ArgumentTypeError("invalid interface definition: %s" % value)`; or `ArgumentTypeError("invalid interface definition, collision domain \`<cd>\` contains non-alphanumeric characters")` (CD must match `^\w+$`). Trailing empty MAC (`0:A/`) is invalid |
| `cd_mac(value)` | `--add` (vconfig, lconfig) | tuple `(cd, mac)` from `CD[/MAC]` via `utils.parse_cd_mac_address` | raises **`SyntaxError(f"Invalid interface definition: \`{value}\`.")`** — NOT an `ArgumentTypeError`, so argparse does *not* catch it; it propagates to the top-level handler → `CRITICAL (SyntaxError) ...`, exit **1** (not 2). Faithful-port trap |
| `volume(value)` | vstart `--volume` | the raw string; must split on `|` into exactly 2 or 3 non-empty parts | `ArgumentTypeError("invalid volume definition: %s" % value)` → exit 2 |

`parse_cd_mac_address`: splits on `/`, filters empty parts, requires exactly 2 parts if `/` present; no `/` → `(value, None)`.

### 0.7 Confirmation-prompt primitive

`confirmation_prompt(prompt_string, callback_yes, callback_no)` = `rich.prompt.Confirm.ask(prompt_string)`; renders as `<prompt> [y/n]: ` on stdout, re-asks on invalid input (`Please enter Y or N`), no default (Enter alone re-prompts). Not-confirmed → `callback_no()`.

---

## 1. `kathara lstart` — flags: 16 args (15 options + 1 positional); 3 MEGs

Description: `Start a Kathara network scenario`.

| Flag(s) | dest | action/type | nargs | default | Notes / help text |
|---|---|---|---|---|---|
| `-h, --help` | — | help | | SUPPRESS | "Show a help message and exit." |
| `--noterminals` | `terminals` | `store_const` `const=False` | | `None` | MEG-1. "Start the network scenario without opening terminal windows." |
| `--terminals` | `terminals` | `store_const` `const=True` | | (shared `None`) | MEG-1. "Start the network scenario opening terminal windows." |
| `--privileged` | `privileged` | `store_const` `const=True` | | `None` | "Start the devices in privileged mode. MUST BE ROOT FOR THIS OPTION." |
| `-d, --directory` | `directory` | str | | `None` | "Specify the folder containing the network scenario." |
| `-F, --force-lab` | `force_lab` | `store_true` | | `False` | "Force the network scenario to start without a lab.conf or lab.dep file." |
| `-l, --list` | `list` | `store_true` | | `False` | "Show information about running devices after the network scenario has been started." |
| `-o, --pass` | `global_machine_metadata` | str, metavar `METADATA` | `*` | `None` | "Apply metadata to all devices of a network scenario during startup." Parsed by `OptionParser` (key=value pairs) |
| `--terminal-emu` | `terminal_emu` | str | | `None` | "Set a different terminal emulator application (Unix only)." |
| `--print, --dry-mode` | `dry_mode` | `store_true` | | `False` | "Open the lab.conf file and check if it is correct (dry run)." |
| `--no-hosthome, -H` | `hosthome_mount` | `store_const` `const=False` | | `None` | MEG-2. "Do not mount \"/hosthome\" directory inside devices." |
| `--hosthome` | `hosthome_mount` | `store_const` `const=True` | | `None` | MEG-2. "Mount \"/hosthome\" directory inside devices." |
| `--no-shared, -S` | `shared_mount` | `store_const` `const=False` | | `None` | MEG-3. "Do not mount \"/shared\" directory inside devices." |
| `--shared` | `shared_mount` | `store_const` `const=True` | | `None` | MEG-3. "Mount \"/shared\" directory inside devices." |
| `--exclude` | `excluded_machines` | str, metavar `DEVICE_NAME` | `+` | `[]` | "Exclude specified devices from startup." |
| `machine_name` (positional) | `machine_name` | str, metavar `DEVICE_NAME` | `*` | `[]` | "Launches only specified devices." |

Tri-state semantics (spec §4.2): `terminals`, `hosthome_mount`, `shared_mount`, `privileged` are all None-when-unset; `terminals=None` means "use Setting.open_terminals", `hosthome_mount/shared_mount=None` fall through to settings inside the manager, `privileged=None` is stored as global machine metadata regardless.

Runtime behavior / stdout (in order):

1. Lab path resolved; custom `kathara.conf` loaded (INFO log line if present).
2. `Setting.open_terminals` overridden by `terminals` if not None; `Setting.terminal` overridden by `--terminal-emu` if truthy.
3. Panel (blue bold, centered, square box): `Starting Network Scenario` — or `Checking Network Scenario` in dry mode.
4. `LabParser.parse(lab_path)`; on `IOError`: re-raised unless `-F`, in which case `FolderParser.parse` (one device per subdirectory).
5. `lab.dep` parsed; if present, machines reordered (`apply_dependencies`).
6. If the lab has meta info (`str(lab)` non-empty): a panel with the lab metadata, highlighted by `LabMetaHighlighter` (lines `Name: `, `Description: `, `Version: `, `Author(s): `, `Email: `, `Website: ` in bold dark_orange3).
7. Empty lab (`len(lab.machines) <= 0`) → raises `EmptyLabError` → top handler → exit 1.
8. `-o/--pass` parsed by `OptionParser` into `lab.global_machine_metadata`.
9. `lab.ext` handling: if `<lab_path>/lab.ext` exists — Linux only (`OSError("lab.ext is only available on Linux systems.")` otherwise), root only (`PrivilegeError("You must be root in order to use lab.ext file.")` otherwise); when used, terminals are force-disabled. (ExtParser itself is DEFERRED scope; the *gating errors* above are still CLI surface.)
10. Dry mode: prints (green) `✓ lab.conf file is correct.`, plus `✓ lab.dep file is correct.` if deps present, plus `✓ lab.ext file is correct.` if lab.ext present; returns **0** before any deploy.
11. `hosthome_mount` / `shared_mount` stored as lab options.
12. Privileged check: if `--privileged` or any machine meta privileged: non-root → `PrivilegeError("You must be root in order to start Kathara devices in privileged mode.")` (exit 1); root with open_terminals → yellow line `⚠ Running devices with privileged capabilities, terminals might not open!`.
13. `deploy_lab(lab, selected_machines=set(machine_name), excluded_machines=set(excluded_machines))`. Progress bars (see §14): `Deploying collision domains` then `Deploying devices`; possible Docker-pull progress bar; possible image-update prompt; possible volume-mount prompt (see §14). Unknown selected/excluded names raise `MachineNotFoundError` (exit 1).
14. `-l/--list`: rich status spinner `Loading...` (dots), then the machines-stats table (§13 format).

Exit codes: 0 success/dry-run; 1 any raised exception (missing lab.conf without `-F` = `IOError`, EmptyLabError, PrivilegeError, MachineNotFoundError, Docker errors...); 2 argparse.

Doc cross-check (`kathara-lstart.1.ronn`): synopsis and options match; doc calls `-o/--pass` values "options" with metavar `OPTION` whereas code metavar is `METADATA` and help says "Apply metadata..." (M-8).

---

## 2. `kathara lclean` — flags: 4 args (3 options + 1 positional)

Description: `Stop a Kathara network scenario`.

| Flag(s) | dest | action/type | nargs | default | Help |
|---|---|---|---|---|---|
| `-h, --help` | — | help | | SUPPRESS | "Show a help message and exit." |
| `-d, --directory` | `directory` | str | | `None` | "Specify the folder containing the network scenario." |
| `--exclude` | `excluded_machines` | str, metavar `DEVICE_NAME` | `+` | `[]` | "Exclude specified devices from clean." |
| `machine_names` (positional) | `machine_names` | str, metavar `DEVICE_NAME` | `*` | `[]` | "Clean only specified devices." |

Behavior: lab path resolved, custom conf loaded; `LabParser.parse` — **any** exception falls back to `Lab(None, path=lab_path)` (so lclean works on an arbitrary directory: hash computed from path). Panel `Stopping Network Scenario` (blue bold). `undeploy_lab(lab_hash, selected_machines=set|None, excluded_machines=set|None)` (empty lists → None, not empty set). Progress bars: `Deleting devices`, `Deleting collision domains`. Exit 0; 1 on manager exceptions; 2 argparse.

Doc cross-check: matches.

---

## 3. `kathara lrestart` — flags: 15 args (14 options + 1 positional); 3 MEGs

Description: `Restart a Kathara network scenario`.

Identical rows to lstart **except**:

- No `--print/--dry-mode`, no `--terminal-emu`.
- Extra flag `--xterm` (dest `xterm`, str, optional): "Set a different terminal emulator application (Unix only)."
- `--noterminals`/`--terminals` MEG has **`default=True`** (not `None`) — irrelevant in practice, see below.
- Help wording: `--exclude` = "Exclude specified devices."; positional help = "Restarts only specified devices."

| Flag(s) | dest | action | default |
|---|---|---|---|
| `-h, --help` | — | help | SUPPRESS |
| `--noterminals` / `--terminals` | `terminals` | store_const False/True | `True` |
| `--privileged` | `privileged` | store_const True | `None` |
| `-d, --directory` | `directory` | str | `None` |
| `-F, --force-lab` | `force_lab` | store_true | False |
| `-l, --list` | `list` | store_true | False |
| `-o, --pass` | `global_machine_metadata` | str, nargs `*`, metavar METADATA | None |
| `--xterm` | `xterm` | str | None |
| `--no-hosthome, -H` / `--hosthome` | `hosthome_mount` | store_const False/True | None |
| `--no-shared, -S` / `--shared` | `shared_mount` | store_const False/True | None |
| `--exclude` | `excluded_machines` | nargs `+`, metavar DEVICE_NAME | `[]` |
| `machine_name` (positional) | `machine_name` | nargs `*`, metavar DEVICE_NAME | `[]` |

Behavior: parses argv with its own parser (validation only), then:

1. Builds lclean argv: `['-d', directory]` if given, + positional machine names, + `['--exclude', ...]` if given; runs `LcleanCommand().run(current_path, lclean_argv)`.
2. Runs `LstartCommand().run(current_path, argv)` with the **original raw argv**.

**Latent code bug (port as-is, record in DIVERGENCES.md):** because the raw argv is re-parsed by *lstart's* parser, `kathara lrestart --xterm foo` passes lrestart's parse, performs the lclean, then dies in lstart's parser with `unrecognized arguments: --xterm` → **exit 2 after the lab has already been cleaned**. There is no working way to set a terminal emulator via lrestart (`--terminal-emu` is rejected by lrestart's parser, `--xterm` by lstart's). Similarly `--print` is rejected up front by lrestart's parser (documented: "lack of some options (e.g. --print)").

Output: lclean's output (panel `Stopping Network Scenario`, delete progress bars) followed by the full lstart output. Exit: 0; 1 exceptions; 2 argparse (either parser).

Doc cross-check: doc documents `--xterm <XTERM>` as functional (M-7); typo "informarion" in `-l` help (doc only).

---

## 4. `kathara linfo` — flags: 6 args (all options); 2 MEGs

Description: `Show information about a Kathara network scenario`.

| Flag(s) | dest | action | default | Help |
|---|---|---|---|---|
| `-h, --help` | — | help | SUPPRESS | "Show a help message and exit." |
| `-d, --directory` | `directory` | str | None | "Specify the folder containing the network scenario." |
| `-w, -l, --watch, --live` | `watch` | store_true | False | MEG-1. "Watch mode, can be used only when a network scenario is launched." |
| `-c, --conf` | `conf` | store_true | False | MEG-1. "Read static information from lab.conf." |
| `-n, --name` | `name` | str, metavar `DEVICE_NAME` | None | MEG-2. "Show only information about a specified device." |
| `-t, --topology` | `topology` | store_true | False | MEG-2. "Get running topology info" (no trailing period — sic) |

Behavior: lab path resolved; custom conf loaded; `LabParser.parse` with fallback to `Lab(None, path=lab_path)` on any exception.

- `-w` (watch): full-screen `rich.live.Live` alternate-screen loop, 1 refresh/sec (initial spinner `Loading...`):
  - with `-n`: repeated single-device panel, title `<name> Information`; body `str(machine_stats)` or red bold `Device \`<name>\` Not Found.` Loops forever (only Ctrl+C exits; exit code 0, no interrupt warning — linfo is exempt).
  - with `-t`: repeated topology table (§13); **breaks out and returns 0** when the table becomes None (lab gone).
  - default: repeated machines-stats table; breaks when stats stream ends.
- `-c` (conf): purely static; with `-n` prints panel `str(lab.machines[name])` titled `<name> Information` (KeyError → exit 1 if device not in lab.conf); without prints (a) optional lab-meta panel titled `Network Scenario Information`, (b) panel titled `Topology Information` with body:
  `There are <N> devices.\nThere are <M> collision domains.` (counts green bold; M excludes the bridge link).
- default (no `-w`/`-c`): status spinner `Loading...`, then:
  - `-n`: one-shot device panel as above (title `<name> Information`, red bold `Device \`<name>\` Not Found.` if absent),
  - `-t`: one-shot topology table,
  - neither: one-shot machines-stats table.

Exit: 0 (including Ctrl+C from watch); 1 exceptions; 2 argparse. NOTE: machine stats content is resource-stats territory (DEFERRED for sampling; inventory fields stay per spec §0.3) and the whole command is deferred per spec §3.4 — surface recorded here for completeness.

Doc cross-check: options match; the doc's field list (LAB_HASH, DEVICE_NAME, STATUS, CPU %, ...) describes the Docker stats table (M-9 applies to `list` too — the columns come from `to_dict()` of the stats object minus `container_name`).

---

## 5. `kathara lconfig` — flags: 5 args (all options); 1 required MEG

Description: `Manage the network interfaces of a running Kathara device in a Kathara network scenario`.

| Flag(s) | dest | type | nargs | required | Help |
|---|---|---|---|---|---|
| `-h, --help` | — | help | | | "Show a help message and exit." |
| `-d, --directory` | `directory` | str, metavar `LAB_PATH` | | no | "Path of the network scenario to configure, if not specified the current path is used" (no trailing period — sic) |
| `-n, --name` | `name` | str, metavar `DEVICE_NAME` | | **yes** | "Name of the device to configure." |
| `--add` | `to_add` | `cd_mac`, metavar `CD/MAC` | `+` | MEG (req) | "Specify the collision domain to add." |
| `--rm` | `to_remove` | `alphanumeric`, metavar `CD` | `+` | MEG (req) | "Specify the collision domain to remove." |

The `--add`/`--rm` MEG is `required=True`: omitting both → argparse error `one of the arguments --add --rm is required`, exit 2.

Behavior: lab path resolved; custom conf loaded; `LabParser.parse` (NO fallback here — a broken/missing lab.conf raises → exit 1). `update_lab_from_api(lab)`. `lab.get_machine(name)` (`MachineNotFoundError` → exit 1). Panel: `Updating Network Scenario Device \`<name>\`` (blue bold, centered). Then per item:

- add: green line `+ Adding interface to device \`<name>\` on collision domain \`<cd>\`` + (` with MAC Address <mac>` if given) + `...`; `connect_machine_to_link`.
- rm: red line `- Removing interface on collision domain \`<cd>\` from device \`<name>\`...`; `disconnect_machine_from_link`.

Exit: 0; 1 exceptions (including SyntaxError from a malformed `CD/MAC` — see §0.6); 2 argparse.

Doc cross-check: matches (doc `-n` help "Name of the device to configure." matches code).

---

## 6. `kathara vstart` — flags: 24 args (23 options + 1 positional); 2 MEGs — spec §3.4 flag-identical reimplementation required

Description: `Start a new Kathara device`.

| Flag(s) | dest | action/type | nargs | metavar | default | required | Help |
|---|---|---|---|---|---|---|---|
| `-h, --help` | — | help | | | SUPPRESS | | "Show a help message and exit." |
| `--noterminals` | `terminals` | store_const `False` | | | `None` | | MEG-1. "Start the device without opening a terminal window." |
| `--terminals` | `terminals` | store_const `True` | | | (shared) | | MEG-1. "Start the device opening its terminal window." |
| `--num_terms` | `num_terms` | str | | `NUM_TERMS` | `None` | no | MEG-1. "Choose the number of terminals to open for the device." (string, validated later by Machine meta → `MachineOptionError` on non-int) |
| `--privileged` | `privileged` | store_const `True` | | | `None` | no | "Start the device in privileged mode. MUST BE ROOT FOR THIS OPTION." |
| `-n, --name` | `name` | str | | `DEVICE_NAME` | — | **yes** | "Name of the device to be started." |
| `--eth` | `eths` | type=`interface_cd_mac` | `+` | `N:CD/MAC` | `None` | no | "Set a specific interface on a collision domain." |
| `-e, --exec` | `exec_commands` | str | `*` | | `None` | no | "Execute a specific command in the device during startup." |
| `--mem` | `mem` | str | | | `None` | no | "Limit the amount of RAM available for this device." |
| `--cpus` | `cpus` | str | | | `None` | no | "Limit the amount of CPU available for this device." |
| `-i, --image` | `image` | str | | | `None` | no | "Run this device with a specific Docker Image." |
| `--no-hosthome, -H` | `hosthome_mount` | store_const `False` | | | `None` | | MEG-2. "Do not mount \"/hosthome\" directory inside the device." |
| `--hosthome` | `hosthome_mount` | store_const `True` | | | `None` | | MEG-2. "Mount \"/hosthome\" directory inside the device." |
| `--terminal-emu` | `terminal_emu` | str | | | `None` | no | "Set a different terminal emulator application (Unix only)." |
| `--print, --dry-run` | `dry_mode` | store_true | | | `False` | no | "Check if the device parameters are correct (dry run)." **Note: alias is `--dry-run` here, `--dry-mode` in lstart** |
| `--bridged` | `bridged` | store_true | | | `False` | no | "Add a bridge interface to the device." |
| `--port` | `ports` | str | `+` | `[HOST:]GUEST[/PROTOCOL]` | `None` | no | "Map localhost port HOST to the internal port GUEST of the device for the specified PROTOCOL." |
| `--sysctl` | `sysctls` | str | `+` | `SYSCTL` | `None` | no | "Set sysctl option for the device." |
| `--env` | `envs` | str | `+` | `ENV` | `None` | no | "Set environment variable for the device." |
| `--ulimit` | `ulimits` | str | `+` | `KEY=SOFT[:HARD]` | `None` | no | "Set ulimit for the device." |
| `--volume` | `volumes` | type=`volume` | `+` | `HOST\|GUEST\|[MODE]` | `None` | no | "Specify a volume to mount." |
| `--shell` | `shell` | str | | | `None` | no | "Set the shell (sh, bash, etc.) that should be used inside the device." |
| `--entrypoint` | `entrypoint` | str | | `ENTRYPOINT` | `None` | no | "Specify the entrypoint command of the device." |
| `args` (positional) | `args` | `nargs=argparse.REMAINDER` | REMAINDER | `ARG` | `[]` | | "Specify extra arguments for the entrypoint command." |

MEG-1 = `--noterminals | --terminals | --num_terms` (three-way). MEG-2 = hosthome pair.

Runtime behavior:

1. Panel: `Starting Device \`<name>\`` — or `Checking Device \`<name>\`` in dry mode (blue bold, centered).
2. Dry mode: prints green `✓ <name> configuration is correct.` and returns **0**. **Note: dry-run exits *before* any semantic validation** — it only proves argparse accepted the flags (the panel/checkmark still print). `--eth 0:A --print` never touches Docker.
3. Setting overrides: `open_terminals` (if terminals not None), `terminal` (`--terminal-emu`), `device_shell` (`--shell`).
4. `Lab("kathara_vlab")` — the fixed single-device lab namespace shared by all v-commands; options `hosthome_mount=<arg>`, `shared_mount=False` (shared is force-disabled for vstart).
5. `--privileged` non-root → `PrivilegeError("You must be root in order to start this Kathara device in privileged mode.")`; root+terminals → yellow `⚠ Running devices with privileged capabilities, terminal might not open!` (singular "terminal", differs from lstart's plural).
6. If REMAINDER args begin with `--`, that first token is stripped.
7. `volumes` and `eths` popped from args; **everything else left in the dict is passed as `**args` to `lab.get_or_new_machine(name, **args)`** and becomes machine meta (`num_terms`, `exec_commands`, `mem`, `cpus`, `image`, `bridged`, `ports`, `sysctls`, `envs`, `ulimits`, `shell`, `entrypoint`, plus leftover keys `terminals`, `hosthome_mount`, `terminal_emu`, `dry_mode`, `privileged`, `args` — Machine.add_meta ignores what it doesn't know, but the Go sugar reimplementation must replicate the *effective* meta set). Meta validation errors (bad mem/cpus/port/sysctl/ulimit formats) raise `MachineOptionError` → exit 1.
8. Each `--eth N:CD[/MAC]` → `lab.connect_machine_to_link(name, cd, machine_iface_number=int(N), mac_address=mac)`; non-numeric N → `SyntaxError("Interface number in \`--eth <n>:<cd>[/<mac>]\` is not a number.")` → exit 1. Non-sequential iface numbers raise `NonSequentialMachineInterfaceError` at deploy (exit 1).
9. Each `--volume` string appended as machine meta `volume`.
10. `deploy_lab(lab)` — same progress bars/prompts as lstart.

Exit: 0; 1; 2 as usual.

Doc cross-check (`kathara-vstart.1.ronn`): doc says alias `--dry-mode` but code implements `--dry-run` (M-1); doc omits `--volume`, `--entrypoint` and the trailing `ARG ...` REMAINDER entirely (M-2); doc `--noterminals`/`--hosthome` speak of "devices" plural in places where code help is singular (cosmetic).

---

## 7. `kathara vclean` — flags: 2 args (all options)

Description: `Stop a single Kathara device`.

| Flag(s) | dest | required | metavar | Help |
|---|---|---|---|---|
| `-h, --help` | — | | | "Show a help message and exit." |
| `-n, --name` | `name` | **yes** | `DEVICE_NAME` | "The name of the device to clean." |

Behavior: `Lab("kathara_vlab")`; panel `Stopping Device \`<name>\`` (blue bold, centered); `undeploy_lab(lab_name="kathara_vlab", selected_machines={name})`. Undeploy progress bars. A non-existent name is a silent no-op success (undeploy of empty selection) — exit 0.

Doc cross-check: matches.

---

## 8. `kathara vconfig` — flags: 4 args (all options); 1 required MEG

Description: `Manage the network interfaces of a running Kathara device`.

| Flag(s) | dest | type | nargs | required | Help |
|---|---|---|---|---|---|
| `-h, --help` | — | help | | | "Show a help message and exit." |
| `-n, --name` | `name` | str, metavar `DEVICE_NAME` | | **yes** | "Name of the device to be connected on desired collision domains." |
| `--add` | `to_add` | `cd_mac`, metavar `CD/MAC` | `+` | MEG (req) | "Specify the collision domain to add." |
| `--rm` | `to_remove` | `alphanumeric`, metavar `CD` | `+` | MEG (req) | "Specify the collision domain to remove." |

Behavior: `Lab("kathara_vlab")`; `update_lab_from_api(lab)`; `lab.get_machine(name)` (`MachineNotFoundError` → exit 1) and fetches the machine API object. Panel `Updating Device \`<name>\``. Then identical add/rm console lines as lconfig (§5), except the rm path also fetches the link API object first (`get_link_api_object`; `LinkNotFoundError` → exit 1). Exit: 0/1/2.

Doc cross-check: doc `-n` help is "Name of the device to manage." vs code "Name of the device to be connected on desired collision domains." (cosmetic); doc title says "Attach network interfaces..." vs description string "Manage the network interfaces..." (cosmetic).

---

## 9. `kathara connect` — flags: 6 args (5 options + 1 positional); 1 MEG

Description: `Connect to a Kathara device`.

| Flag(s) | dest | action | Help |
|---|---|---|---|
| `-h, --help` | — | help | "Show a help message and exit." |
| `-d, --directory` | `directory` | str | MEG-1. "Specify the folder containing the network scenario." |
| `-v, --vmachine` | `vmachine` | store_true | MEG-1. "The device has been started with vstart command." |
| `--shell` | `shell` | str | "Shell that should be used inside the device." |
| `-l, --logs` | `logs` | store_true | "Print device startup logs before launching the shell." |
| `machine_name` (positional) | `machine_name` | str, metavar `DEVICE_NAME`, single | "Name of the device to connect to." |

Behavior: `-v` → `Lab("kathara_vlab")`, else lab path resolution + custom conf + `LabParser.parse` with `Lab(None, path=...)` fallback. `logging.debug` line with the hash. `connect_tty(machine_name, lab_hash, shell, logs)`: attaches an interactive TTY (raw stdin/stdout pass-through; terminal integration is spec §3.3 rebuild territory). With `-l`, startup log content is printed first; if the machine is still starting, the "waiting" flow of §14 can appear. `MachineNotRunningError`/`MachineNotFoundError` → exit 1. Exit code 0 on clean detach (the remote shell's exit status is NOT propagated).

Doc cross-check: doc names the MEG flag **`-v, --vdevice`** but code is `-v, --vmachine` (M-3); doc documents **`--command <SHELL>`** where code implements `--shell` (M-4).

---

## 10. `kathara exec` — flags: 8 args (6 options + 2 positionals); 1 MEG

Description: `Execute a command in a Kathara device`.

| Flag(s) | dest | action | nargs | default | Help |
|---|---|---|---|---|---|
| `-h, --help` | — | help | | SUPPRESS | "Show a help message and exit." |
| `-d, --directory` | `directory` | str | | None | MEG-1. "Specify the folder containing the network scenario." |
| `-v, --vmachine` | `vmachine` | store_true | | False | MEG-1. "The device has been started with vstart command." |
| `--no-stdout` | `no_stdout` | store_true | | False | "Disable stdout of the executed command." |
| `--no-stderr` | `no_stderr` | store_true | | False | "Disable stderr of the executed command." |
| `--wait` | `wait` | store_true | | `False` (explicit) | "Wait until startup commands execution finishes." |
| `machine_name` (positional) | `machine_name` | str, metavar `DEVICE_NAME`, single | | | "Name of the device to execute the command into." |
| `command` (positional) | `command` | str, metavar `COMMAND` | `+` | | "Shell command that will be executed inside the device." |

Behavior: lab resolution identical to connect. Command payload: if more than one COMMAND token, the list is passed; if exactly one, the bare string (`command.pop()`) — the manager shlex-splits strings. Streams the remote process: stdout chunks decoded via `chardet` and written to **sys.stdout**, stderr chunks to **sys.stderr** (each suppressible via the flags). No panels, no progress bars, no trailing newline added.

**Exit code = the remote command's exit code** (`exec_output.exit_code()`, e.g. `docker exec_inspect ExitCode`) — the only command whose success exit is not necessarily 0. `--wait` blocks until startup scripts finish (interactive override: `Waiting startup commands execution. Press [ENTER] to override...` — see §14). Errors (device not found/running) → exit 1; argparse → 2. Ctrl+C: exempt from the interrupt warning; exit 0.

Doc cross-check: matches (`-- ` separator shown in examples is plain argparse behavior).

---

## 11. `kathara list` — flags: 4 args (all options)

Description: `Show all running Kathara devices of the current user`.

| Flag(s) | dest | action | Help |
|---|---|---|---|
| `-h, --help` | — | help | "Show a help message and exit." |
| `-a, --all` | `all` | store_true | "Show all running Kathara devices of all users. MUST BE ROOT FOR THIS OPTION." |
| `-w, -l, --watch, --live` | `watch` | store_true | "Watch mode." |
| `-n, --name` | `name` | str, metavar `DEVICE_NAME` | "Show only information about a specified device." |

Note: NO mutually-exclusive groups here (unlike linfo, `-w` and `-n` combine freely).

Behavior: `-a` without root → `PrivilegeError("You must be root in order to show all Kathara devices of all users.")` → exit 1. Watch mode: same Live alternate-screen loop as linfo (spinner, then table each second, breaks when stream ends). One-shot: status spinner `Loading...` then machines-stats table (§13). Exit 0 (Ctrl+C exempt from warning); 1; 2.

Doc cross-check: matches (doc adds field-list prose).

---

## 12. `kathara wipe` — flags: 4 args (all options); 1 MEG. `kathara check` — 1 arg. `kathara settings` — 0 args

### wipe

Description: `Delete all Kathara devices and collision domains, optionally also delete settings`.

| Flag(s) | dest | action | Help |
|---|---|---|---|
| `-h, --help` | — | help | "Show a help message and exit." |
| `-f, --force` | `force` | store_true | "Force the wipe." |
| `-s, --settings` | `settings` | store_true | MEG-1. "Wipe the stored settings of the current user." |
| `-a, --all` | `all` | store_true | MEG-1. "Wipe all Kathara devices and collision domains of all users. MUST BE ROOT FOR THIS OPTION." |

Behavior: without `-f`, confirmation prompt (exact): **`Are you sure to wipe Kathara?`** rendered `Are you sure to wipe Kathara? [y/n]: `; answer no → `sys.exit()` → **exit 0**, nothing done. `-s`: `Setting.wipe_from_disk()` (settings file deleted; recreated with defaults on next run). Otherwise: `-a` without root → `PrivilegeError("You must be root in order to wipe all Kathara devices of all users.")` exit 1; `wipe(all_users=bool(all))` with undeploy progress bars (`Deleting devices` / `Deleting collision domains`). Exit 0/1/2.

Doc cross-check: matches.

### check

Description: `Check your system environment`. Only `-h/--help`.

Output (all stdout, in order): panel `System Check` (blue bold, centered), then lines (values in bold dark_orange3, tab-aligned):

```
Current Manager is:		<manager name>
Manager version is:		<docker/k8s version>
Python version is:		<sys.version, newlines replaced by "- ">
Kathara version is:		3.8.3
Operating System version is:	<uname sysname-release-machine on Linux / platform.platform() elsewhere>
```

then status spinner `Trying to run container with \`<default image>\` image...` while it deploys+undeploys a test lab `kathara_test` with one machine `hello_world` (terminals off, hosthome off). Success: green bold `✓ Container run successfully.` → exit **0**. Failure: red bold `✗ Running container failed: <exception str>` (the glyph is `×` ×) → return **1**.

Doc cross-check: matches.

### settings

Description: `Show and edit Kathara settings`. **`SettingsCommand` defines no parser and never parses argv** — `run()` immediately builds the interactive consolemenu UI (`SettingsMenuFactory` → trdparty/consolemenu, curses-style full-screen menu; Common options + Docker/Kubernetes-specific submenus). Consequences: `kathara settings -h` ignores the `-h` and opens the menu (doc claims `[-h]` works — M-5); no argparse exit-2 path exists; Ctrl+C in the menu → exit 0 without warning (exempt list). The menu implementation is **DEFERRED/REPLACED** scope (spec §3.2 rebuild: `kathara config get/set/list/reset` + bubbletea form; keep the `settings` command name). The stable v3.8.3 surface to preserve: command name, exit 0 on completion, settings persisted to the same `kathara.conf` path/schema.

---

## 13. Shared stdout formats (tables/panels) — golden-test reference

- **Panels** (`create_panel`): rich `Panel`, `box=SQUARE`, `title_align='center'`; style/justify as noted per command.
- **Machines-stats table** (`create_lab_table`): title `TIMESTAMP: <datetime.now()>`, `box=SQUARE_DOUBLE_HEAD`, `show_lines=True`, `expand=True`; columns = keys of `IMachineStats.to_dict()` minus `container_name`, upper-cased with `_`→space (Docker: `LAB HASH`, `MACHINE NAME`, `STATUS`, `CPU %`, `MEM USAGE / LIMIT`, `MEM %`, `NET I/O`), header style dark_orange3; one row per device, all values `str()`. Empty result → centered italic timestamp line + DOUBLE-box red bold panel `No Devices Found`. Stream ended (StopIteration on first next) → `None` (watch loops break; one-shot prints nothing).
- **Topology table** (`create_topology_table`): same box/title; columns `LINK NAME`, `DEVICES`; rows sorted by link name, devices comma-joined. Empty → red bold panel `No Collision Domains Found`.
- **Status spinners**: rich `console.status(..., spinner='dots')`; text `Loading...` (linfo/list/lstart -l) or the check-command text above. Rendered to stdout, erased on completion.

## 14. Deploy/undeploy event UI (fires inside lstart/lrestart/vstart/lclean/vclean/wipe/check)

Registered in `cli/ui/event/register.py`; all render on stdout:

- **Progress bars** (`HandleProgressBar`): description `[<message>]` bold, spinner, bar, `M/N` complete column; messages exactly: `Deploying collision domains`, `Deploying devices`, `Deleting collision domains`, `Deleting devices`.
- **Docker pull bar** (`HandleDockerImagePull`): per-layer tasks `[Downloading <id>]` / `[Download Complete <id>]` with percentage + time-remaining columns.
- **Image update prompt** (`UpdateDockerImage`, fires when `image_update_policy == 'Prompt'`; `Always` pulls silently): exact text
  `A new version of image \`<image>\` has been found on Docker Hub. Do you want to pull it?` `[y/n]: ` — yes pulls, no continues with local image.
- **Volume mount prompt** (`MountDevicesVolumes`, fires when devices declare volumes): prints `The following devices have volumes configured:` then per device a tree `* Device \`<name>\`` with branches `Host Path: <host> -> Device Path: <guest>`; if `volume_mount_policy == 'Prompt'`: `Continue with volume mounting? [y/n]: ` — no sets lab option `_mount_volumes=False` (deploy proceeds without volumes).
- **Machine terminal handler** (`HandleMachineTerminal`): after each deploy, opens `get_num_terms()` terminal windows when `open_terminals` (spawns `<kathara> connect [-v] -l <name>` in the configured emulator/TMUX — spec §3.3 rebuild). `machine_startup_wait_started` → writes to stdout: `Waiting startup commands execution. Press [ENTER] to override...`; `machine_startup_wait_ended` → clears screen (`\033[2J\033[0;0H`, `cls` on Windows).
- Missing Docker image confirmation: pulls happen implicitly on deploy (`docker_pull_started` events); no prompt for a *missing* image, only for updates.

## 15. Exit-code summary

| Code | Producer |
|---|---|
| 0 | Successful command; `-v`; dry runs; Ctrl+C (always, warning printed unless exec/linfo/list/settings); wipe declined at prompt |
| 1 | No/invalid top-level command (help printed); settings check failure; unrecognized command; ImportError; any exception during `run` (logged CRITICAL, or traceback when debug_level=EXCEPTION); `check` container test failure; Python < 3.9 |
| 2 | argparse error in any sub-command parser (usage + message on stderr) |
| N | `exec` only: the remote command's exit code |

---

## 16. Doc-vs-code mismatches (docs/*.ronn vs cli/command/*.py)

| # | Where | Doc says | Code does |
|---|---|---|---|
| M-1 | kathara-vstart.1 §OPTIONS | `--print`, `--dry-mode` | vstart implements `--print, --dry-run` (`--dry-mode` is lstart-only; `kathara vstart --dry-mode` → exit 2) |
| M-2 | kathara-vstart.1 SYNOPSIS/OPTIONS | no mention | code also has `--volume HOST\|GUEST\|[MODE]`, `--entrypoint ENTRYPOINT`, and positional REMAINDER `ARG ...` — all undocumented |
| M-3 | kathara-connect.1 | `-v, --vdevice` | code flag is `-v, --vmachine` (`--vdevice` → exit 2) |
| M-4 | kathara-connect.1 | `--command <SHELL>` | code flag is `--shell` (`--command` → exit 2) |
| M-5 | kathara-settings.1 SYNOPSIS | `kathara settings [-h]` | SettingsCommand has no parser; `-h` is silently ignored and the menu opens |
| M-6 | kathara.1 §DESCRIPTION | "global commands (connect, info, wipe, settings, check)" | there is no `info` command (they are `linfo` and `list`); `list` also missing from that sentence |
| M-7 | kathara-lrestart.1 | `--xterm <XTERM>` documented as working option | accepted by lrestart's parser but the raw argv is re-parsed by lstart, which rejects `--xterm` → exit 2 **after** the lclean already ran (code bug; doc presents it as functional) |
| M-8 | kathara-lstart.1 / kathara-lrestart.1 | `-o/--pass` metavar `OPTION`, "Apply options to all devices" | code metavar `METADATA`, help "Apply metadata to all devices..." (cosmetic) |
| M-9 | kathara-vconfig.1 | title "Attach network interfaces to a running device"; `-n` "Name of the device to manage." | description string "Manage the network interfaces..."; `-n` help "Name of the device to be connected on desired collision domains." (cosmetic) |
| M-10 | kathara-vstart.1 | `--sysctl`: "Only the `net.` namespace is allowed to be set." | CLI help omits the restriction (it is enforced later in Machine meta validation, `MachineOptionError`) — doc-only detail, not a flag mismatch |

Cosmetic wording drift not listed row-by-row: singular/plural "device(s)" in vstart hosthome/terminals help, doc typo "informarion" (lrestart `-l`), doc typo "conjuction" (connect/exec).

## 17. Per-command flag counts (add_argument calls, including `-h` and positionals)

| Command | Total args | Options | Positionals | MEGs (required) |
|---|---|---|---|---|
| kathara (global) | 3 | 2 | 1 | 0 |
| lstart | 16 | 15 | 1 | 3 (0) |
| lclean | 4 | 3 | 1 | 0 |
| lrestart | 15 | 14 | 1 | 3 (0) |
| linfo | 6 | 6 | 0 | 2 (0) |
| lconfig | 5 | 5 | 0 | 1 (1) |
| vstart | 24 | 23 | 1 | 2 (0) |
| vclean | 2 | 2 | 0 | 0 |
| vconfig | 4 | 4 | 0 | 1 (1) |
| connect | 6 | 5 | 1 | 1 (0) |
| exec | 8 | 6 | 2 | 1 (0) |
| list | 4 | 4 | 0 | 0 |
| wipe | 4 | 4 | 0 | 1 (0) |
| check | 1 | 1 | 0 | 0 |
| settings | 0 | 0 | 0 | — (no parser) |
