# Spike: encoding + binary detection parity

Phase 2 dependency spike for PORT_SPEC §12.8 (risk 8) — "Python `str` is Unicode,
Go `string` is bytes. `utils.py` does `unicodedata` normalization, `chardet`
detection and `binaryornot` checks before tarring startup files into containers."

Status: code compiles, passes `gofmt` / `go vet` / `errcheck` / `staticcheck`,
and every behavioural claim below was pinned against the real Kathara 3.8.3 +
binaryornot + chardet stack through `/root/kathara/pyvenv`. Subject to full §10
adversarial review in Phase 3; open items are in §8.

**Headline results**

| | |
|---|---|
| Fixtures | **52** (`internal/util/testdata/encoding/fixtures/`) |
| Go/Python match rate | **52/52 = 100%** on `is_binary`, `is_binary(check_extensions=False)`, read-mode bytes, write-mode bytes, and 23 of 24 feature-vector slots |
| Tests | 9 top-level, **282 PASS assertions counting subtests**, 0 failures |
| Fuzz cross-check | 20 000 generated chunks, 8 byte distributions: **0 verdict mismatches** |
| Cannot be replicated bit-for-bit | exactly one thing — the `byte_entropy` feature, §6 |
| chardet's role in file handling | **none** in the resolved oracle; see §4 |

---

## 1. What landed where

| File | Contents |
|---|---|
| `internal/util/encoding.go` | `ConvertWin2Linux`, `ConvertWin2LinuxInPlace`, `IsBinary`, `IsBinaryString`, `HasBinaryExtension`, `pathlibSuffix`, `computeBinaryFeatures`, the UTF-16/32 and CJK accept predicates |
| `internal/util/encoding_tables.go` | **generated** — 138 binary extensions, 56 magic prefixes, and CPython's exact accept tables for `gb2312`/`big5`/`shift_jis`/`euc-jp`/`euc-kr` |
| `internal/util/encoding_tree.go` | **generated** — binaryornot's trained decision tree, transpiled from its Python AST with thresholds copied verbatim |
| `internal/util/encoding_test.go` | the table test driven entirely by `expected.json` |
| `internal/util/testdata/encoding/fixtures/` | 52 fixture files |
| `internal/util/testdata/encoding/expected.json` | behaviour recorded **BY** the oracle venv (140 KB) |
| `tools/vectorcheck/encoding_probe.py` | Layer B authority runner: owns the corpus definition, regenerates the fixtures, and dumps `expected.json` |
| `tools/vectorcheck/gen_encoding_tables.py` | code generator for the two generated Go files |

Regenerate with:

```
/root/kathara/pyvenv/bin/python tools/vectorcheck/encoding_probe.py [--regen-corpus]
/root/kathara/pyvenv/bin/python tools/vectorcheck/gen_encoding_tables.py
```

**Package placement note.** PACKAGE_GRAPH.md §5's file map for `internal/util`
lists `binary.go` for the sniff; the spike task named `encoding.go`, and the
file holds both the sniff *and* `convert_win_2_linux`, which the map assigns to
`tar.go`'s neighbourhood. `encoding.go` is the honest name for the pair. This is
a filename inside an already-sanctioned package, not a dependency-table change —
PACKAGE_GRAPH §2 row 2 ("tar packing + CRLF/BOM normalization + binary sniff")
is satisfied either way. Flagged as **OQ-E6** so the map can be amended rather
than silently drifted from.

---

## 2. Where these functions sit in the deploy path

```
kathara lstart
  └─ DockerMachine.start / KubernetesMachine.create
      └─ Machine.pack_data()                       model/Machine.py:381
          ├─ copy_fs(self.fs, machine_tar_dir, on_copy=…)      :399
          │     └─ convert_win_2_linux(<tar staging syspath>, write=True)
          │        ← every device file in the lab directory, one call each
          └─ for pc1.startup / pc1.shutdown / shared.startup / shared.shutdown  :410
                └─ convert_win_2_linux(<tar staging syspath>, write=True)
      └─ DockerMachine.copy_files(container, "/", tar_data)    DockerMachine.py:391
         KubernetesMachine.copy_files(...)                     KubernetesMachine.py:922

Kathara.copy_files(machine, guest_to_host)          manager/Kathara.py:312
  └─ DockerManager.copy_files / KubernetesManager.copy_files   :520
      └─ utils.pack_files_for_tar(guest_to_host)    utils.py:449
          └─ utils.pack_file_for_tar(file_obj, arc_name)       utils.py:429
              └─ convert_win_2_linux(file_obj)   ← read mode, host path str only
```

Two call shapes, and they differ in a way that matters:

* **`pack_data` (write mode)** runs against the *staging copy* inside the tar
  builder, whose path preserves the device-relative name — so
  `pc1/etc/frr/frr.conf` keeps `.conf` and `pc1/usr/share/blob.gz` keeps `.gz`.
  Under binaryornot ≥ 0.5 that extension is consulted **before the file is
  opened** (§3), so lab-file *names* now steer normalisation.
* **`pack_file_for_tar` (read mode)** runs against the caller's *host* path.
  Same extension sensitivity, different string.

`convert_win_2_linux` is reached on the `lstart` hot path for every file in
every device directory. A wrong verdict does not fail loudly — it ships a
CRLF-terminated `.startup` into the container, where `/bin/sh` chokes on the
trailing `\r`, or (the other direction) rewrites a binary blob.

---

## 3. The version trap: binaryornot 0.4.4 vs 0.6.0

**This is the most consequential finding of the spike.**

Kathara 3.8.3 declares `binaryornot>=0.4.4` in both `setup.py` and
`pyproject.toml`. Only the dev-only `src/requirements.txt` pins `==0.4.4`. A
`pip install kathara==3.8.3` today therefore resolves **binaryornot 0.6.0**, and
that is what the oracle venv has. The two releases share a function name and
nothing else:

| | 0.4.4 | 0.6.0 (the oracle) |
|---|---|---|
| chunk read | first **1024** bytes | first **512** bytes |
| extension check | hardcoded `filename.endswith('.pyc')` | 138-entry CSV table, consulted **before opening the file** |
| magic prefixes | none | 56-entry CSV table, checked before the classifier |
| classifier | translate-table ratios: binary if (`nontext>0.3` and `high<0.05`) or (`nontext>0.8` and `high>0.8`), then a NUL/`\xff` fallback | a **trained decision tree** over 24 byte-statistics features |
| chardet | **required** — the verdict flips on `chardet.detect(...)['confidence'] > 0.9` | **zero dependencies**, chardet never touched |

Measured over the 52-fixture corpus (`--legacy` mode of the probe records both):
**17 of 52 fixtures = 33% get a different verdict.** Both directions occur.

| fixture | 0.6.0 | 0.4.4 | why |
|---|---|---|---|
| `text_content.{png,zip,gz,bin}`, `PNG_UPPERCASE.PNG`, `crlf_content.tar`, `empty.bin` | binary | text | 0.6.0's extension table |
| `bmw_prose.txt` (`"BMW cars are…"`), `mz_prose.txt` | binary | text | 2-byte magics `BM` (bmp) and `MZ` (dos exe) |
| `cp1252_curly.txt`, `shift_jis.txt` | binary | text | 0.4.4 let chardet's high-confidence guess rescue them |
| `boundary_512.txt`, `boundary_1024.txt`, `nul_text.txt`, `single_nul.dat` | text | binary | 0.4.4's NUL fallback, and its wider 1024-byte window |
| `utf16le_nobom.txt`, `utf16be_nobom.txt` | text | binary | 0.4.4's NUL fallback |

The `crlf_content.tar` row is the one to worry about in production: a lab file
literally named `*.tar`, `*.gz`, `*.zip`, `*.bin`, `*.db`, `*.iso`, `*.pdf`,
`*.so`, `*.class` … now skips normalisation on name alone, and a lab file named
`x.png` that happens to contain text does too. Conversely `.pyc` is the *only*
extension 0.4.4 honoured.

**Ported decision:** the Go port reproduces **0.6.0**, because that is what the
oracle resolves, what a user installing Kathara today gets, and — decisively —
because 0.4.4 is *unportable* (§4). `encoding_test.go` asserts
`expected.json`'s recorded `binaryornot` version equals `0.6.0`, so a venv
rebuild that moves the oracle fails loudly instead of silently re-baselining.
See **OQ-E1**.

---

## 4. chardet's actual role

`grep -rn chardet src/` gives five call sites, all in the same shape and none of
them in file handling:

| site | what it decodes |
|---|---|
| `manager/docker/DockerMachine.py:713` | `cat /var/log/{shared,startup}.log /var/kathara/*` output printed after startup |
| `manager/docker/DockerMachine.py:884` | exec stdout, only to regex out an OCI-runtime "binary not found" message |
| `manager/kubernetes/KubernetesMachine.py:739` | the same startup-log dump on k8s |
| `cli/command/ExecCommand.py:106` | `kathara exec` stdout chunks |
| `cli/command/ExecCommand.py:110` | `kathara exec` stderr chunks |

Every one is `chardet.detect(b)` immediately followed by
`b.decode(result['encoding'])` on bytes coming off a container exec stream.
**chardet is never used for lab files, tar packing, or `convert_win_2_linux`.**
root-utils.md's mapping of "`binaryornot`, `chardet` → `saintfish/chardet`,
`golang.org/x/text`" (PACKAGE_GRAPH line 398) overstates the need.

Two consequences for the 1.0 port:

1. **No 1.0 path needs a chardet-equivalent guess** *as long as the port targets
   binaryornot 0.6.0*, which has zero dependencies. The only place chardet's
   guess ever fed a *decision* (as opposed to a display decode) was inside
   binaryornot **0.4.4**'s `is_binary_string`, and that release is not the
   target. This is why 0.4.4 is unportable: its verdict is a function of
   Mozilla-universalchardet confidence scores, and `saintfish/chardet` is a port
   of *ICU*'s detector with a different scoring model entirely. There is no Go
   library that reproduces it, and no amount of test-fitting would make one.
2. The exec-stream decode remains, and it is **not** part of this spike — it
   belongs to the `kathara exec` / startup-log output path. Recorded findings
   for whoever ports it:
   * `chardet.detect(b'\x80\x81\x82')` returns `{'encoding': None, …}`, and
     `bytes.decode(None)` raises `TypeError: decode() argument 'encoding' must
     be str, not None`. Undecodable container output **crashes `kathara exec`**.
     Every call site is guarded only against *empty* output, not against a
     `None` encoding. This is a live Python bug → DIVERGENCES.md candidate.
   * Detection runs **per chunk**, so a multi-chunk stream can switch encodings
     mid-output; there is no incremental detector state.
   * The pragmatic Go equivalent is `utf8.Valid` → pass through, else latin-1
     widen (never fails, never crashes), which is a *deliberate* behaviour
     change and needs a ruling. Tracked as **OQ-E5**; out of scope here.

---

## 5. `convert_win_2_linux`: what the code actually does

```python
def convert_win_2_linux(filename: str, write: bool = False) -> Optional[bytes]:
    if not is_binary(filename):
        file_obj = None
        try:
            file_obj = open(filename, mode='r', encoding='utf-8-sig')
            file_content = file_obj.read().replace("\n\r", "\n").replace("\r\n", "\n")
            if not write:
                return file_content.encode('utf-8')
            else:
                with open(filename, mode='w', encoding='utf-8', newline="\n") as w:
                    w.write(file_content)
                return
        except Exception:
            if file_obj:
                file_obj.close()
            pass
    if not write:
        return open(filename, mode='rb').read()
```

### 5.1 The `.replace(...)` chain is dead code — and root-utils.md line 475 is wrong

`open(..., mode='r')` defaults to `newline=None`, i.e. **universal newlines**.
The `TextIOWrapper` has already turned every `\r\n` *and every lone `\r`* into
`\n` before `.read()` returns. No `\r` can survive into the string, so neither
`replace("\n\r", "\n")` nor `replace("\r\n", "\n")` can ever match.

root-utils.md §"10. `convert_win_2_linux` normalization details" states "lone
`\r` **untouched**". Verified against the oracle — it is not:

| fixture | input | Python output |
|---|---|---|
| `cr_only.txt` | `line1\rline2\rline3\r` | `line1\nline2\nline3\n` |
| `lone_cr.txt` | `before\rafter…` | `before\nafter…` |
| `nl_cr.txt` | `a\n\rb\n\r\nc\n` | `a\n\nb\n\nc\n` |

Note `nl_cr.txt` especially: had the replace chain been live, `\n\r` → `\n`
would have *deleted* a line break. Universal newlines turns it into `\n\n`
instead. The Go port implements universal-newline translation only;
`TestReplaceChainIsDeadCode` re-applies the literal Python replace chain to
Python's own recorded output for every decoded fixture and asserts it is a
no-op, so if this analysis is ever wrong the test says so.

**Action: root-utils.md line 475 needs correcting.** Recorded as **OQ-E7**; the
Go behaviour is already the oracle-verified one.

### 5.2 Everything else the fixtures pin

* `utf-8-sig` strips **exactly one** leading BOM; a second consecutive BOM and
  any interior U+FEFF survive (`utf8_bom_only.txt` → empty output).
* A file that is not valid UTF-8 is **not** an error — the blanket
  `except Exception` sends it to the verbatim `rb` read. latin-1, cp1252,
  UTF-16 (both endians, BOM or not) and UTF-32 all come back byte-identical.
* NUL bytes do **not** stop the text path: Python text mode is happy with them,
  so `nul_text.txt` and `boundary_*.txt` are decoded and normalised.
* Write mode returns `None` on *every* path, and is a **silent no-op** whenever
  the file is binary or undecodable (12 of 52 fixtures are rewritten; the other
  40 are left byte-identical).
* The write itself is inside the `try`, so a failed write — read-only file, full
  disk — is swallowed too, and `pack_data` proceeds to tar the un-normalised
  file with no diagnostic. Reproduced; see **OQ-E4**.
* NILABILITY.tsv's row holds: read mode always yields bytes, and `b""` is a
  legitimate result. Go returns a non-nil empty slice
  (`TestEmptyFileIsNotNil`).

### 5.3 `pathlib.Path.suffix` ≠ `filepath.Ext`

`has_binary_extension` uses `Path(filename).suffix.lower().lstrip(".")`.
pathlib returns `""` for `.bashrc` and for `dump.`; `filepath.Ext` returns
`.bashrc` and `.`. Ported as `pathlibSuffix`, which reproduces pathlib's
`0 < i < len(name)-1` rule. Without it every dotfile in a lab directory whose
name matched an extension (`.gz`, `.db`, `.o`, `.z`) would have been
misclassified. Pinned by `TestPathlibSuffix`.

---

## 6. Impossible parity: `byte_entropy`

One thing, and only one, cannot be reproduced bit for bit.

binaryornot 0.6.0's feature 7 is Shannon entropy over the byte histogram,
accumulated as `entropy -= p * math.log2(p)`. CPython's `math.log2` delegates to
the platform C library. Measured on this container's glibc, over all 131 328
`count/len` ratios reachable from a ≤512-byte chunk:

* glibc's `log2` differs from the correctly-rounded value on **257** of them
  (0.20%) — so the *Python* side is not stable across hosts either. A musl or
  macOS oracle would produce different bits.
* Go's `math.Log2` is `Log(frac)*(1/Ln2) + exp`, which differs from glibc on
  **26 605** of them (20.3%) by up to 2 ulp. `Log(p)*(1/Ln2)` (28.0%) and
  `Log(p)/Ln2` (31.5%) are both worse; no stdlib spelling matches.

Matching would require transliterating glibc's table-driven `e_log2.c` into Go
*and* accepting that the result is then wrong against every non-glibc Python.
That is not parity, it is a coin flip dressed as parity.

**Measured blast radius.** A 20 000-chunk fuzz corpus (8 distributions: uniform
random, ASCII, ASCII+high bytes, EUC-range, UTF-16-shaped, control-heavy,
low-entropy repeats, ASCII+sparse noise) was classified by both sides:

| | |
|---|---|
| features other than entropy that ever differed | **0** — the extension table, magic table, all five CJK validators, both UTF-16 and both UTF-32 validators, and every ratio are bit-exact |
| chunks where entropy differed | 832 (4.16%) |
| worst entropy difference | 52 ulp = **4.6 × 10⁻¹⁴** |
| **binary/text verdict mismatches** | **0 / 20 000** |
| closest any sample came to one of the tree's 19 entropy thresholds | **8.9 × 10⁻⁶** |
| safety margin | **1.9 × 10⁸ ×** |

The thresholds are six-decimal sklearn split points; landing within 5 × 10⁻¹⁴ of
one requires an adversarially constructed file, and even then the tree must also
route through that specific node. `TestFeatureVectorParity` therefore compares
23 features with exact `==` and gives entropy a `1e-9` tolerance — four orders
tighter than the closest observed approach, five orders looser than the worst
observed drift — so a real regression still fails the test. The tolerance is the
*only* inexact comparison in the suite.

The Go accumulation writes `entropy -= float64(p * math.Log2(p))`: the explicit
conversion is what stops a Go implementation from fusing the multiply-add into
an FMA (permitted by the spec, and done by arm64), which would make the drift
architecture-dependent on top of libc-dependent.

### 6.1 Why the CJK validators are exact and not approximate

Features 18–22 ask "does CPython's `gb2312` / `big5` / `shift_jis` / `euc-jp` /
`euc-kr` codec decode this chunk?". `golang.org/x/text`'s equivalents accept
*different* byte strings (its `GBK`/`HZ-GB2312` are looser than CPython's
`gb2312`, and unmapped code points differ throughout), so using them would have
been a silent approximation.

Instead `gen_encoding_tables.py` asks CPython itself: for every codec it probes
all 256 lead bytes, then every continuation, and emits the accept set as a
per-lead length plus trail-byte ranges (534 ranges total across the five codecs,
plus EUC-JP's single three-byte lead `0x8F`). This is valid because CPython's
multibyte decoders are single-pass and never backtrack — the lead byte fixes the
sequence length, then the sequence either maps or raises. Result: 0 mismatches
over 20 000 chunks. **No `golang.org/x/text` dependency is needed**, and
`internal/util` still imports stdlib only.

UTF-16/32 are hand-written structural validators (odd length, unpaired
surrogates, `> U+10FFFF`) — also 0 mismatches.

---

## 7. Fixture corpus

52 files, defined in one place (`CORPUS` in `encoding_probe.py`) and verified
byte-for-byte on every probe run, so the corpus cannot rot.

| group | fixtures |
|---|---|
| encodings | `utf8_plain`, `utf8_bom`, `utf8_bom_only`, `utf8_multibyte`, `utf8_invalid`, `latin1`, `cp1252_curly`, `shift_jis`, `utf16{le,be}_bom`, `utf16{le,be}_nobom`, `utf16le_short`, `utf32le_bom` |
| line endings | `crlf`, `cr_only`, `mixed_eol`, `lone_cr`, `nl_cr`, `bom_crlf`, `cr_at_eof`, `no_trailing_newline`, `whitespace_only`, `tabs_ff_vt`, `huge_single_line` (100 001 B), `huge_line_crlf` |
| realistic lab files | `pc1.startup` (BOM + CRLF, the Windows-authored case the function exists for), `noext` |
| NUL / control / high | `nul_text`, `nul_heavy.bin`, `single_nul.dat`, `control_heavy.dat`, `high_bytes.dat` |
| binary | `png_header.bin`, `elf_header.bin`, `gzip_header.bin`, `random_nomagic.bin` |
| size edges | `empty.txt`, `empty.bin`, `single_byte.txt` |
| chunk boundary | `boundary_512.txt`, `boundary_1024.txt` (straddle the 512/1024 window difference) |
| short-magic false positives | `bmw_prose.txt`, `mz_prose.txt` |
| extension vs content | `text_content.{png,pyc,zip,gz,bin}`, `crlf_content.tar`, `png_content.txt`, `PNG_UPPERCASE.PNG` |

Filler bytes come from a seeded Mersenne Twister, so `--regen-corpus`
reproduces the committed files exactly.

`expected.json` records per fixture: size, sha256, input hex (≤4 KB), `is_binary`,
`is_binary(check_extensions=False)`, `has_binary_extension`, chunk size/length,
the full 24-float feature vector in both decimal and `float.hex()` form,
`is_binary_v044` (when `--legacy` is given), read-mode hex + sha256 + size,
write-mode result hex + sha256 + size + changed flag, and any exception text.

---

## 8. Open items

| id | item |
|---|---|
| **OQ-E1** | **Which binaryornot is the port target?** The port pins 0.6.0 (the oracle, and what `pip install kathara==3.8.3` resolves today). But Python users with an old lockfile run 0.4.4 and get a different answer on 33% of this corpus — Python is not self-consistent here. 0.4.4 is unportable (§4). Needs a RULINGS.md entry saying "Kathara-go implements binaryornot 0.6.0 semantics", plus a DIVERGENCES.md note that 3.8.3-with-0.4.4 differs. |
| **OQ-E2** | **Should the port depend on an ML classifier at all?** `encoding_tree.go` is 400 lines of transpiled sklearn output whose upstream is explicitly "regenerate, do not edit". Pinning it means Kathara-go's file classification is frozen to one binaryornot release forever, and a future 0.7.0 would re-diverge. The alternative — a Kathara-owned deterministic rule (NUL in first 512 bytes, or not valid UTF-8) — is a PROPOSED-DIVERGENCES.md candidate, simpler and auditable, but visibly changes which files get normalised. Human decision. |
| **OQ-E3** | Two-byte magics (`BM`, `MZ`) and the extension table make plausible lab files binary (`BMW…` prose; any `*.tar`/`*.gz`/`*.bin` file in a device directory). Ported as-is. Worth a DIVERGENCES.md entry as an upstream bug: a text `startup` fragment named `routes.z` silently keeps its CRLFs. |
| **OQ-E4** | Write mode swallows write errors (read-only file, ENOSPC) inside the blanket `except Exception` and reports success. Reproduced at the marked line in `ConvertWin2LinuxInPlace`. Proposal: surface the error; requires a ruling because `pack_data` currently cannot fail here. |
| **OQ-E5** | `chardet` on exec/log streams (§4) is a separate porting task. `chardet.detect` returning `{'encoding': None}` makes `kathara exec` raise `TypeError` on undecodable container output — live Python bug, DIVERGENCES.md candidate. The Go replacement (UTF-8-or-latin-1) is a deliberate behaviour change needing a ruling. |
| **OQ-E6** | PACKAGE_GRAPH §5 file map says `binary.go`; this landed as `encoding.go` + two generated files. Amend the map. |
| **OQ-E7** | root-utils.md line 475 claims lone `\r` is untouched by `convert_win_2_linux`. It is not (§5.1). Correct the analysis doc. |
| **OQ-E8** | Errors here are plain `fmt.Errorf` wraps. Once `kerrors` lands they should wrap `ErrOS` per ERROR_CODES.md §1.2 (`OSError` → `OS` code), which is what Python's escaping `FileNotFoundError`/`PermissionError` maps to. |
| **OQ-E9** | `pack_file_for_tar`'s `io.IOBase` branch sizes text streams via `tell()` rather than `len()` (root-utils.md line 180). Untouched by this spike — it lives in `tar.go`'s API, which takes `[]byte`. Still open. |
| **OQ-E10** | The oracle venv resolves `binaryornot 0.6.0` while `src/requirements.txt` pins `0.4.4`. If Phase 3 rebuilds the venv from `requirements.txt`, every `expected.json` here becomes wrong. The version assertion in `loadExpectations` turns that into a loud failure; keep it. |

## 9. Things deliberately not done

* No `golang.org/x/text` / `saintfish/chardet` dependency — §6.1 showed the
  exact tables are both cheaper and correct, and §4 showed no 1.0 file-handling
  path needs a charset *guess*. `internal/util` remains stdlib-only.
* No `unicodedata` normalisation port. PORT_SPEC §12.8 groups it here, but
  `utils.py`'s only `unicodedata` use is `slug()` (`utils.py:231`,
  `NFKD` → `encode("ascii", "ignore")`), which names Docker/k8s objects and has
  nothing to do with file encoding. It belongs to `internal/util/slug.go`
  (PACKAGE_GRAPH §5) and is out of scope here.
* No symlink or tar-permission work. §12.8 groups it here, but it belongs to the
  `vfs`/`tar` spike, which owns `WriteTar` header fields.
