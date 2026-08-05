# Layer B — parser conformance vectors

`PORT_SPEC.md` §9 Layer B. A table of *(input files → expected model JSON | expected error)*
covering `lab.conf`, `lab.dep`, the folder-layout fallback and the CLI `-o` option parser.

**These vectors are shared by the Go `labfile` package and the Python client's `LabParser`.
They are what keeps the two implementations from drifting.** Neither implementation gets to
"win" an argument with a vector: the vectors record what Kathará 3.8.3's Python parser
actually does, verified by replaying them against the real Python parser.

`ExtParser` (`lab.ext`) is deferred post-1.0 per spec §0.3 and has no vectors here.

---

## Running the corpus

The authority runner loads every vector, drives the **real Python parser**, serialises the
resulting model into the vector JSON shape and diffs it against `expected.json`:

```
/root/kathara/pyvenv/bin/python tools/vectorcheck/check_python.py
```

| Flag | Effect |
|---|---|
| `--filter SUBSTR` | only run vectors whose id contains `SUBSTR` |
| `--verbose` | print a line per passing vector |
| `--update` | rewrite the failing `expected.json` files from observed Python behaviour |
| `--vectors DIR` | point at a different corpus root |

Exit status is 0 only when every vector passes. **Python is the truth**: if a vector
disagrees with Python, the vector is wrong. Read the diff, decide whether the behaviour is
merely surprising or an actual bug, re-record with `--update`, and add an entry to
[SURPRISES](#surprises) below (and to `DIVERGENCES.md` only if it is a bug the Go port will
deliberately not reproduce).

The Go side replays the same corpus against `labfile`, using the same serialisation.

---

## Layout

One directory per vector. Any directory containing a `vector.json` is a vector; the vector's
**id** is its path relative to this directory (e.g. `labconf/mac_address_error`).

```
labconf/mac_address_error/
├── vector.json      # what to run
├── expected.json    # what must come out
└── input/           # the files handed to the parser
    ├── .gitkeep
    └── lab.conf
```

Categories: `labconf/`, `labdep/`, `labfolder/`, `options/`.

### `vector.json` — the invocation

| Key | Type | Meaning |
|---|---|---|
| `parser` | string | which entry point to drive — see below |
| `description` | string | one line stating what the vector pins |
| `conf_name` | string | *(lab only)* the `conf_name` argument, i.e. `lstart --config`. Default `lab.conf` |
| `options` | list\|null | *(options only)* the argv values passed to `OptionParser.parse` |
| `dirs` | list | directories to create in the materialised lab dir, in listed order |
| `order_sensitive` | bool | default `true`; see [Ordering](#ordering) |
| `notes` | string | optional free text for a reader of the vector |

`parser` values:

| Value | Drives |
|---|---|
| `lab` | `LabParser.parse(path, conf_name)` → `expected.lab` |
| `dep` | `DepParser.parse(path)` → `expected.dep` |
| `folder` | `FolderParser.parse(path)` → `expected.lab` |
| `options` | `OptionParser.parse(options)` → `expected.options` |
| `lab+dep` | the `lstart` sequence: `LabParser.parse`, then `DepParser.parse`, then `Lab.apply_dependencies(deps)` when the dep list is truthy → `expected.lab` **and** `expected.dep` |

### `input/` — the lab directory

The runner materialises a fresh temporary directory per vector: every file in `input/` is
copied into it (except `.gitkeep`), then every path in `vector.json`'s `dirs` is created.
The parser is pointed at that temporary directory, never at the checked-out tree.

`dirs` exists because **git cannot track empty directories** and `FolderParser` vectors are
made of empty machine folders. It is also how nested layouts are expressed
(`"dirs": ["pc1", "pc1/etc"]`).

Input files are byte-exact on purpose: CRLF line endings, a missing final newline, a UTF-8
BOM and deliberately invalid UTF-8 are all part of vectors here. Do not reformat them.

### `expected.json` — the outcome

Exactly one of the success keys or `error` is present, plus an optional `warnings`.

```jsonc
{
  "lab":     { ... },            // parser: lab | folder | lab+dep
  "dep":     ["pc2", "pc1"],     // parser: dep | lab+dep. null = no/empty lab.dep
  "options": {"mem": "64m"},     // parser: options
  "error":   {"class": "SyntaxError", "message": "…"},
  "warnings": ["…"]              // logging.warning calls, in order. Absent means none.
}
```

`error.class` is Python's `type(e).__name__` and `error.message` is `str(e)`, **exact**.
Note `IOError` is an alias of `OSError` in Python 3, so the class is recorded as `OSError`.
Messages routinely contain a trailing newline (the parser interpolates the raw line) — that
is deliberate, not a stray edit.

#### The `lab` shape

```jsonc
{
  "name": null,                  // LAB_NAME, or null
  "hash": null,                  // pinned only when name is set; otherwise path-derived, so null
  "description": null, "version": null, "author": null, "email": null, "web": null,
  "machines": {
    "pc1": {
      "interfaces": {            // keyed by interface number as a string
        "0": {"cd": "A", "mac": null}
      },
      "meta": {
        "exec_commands": [],     // the six structured metas are always present
        "sysctls": {},           // values are int when the literal is numeric, else string
        "envs": {},
        "ports": {},             // key "<host_port>/<protocol>", value guest port (int)
        "ulimits": {},           // {"nofile": {"soft": 1024, "hard": 1024}}
        "volumes": {},           // {"/host/a": {"guest_path": "/guest/a", "mode": "rw"}}
        "extra": {}              // every other meta, in its parsed type
      },
      "has_dir": false,          // Machine.fs is not None, i.e. a same-named folder exists
      "startup_file": null       // "pc1.startup" when that file exists in the lab dir
    }
  },
  "machine_order": ["pc1"],      // Lab.machines insertion order — semantic, see below
  "links": {"A": {"machines": ["pc1"]}},
  "link_order": ["A"],           // Lab.links insertion order — semantic
  "general_options": {},         // never set by any parser; pinned to prove it
  "global_machine_metadata": {}, // ditto
  "has_dependencies": false
}
```

Two shape notes that matter for the Go port:

* **`meta.extra` values keep their parsed Python type.** `privileged` and `bridged` are real
  booleans (they go through `strtobool`); everything else coming out of `lab.conf` is a
  **string**, including `ipv6`, `mem`, `cpus` and `num_terms`. See SURPRISE 7.
* **`ports` keys are a flattened tuple.** Python keys `meta['ports']` by `(host_port, protocol)`;
  the vector serialises that as `"3000/tcp"`.

#### Canonical JSON

`json.dumps(obj, sort_keys=True, indent=2, ensure_ascii=False)` plus a trailing newline.
Sorted keys, two-space indent, literal non-ASCII. Numbers and booleans in their parsed types
— `"privileged": true` is a bool, `"num_terms": "2"` is a string, and the difference is the
whole point. `--update` writes this form; keep hand edits in it.

### Ordering

`machine_order`, `link_order` and each link's `machines` list capture **insertion order**,
which is semantic in Python: it is lab.conf file order (first mention of a name, whether the
first mention is a meta or an interface), and it drives deploy order, `linfo` output and
interface numbering downstream.

`"order_sensitive": false` marks a vector whose order is *unspecified in Python* — today only
the `FolderParser` glob-order cases. The runner sorts those lists on both sides before
comparing, so the vector asserts the device *set* and stays reproducible on any filesystem.
See SURPRISE 21.

Interface numbers are always contiguous `0..N-1` in a successful `lab.conf` parse
(`check_integrity` runs inside `parse`), so `interfaces` needs no separate order array.

---

## Corpus

142 vectors: 71 success, 71 error.

| Category | Vectors | Success | Error | Covers |
|---|---:|---:|---:|---|
| `labconf/` | 97 | 39 | 58 | `LabParser.parse` — quoting, comments, every machine option, MAC syntax, LAB_ metadata, names/charsets, integrity, file-level errors |
| `labdep/` | 26 | 16 | 10 | `DepParser.parse` + `Lab.apply_dependencies` — syntax, cycles, flatten order, absent/empty files |
| `labfolder/` | 9 | 8 | 1 | `FolderParser.parse` — subdirectories as devices, reserved and hidden names, glob order |
| `options/` | 10 | 8 | 2 | `OptionParser.parse` — the CLI `-o/--pass` values |

Adding a vector: create the directory, write `vector.json` and `input/` (plus `input/.gitkeep`),
author `expected.json` **from the Python source by hand**, then run the checker. Authoring
first and letting the runner refute you is the point — five of the entries below were found
exactly that way.

---

## SURPRISES

Python behaviours worth knowing before porting. Each is pinned by at least one vector.
Marked **[bug]** where the behaviour looks unintended — those are `DIVERGENCES.md` candidates,
not automatic divergences: per spec §0.1 the default is to port them as-is.

1. **`int()` accepts PEP 515 underscore separators, so `pc1[0_1]` is interface 1** and
   `pc1[1_0]` is interface 10. The interface-vs-meta dispatch is "does `int(arg)` raise", and
   `\w` admits `_`. Go's `strconv.Atoi` rejects `"0_1"`, so a naive port silently reclassifies
   the line as a *meta*. `_0` and `0_` are malformed PEP 515 and do become metas.
   → `labconf/interface_underscore_digits`, `labconf/meta_named_like_number`

2. **The lab.dep "phantom empty dependency" does not exist.** `parser-settings.md` §1.2 and
   `EXPECTATIONS-core.md` §15/23 both state that `a: b c ` yields an empty-string machine name
   in the flattened output. It cannot: `line.strip()` runs *before* the regex, so a trailing
   space never reaches the deps group. Do not implement the phantom in Go.
   → `labdep/trailing_space_no_phantom`

3. **`bridged_iface` in lab.conf is always fatal.** `EXPECTATIONS-core.md` §15/13 claims it
   "can legally fill the hole" in interface numbering. It cannot. `add_meta` stores it via the
   generic path as a **string**, and `Machine.check()` does
   `sorted_keys.append(self.meta['bridged_iface']); sorted_keys.sort()` — sorting `str` against
   `int` raises `TypeError: '<' not supported between instances of 'str' and 'int'`. With no
   interfaces at all it instead raises `NonSequentialMachineInterfaceError`. There is no
   lab.conf that sets `bridged_iface` and parses. **[bug]**
   → `labconf/bridged_iface_with_interface`, `labconf/bridged_iface_only`

4. **Inner quotes are never "deleted".** `EXPECTATIONS-core.md` §15/2 predicts
   `pc1[image]="ka"tha"ra"` → `kathara`. The `.replace('"','').replace("'",'')` on the matched
   value is dead code — the value class `[^"']+` already excludes quotes — so the line simply
   fails to match and raises. Quote handling is: opening quote optional, closing quote must be
   the *same* character (a regex backreference), nothing in between.
   → `labconf/inner_quotes`, `labconf/unmatched_quotes`, `labconf/unclosed_quotes`

5. **The `ulimit` errors name the meta, not the device.** `MachineOptionError(f"Invalid ulimit
   value (\`{value}\`) on \`{name}\`.")` interpolates `add_meta`'s `name` parameter, which is
   the literal string `"ulimit"`. Every other option error interpolates `self.name`. So the
   message reads ``on `ulimit` `` where the user expects ``on `pc1` ``. **[bug]**
   → `labconf/ulimit_invalid_format`, `labconf/ulimit_below_minus_one`,
   `labconf/ulimit_soft_unlimited_hard_bounded`

6. **The invalid-volume-mode message ends with a trailing space**
   (`"Allowed values are ro, rw, rx. "`). Byte-exact error parity requires keeping it.
   Also note `rx` really is an accepted mode (contra `tests-docs.md`; see SYNTHESIS C-6).
   → `labconf/volume_invalid_mode`, `labconf/meta_all_options`

7. **Only `privileged` and `bridged` become real booleans.** Everything else from lab.conf is
   a string: `pc1[ipv6]=false` stores the string `"false"`, which is **truthy**. The same key
   set via the Python API (`update_meta`) receives an actual `bool`, so the two entry points
   produce different types for the same meta. `mem`, `cpus` and `num_terms` are likewise
   strings out of lab.conf. **[bug]** for `ipv6`.
   → `labconf/meta_all_options`, `labconf/strtobool_spellings`

8. **Trailing comments only work after a *quoted* value.** The `(\s+#.*)?$` group can never
   fire otherwise, because `[^"']+` is greedy and includes `#` and spaces. So
   `pc1[image]=kathara/frr # note` silently stores `kathara/frr # note`, and the same swallow
   on an interface line surfaces as the baffling
   ``Collision domain `A # comment` contains non-alphanumeric characters.`` A comment glued to
   the closing quote (`'A'# c`) is a syntax error. **[bug]**
   → `labconf/comment_swallowed_meta`, `labconf/comment_swallowed_interface_error`,
   `labconf/inline_comment`, `labconf/inline_comment_no_space`

9. **Indented comments: illegal in lab.conf, legal in lab.dep.** lab.conf tests
   `line.startswith('#')` on the **raw** line; lab.dep tests it on the **stripped** line. Two
   files, two conventions. (`lab.ext`, deferred, uses the raw-line convention.)
   → `labconf/indented_comment`, `labdep/indented_comment_ok`

10. **Syntax-error messages embed the raw line including its trailing newline** — and the CR
    on a CRLF file, since mmap over a text-mode fd does no newline translation. The message
    therefore contains an embedded newline before its closing backtick. A Go port that trims
    the line produces different bytes.
    → `labconf/error_line_number`, `labconf/crlf_error_message`, `labconf/unknown_lab_metadata_key`

11. **`LAB_*` values containing `=` crash the parser.** The metadata branch does
    `(key, value) = line.split("=")`, so `LAB_WEB=https://example.org/?a=b` — an entirely
    plausible line — raises an uncaught `ValueError: too many values to unpack (expected 2)`
    with no file, no line number and no mention of lab.conf. **[bug]**
    → `labconf/lab_metadata_with_equals`

12. **A UTF-8 BOM breaks line 1.** `'﻿'.isspace()` is `False`, so `strip()` keeps it, the
    device regex fails, and the file dies with a syntax error whose message contains an
    invisible character. Any lab.conf saved as "UTF-8 with BOM" by a Windows editor is
    rejected. **[bug]**
    → `labconf/bom_file`

13. **Invalid UTF-8 anywhere aborts the parse** with an uncaught `UnicodeDecodeError`, because
    each line is `mmap.readline().decode('utf-8')`. Go strings are bytes; reproducing this
    needs an explicit `utf8.Valid` check per line.
    → `labconf/invalid_utf8`

14. **`strtobool` failures escape with no context.** `pc1[privileged]=maybe` raises a bare
    `ValueError("Invalid truth value \`maybe\`.")` — no file, no line — because it is raised
    from *inside* the `except ValueError:` handler that implements the interface/meta dispatch.
    → `labconf/strtobool_invalid`

15. **`RESERVED_MACHINE_NAMES` (`shared`, `_test`) is enforced two different ways.** LabParser
    raises `ValueError`; FolderParser silently `continue`s. Same list, same intent, opposite
    behaviour.
    → `labconf/reserved_name_shared`, `labconf/reserved_name_test`, `labfolder/ignore_reserved_folders`

16. **Collision-domain names are far more permissive than device names, and are not reserved.**
    Devices are ASCII `^[a-z0-9_]{1,30}$`; collision domains only need Unicode `^\w+$` — so
    `UPPER`, `_leading`, `123` and even `shared` are all legal collision domains.
    `^\w+$` is Unicode-aware, so a fully non-ASCII collision-domain name is legal too — RE2's
    ASCII `\w` would reject the whole line.
    → `labconf/cd_name_charset`, `labconf/cd_named_shared`, `labconf/cd_unicode_name`

17. **Meta names are Unicode `\w`.** `pc1[éth]=value` and `pc1[имя]=другое` are legal metas.
    The *device* name stays ASCII only because `[a-z0-9_]` is a literal character range, not
    because of any explicit ASCII intent.
    → `labconf/meta_unicode_arg`

18. **Unicode digits split three ways.** `pc1[٣]` (Arabic-Indic three) is **interface 3**:
    `\w` matches it and `int()` parses it. `pc1[²]` (superscript two) is a **meta named `²`**:
    `\w` matches and `str.isdigit()` is true, but `int()` rejects it. Go's RE2 `\w` is ASCII, so
    both lines fail the device regex entirely and become syntax errors — OQ-14a, needs a ruling.
    → `labconf/interface_unicode_digit`, `labconf/meta_named_like_number`

19. **`depgen.flatten` is not a canonical topological sort.** Within one dependency level the
    order is lab.dep *line* order: `d: b c` yields `[a, b, c, d]` while the same graph written
    `d: c b` yields `[a, c, b, d]`. Device deploy order under lab.dep therefore depends on how
    the file was typed. OQ-15b: a Go rewrite using "topological sort with cycle detection" must
    reproduce this, not improve on it.
    → `labdep/diamond_order_ab`, `labdep/diamond_order_ba`

20. **A comments-only lab.dep returns `[]`, not `None`,** while a missing or zero-byte lab.dep
    returns `None` (the empty one also logs `lab.dep file is empty. Ignoring...`). Callers test
    truthiness so all three behave alike today, but the Go signature has to be able to express
    the distinction or record it as a divergence.
    → `labdep/comments_only`, `labdep/missing_lab_dep`, `labdep/empty_lab_dep`

21. **`FolderParser` device order is neither sorted nor creation order.** `glob` does not sort;
    the order is `os.scandir` order. On the oracle host, folders created
    `m_delta, m_alpha, m_charlie, m_bravo` came back `['m_bravo', 'm_charlie', 'm_alpha', 'm_delta']`
    (ext4 hashed readdir). This is OQ-15a: unspecified in Python, and it decides machine
    insertion order — hence deploy order — for conf-less labs. The vector is marked
    `"order_sensitive": false` so it asserts the device set only; whichever way the ruling goes,
    the vector stays valid.
    → `labfolder/glob_order`

22. **One badly-named folder kills the whole FolderParser run.** `glob` skips dot-directories,
    but any other directory whose name breaks the device charset — `PC1`, `my-notes`, a 31-char
    name — reaches the `Machine` constructor and raises
    ``SyntaxError: Invalid device name `PC1`.`` So `kathara lstart` on a conf-less directory
    fails outright if the user has a `Docs/` folder next to their devices. **[bug]**
    → `labfolder/invalid_device_name`

23. **`.startup` files have no effect on parsing at all.** No parser reads them; an orphan
    `pc4.startup` does not create a device and a missing `pc1.startup` is not an error. What a
    same-named *directory* does change is `Machine.fs`, which is non-`None` only when the folder
    exists — that is the one filesystem fact the parse result carries.
    → `labconf/machine_dir_and_startup_files`, `labfolder/files_are_not_devices`

24. **The `/`-splitting for MACs drops empty segments, which makes the errors asymmetric.**
    `A/` is `Invalid interface definition` (one part left) but `A//00:…:01` and `A/00:…:01/` are
    both fine (two non-empty parts left). A structurally-invalid MAC that survives the split is
    then rejected much later, by the `Interface` constructor, with a different exception type
    (`InterfaceMacAddressError`) and no line number.
    → `labconf/mac_address_empty_segments`, `labconf/mac_address_trailing_empty_segment`,
    `labconf/mac_address_trailing_slash`, `labconf/mac_address_leading_slash`,
    `labconf/mac_address_error`

25. **Empty values are impossible for machine metas but fine for lab metadata.**
    `pc1[image]=` and `pc1[0]=''` are syntax errors (the value class needs ≥1 character), but
    `LAB_DESCRIPTION=` sets `description` to `""`.
    → `labconf/empty_value`, `labconf/empty_quoted_value`, `labconf/lab_metadata_empty_value`

26. **The interface/meta dispatch is `try: int(arg) / except ValueError:`, which means any
    `ValueError` raised *in the interface branch* would be silently reinterpreted as a meta
    assignment.** Nothing on today's interface path raises `ValueError`
    (`MachineCollisionDomainError`, `InterfaceMacAddressError` and `SyntaxError` all propagate),
    so the hazard is latent — but a Go port must dispatch on `strconv.Atoi`'s result alone and
    must not route "any error" to the meta path.
    → `labconf/duplicate_interface_number`, `labconf/same_collision_domain_error`

27. **`OptionParser`'s error text embeds a CPython runtime message**
    (`not enough values to unpack (expected 2, got 1)` / `too many values to unpack (expected 2)`).
    Go cannot reproduce those strings from its own runtime; only the outer
    `Option parameter not valid: %s.` frame is portable. Treat the inner text as
    Python-implementation detail if the Go messages have to diverge — and record it.
    → `options/no_equals`, `options/two_equals`

28. **Duplicate metas warn and overwrite; duplicate interfaces are fatal.** A repeated
    `(device, meta)` logs `In lab.conf - Line N: Device \`pc1\` already has a value assigned to
    meta \`image\`. …` and last-wins — including for the structured metas, keyed by sub-key
    (`sysctl` per sysctl name, `port` per `(host, proto)`, …). `exec` never warns, because it
    appends. A repeated `machine[N]` interface number raises `MachineCollisionDomainError`.
    → `labconf/duplicate_meta_overwrite`, `labconf/duplicate_typed_meta_warnings`,
    `labconf/exec_order_preserved`, `labconf/duplicate_interface_number`

29. **The sysctl int coercion can crash the parser.** `int(val) if val.lstrip('-').isnumeric() else val`
    strips **all** leading dashes before the numeric test, so `net.a.b=--5` passes `isnumeric()`
    and `int('--5')` raises a bare `ValueError: invalid literal for int() with base 10: '--5'` —
    uncaught, because it fires inside the `except ValueError:` dispatcher (same escape route as
    SURPRISE 14). A single leading `-` is fine and yields a negative int. **[bug]**
    → `labconf/sysctl_double_dash_crash`, `labconf/meta_all_options`

30. **Malformed port values crash the tuple unpack.** Two `/` (`80/tcp/x`) or two `:` (`1:2:3`)
    raise a bare uncaught `ValueError: too many values to unpack (expected 2)` — no file, no
    line, no device name. Only *non-numeric* ports get the wrapped
    `MachineOptionError: Port value not valid …`. **[bug]**
    → `labconf/port_two_slashes`, `labconf/port_two_colons`, `labconf/port_value_invalid`

31. **Interface numbers are unbounded Python ints.** `pc1[99999999999999999999]=A` is an
    *interface* (the dispatch is `int(arg)`, arbitrary precision) and fails only later, in
    `check_integrity`, as ``Interface `0` missing``. Go's `strconv.Atoi` overflows on the same
    arg, and a naive port would misroute the line to the meta path — the overflow twin of the
    PEP 515 hazard in SURPRISE 1.
    → `labconf/interface_number_overflow`

32. **The volume `|` split filters empty segments, like the MAC `/` split (SURPRISE 24).**
    `/a||/b` and `|/a|/b` both survive as two-part volumes with mode `ro`. The sysctl *key*
    regex additionally needs `net.` plus at least **two** more labels: `net.foo=1` is rejected
    with the namespace error even though its namespace is fine.
    → `labconf/volume_empty_segments`, `labconf/sysctl_shallow_key`

### Not surprising, but easy to get wrong

* `IOError` is `OSError` in Python 3 — vectors record the class as `OSError`.
* A missing `lab.conf` and a zero-byte `lab.conf` are two different messages
  (`No lab.conf in given directory.` / `lab.conf file is empty.`), and `conf_name` is
  interpolated into both (`labconf/missing_alt_conf_name`, `labconf/empty_alt_conf_name`).
  A third, rarer file-level error — the file exists but cannot be opened, e.g. it is a
  directory — is `Cannot open lab.conf file.`, and that one hard-codes the default name in
  `lab.dep`'s twin (`labconf/conf_name_is_directory`, `labdep/lab_dep_is_directory`).
* `lab.dep` decodes each line as UTF-8 exactly like `lab.conf`, with the same uncaught
  `UnicodeDecodeError` on invalid bytes (`labdep/invalid_utf8`).
* The device-line arg group is `\w+`, so `pc1[]=A` fails the whole regex and is a raw-line
  syntax error, not an empty-arg meta (`labconf/empty_arg_brackets`).
* The env *value* group is `.*`, so `pc1[env]=VAR=a=b` stores `a=b` — contrast the sysctl
  value class `[^=]*`, which rejects a second `=` (`labconf/env_value_with_equals`).
* A repeated `LAB_*` metadata key silently last-wins — no warning, unlike duplicate machine
  metas (`labconf/lab_metadata_duplicate`).
* `OptionParser` accepts an empty key: `-o =value` yields `{"": "value"}` (`options/empty_key`).
* `check_integrity()` runs *inside* `LabParser.parse`, so a sparse interface list is a
  parse-time error and a successfully-parsed lab.conf can never produce a sparse machine.
  (This contradicts the `PORT_SPEC.md` §4.1 struct comment; see SYNTHESIS C-2.)
* Device names are capped at 30 characters — 30 parses, 31 is a syntax error, and the error is
  the generic "unmatched line" one, not a "bad device name" one. The same is true of uppercase
  and dashed names.
* The `\3` backreference in the device-line regex is not RE2-compatible. Go must hand-roll the
  quote matching; the behaviour to preserve is in vectors 4 and 8 above.
