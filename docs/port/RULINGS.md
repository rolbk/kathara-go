# Autonomous rulings on open questions

Made under the accepted-defaults framework (see memory: kathara-port-decisions). Each is
binding for the port; vectors/goldens enforce them.

| OQ | Ruling |
|---|---|
| OQ-14a Unicode digits | **Replicate CPython.** Interface-number dispatch must mirror `int()`: Unicode Nd digits accepted (`pc1[٣]` = iface 3), PEP 515 underscores between digits (`0_1`→1, `1_0`→10; `_0`/`0_` are metas), and meta/CD name classes use Unicode `[\p{L}\p{N}_]`, not ASCII `\w`. Dispatch on the int-parse result only — never route other errors to the meta path. |
| OQ-15a FolderParser order | **Canonicalize:** Go sorts machine names (Python order is filesystem-dependent). See PROPOSED-DIVERGENCES.md. |
| OQ-15b depgen flatten | **Replicate:** within-level order = lab.dep line order (deterministic insertion order in Python). Not a canonical toposort. |
| lab.dep empty variants | Comments-only file → empty non-nil list; missing/empty file → nil. Preserved via nil-vs-empty distinction. |
