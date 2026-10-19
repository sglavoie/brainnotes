## Verification

- [ ] `just ci` passes.
- [ ] `internal/cli` changes: `just lint-phase-gate` passes (it runs outside `just ci`).
- [ ] Synchronization changes (`vaultsync`, `vaultexport`, `vaultimport`, `vaultsyncd`, `synctransport`): `just test-sync-transport` passes, and `just test-sync-recovery` for recovery or fault-handling changes.
- [ ] Browser changes: the relevant Playwright suite passes and visual baselines are reviewed.
- [ ] Browser UI changes: Library, reading, and editing remain usable at 320 px and 200% zoom.
- [ ] Browser UI changes: I completed `docs/operations/ui-qualification.md` on the exact build, including a physical phone for editor changes and a screen reader, and recorded its results below.

Manual qualification evidence (commit, device, OS, browser, screen reader, results and failures):

