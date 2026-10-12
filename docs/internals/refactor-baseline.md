# Refactoring baseline

Measured at `HEAD 9d18e34` on 2026-09-01. This is the committed baseline every
later refactoring session compares against.

Reproduce with:

```sh
just metrics
```

`scripts/metrics.py` is a thin entrypoint over sibling `metrics_*.py` modules,
following the `vault_safety_*.py` convention already in `scripts/`. It is pure
measurement — it reads the repository and writes only to stdout. It is
deliberately **not** part of `just ci`: it is a report, not a gate.

Two details that look incidental and are not:

- The only file it creates is an ephemeral golangci-lint config inside a
  `tempfile.TemporaryDirectory()`, because golangci-lint v2 exposes no
  command-line flags for per-linter thresholds. Passing our own config also
  pins the thresholds to the ones below, so a repo-level `.golangci.yml` —
  Session S02's deliverable — cannot silently move the numbers this baseline
  records.
- The TypeScript complexity half is the one measurement that does **not** pin
  its own thresholds: it runs the repo's `eslint.config.js`, because that file
  is where those thresholds were first written (§6) rather than a config added
  after the fact. It needs `npm ci` to have run; without ESLint installed the
  section reports "unavailable" instead of failing.
- The recipe runs `python3 -B`. Without it, importing the sibling modules
  writes `scripts/__pycache__/` into the working tree, which this repo's
  `.gitignore` does not cover — only a user's *global* gitignore does, which is
  precisely the hazard that `.gitignore`'s own comment warns about.

**The initial number being ratcheted was 281** — source-level complexity
violations at this baseline. Test-file violations (271) were tracked but
explicitly out of scope: they buy
the 1.32 Go test ratio that makes the rest of the work safe.

## Contents

Section numbers (§1–§6) are preserved across the files, and cross-references
such as "§2.4" or "§6" refer to this set.

- [Sections 1–4: denominator, measurements, test coverage, how later sessions use this file](refactor-baseline-measurements.md)
- [Sections 5–6: the Go ratchet gate (S02) and the TypeScript ratchet gate (S03)](refactor-baseline-ratchets.md)
