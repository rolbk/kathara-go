# Autonomous rulings on open questions

Made under the accepted-defaults framework (see memory: kathara-port-decisions). Each is
binding for the port; vectors/goldens enforce them.

| OQ | Ruling |
|---|---|
| OQ-14a Unicode digits | **Replicate CPython.** Interface-number dispatch must mirror `int()`: Unicode Nd digits accepted (`pc1[٣]` = iface 3), PEP 515 underscores between digits (`0_1`→1, `1_0`→10; `_0`/`0_` are metas), and meta/CD name classes use Unicode `[\p{L}\p{N}_]`, not ASCII `\w`. Dispatch on the int-parse result only — never route other errors to the meta path. |
| OQ-15a FolderParser order | **Canonicalize:** Go sorts machine names (Python order is filesystem-dependent). See PROPOSED-DIVERGENCES.md. |
| OQ-15b depgen flatten | **Replicate:** within-level order = lab.dep line order (deterministic insertion order in Python). Not a canonical toposort. |
| lab.dep empty variants | Comments-only file → empty non-nil list; missing/empty file → nil. Preserved via nil-vs-empty distinction. |
| FeatureNotAvailable human label | **`ERROR_CODES.md` §1.4 governs: the human line is `CRITICAL (FeatureNotAvailable) {message}`.** `JSON_CLI_CONTRACT.md` §5.6 says `FeatureNotAvailableError` in a parenthetical; that document's scope line states human-mode output is *not* governed by it and lists `ERROR_CODES.md` as normative, while §1.4 pins the label per row (as it does for the other two port-new codes, `InternalError` and `ConfirmationRequired`, neither of which takes the `Error` suffix). No Python parity evidence can break the tie: 3.8.3 never emits this code, and the §0.1 "human_label = Python class name" rule has no class to name — the client-side analogue `NotSupportedError` is already the label of code `NotSupported`, so reusing it would break human-label injectivity (`ERROR_CODES.md` §4). Implemented in `kerrors.HumanLabel`, pinned by `TestHumanLabels`. **Action for the contract owner:** errata the §5.6 parenthetical in `JSON_CLI_CONTRACT.md` (that doc's §9 requires a human decision + version bump; no goldens recorded yet). |
