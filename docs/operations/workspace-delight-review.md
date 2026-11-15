# Workspace delight: session 9 review

Reviewed on 2026-11-15 against `main`, on `feat/workspace-delight`.

## Pull-request summary

The workspace now gives each page a clearer starting point: consistent disclosure
markers and primary actions, compact incoming-change badges, useful empty states,
and quieter search and note previews. Keyboard help is available from Workspace,
with navigation chords, list movement, and a persistent single-key off switch.

Reading flows into writing through a passage toolbar beside mouse selections and
a docked toolbar on touch devices. The editor puts save feedback beside Save,
moves secondary controls into Format, and keeps Focus view simple. Graph uses
quieter edges, clearer selection, a legend, and shorter status copy. Optional
motion respects reduced-motion preferences, and the workspace can open in an
installed standalone window while the server is running.

The browser guide, qualification journeys, and both README animations now reflect
these changes.

## Integration review and repairs

Reviewed the branch source and documentation diff end to end, plus generated
assets through their build and visual output. Checked disclosure consistency,
primary-action hierarchy, copy, shortcut coverage, and reduced-motion handling
across the eight implementation sessions.

The review found shortcut controls missing from the custom editor and Broken
links headers. Both now use the shared controls. Broken links navigation chords
activate its existing site-navigation links. The sheet now includes query
expression suggestions and the selected-passage action.

Opening the sheet closes its originating Workspace disclosure. Returning focus
to the now-hidden button failed the new browser regression; closing the sheet
now focuses the disclosure summary in that case. The six regression cases cover
reading, the editor, and Broken links at 1280 and 390 px, including toggling the
preference, opening help, returning focus, and resuming navigation.

The recorder now waits for confirmed capture and the current save state, shows
the shortcut sheet and passage toolbar, and frames the new text in both editor
panes. It also awaits the demo server's exit during cleanup.

## Focused validation

- Web build, TypeScript check, web lint, and the Go complexity ratchet passed.
- Keyboard shortcut and deferred-loader unit tests: 36 passed.
- Initial keyboard-related browser/visual selection: 41 passed; two new
  focus-return cases failed and led to the repair above. The focused three-case
  rerun passed, followed by all six desktop/phone cases after adding the width
  coverage. Existing shortcut accessibility and Graph keyboard checks passed in
  that initial selection.
- Focused Go checks passed for shared manifest heads, Graph assets, Pinned
  navigation, and scalar-title property suppression.
- `just demo` passed for both recordings. Repeated only `just demo web` after
  visual inspection identified the premature capture frame and editor framing;
  the revised recording passed and those frames were inspected again.
- Browser-test lint and `git diff --check` passed.

Full CI, the full browser suite (which already includes the visual project),
the conditional race check, and the final clean-tree check remain the ASL
runner's authoritative gates. This report does not claim those final gates ran
inside the worker turn.

## Visual and manual evidence

Every one of the 177 changed PNG baselines listed below was inspected in 23
contact sheets, covering both themes, desktop/phone widths, the short 320 px
editor, empty states, Graph resting/selected/list views, passage controls, and
keyboard help. No additional visual defect was found. Screenshot masks remain
intentional. Only the four `keyboard-shortcuts-*` baselines were changed in
session 9, to include the missing shortcut descriptions.

All 14 web demo frames were inspected with their captions, as were the six CLI
command-result frames. Revised capture and editor/save frames were inspected
again after re-recording. The demo uses a disposable copy of the demo vault and
shows the real Graph layout. Separate captures of the newly added Workspace
controls at phone width were also inspected; the six browser cases assert no
horizontal page overflow.

Installed-window evidence from session 8 remains applicable to the unchanged
manifest and serving behavior: Chrome 157.0.8103.78 installed and launched Brief
in an actual graphical standalone window, with zero installability errors and
zero service-worker registrations. Its handoff records the isolated profile,
light/dark captures, cleanup, and reproduction script. Graph layout and passage
qualification evidence from their implementation sessions is retained for
unchanged behavior.

This session did not perform a new physical-phone or VoiceOver qualification.
The expanded [manual journeys](ui-qualification.md) describe those checks;
browser viewport emulation is not recorded as a physical-device pass.

## Changed baselines reviewed

Paths below are relative to `internal/web/e2e/visual-baselines/`; every entry
links to the reviewed file. This inventory was generated from
`git diff --name-only main -- internal/web/e2e/visual-baselines`.

- [brief-entry-dark-1280.png](../../internal/web/e2e/visual-baselines/brief-entry-dark-1280.png)
- [brief-entry-dark-390.png](../../internal/web/e2e/visual-baselines/brief-entry-dark-390.png)
- [brief-entry-light-1280.png](../../internal/web/e2e/visual-baselines/brief-entry-light-1280.png)
- [brief-entry-light-390.png](../../internal/web/e2e/visual-baselines/brief-entry-light-390.png)
- [command-search-dark-1280.png](../../internal/web/e2e/visual-baselines/command-search-dark-1280.png)
- [command-search-dark-390.png](../../internal/web/e2e/visual-baselines/command-search-dark-390.png)
- [command-search-light-1280.png](../../internal/web/e2e/visual-baselines/command-search-light-1280.png)
- [command-search-light-390.png](../../internal/web/e2e/visual-baselines/command-search-light-390.png)
- [confirmation-dark-1280.png](../../internal/web/e2e/visual-baselines/confirmation-dark-1280.png)
- [confirmation-dark-390.png](../../internal/web/e2e/visual-baselines/confirmation-dark-390.png)
- [confirmation-light-1280.png](../../internal/web/e2e/visual-baselines/confirmation-light-1280.png)
- [confirmation-light-390.png](../../internal/web/e2e/visual-baselines/confirmation-light-390.png)
- [connection-dark-1280.png](../../internal/web/e2e/visual-baselines/connection-dark-1280.png)
- [connection-dark-390.png](../../internal/web/e2e/visual-baselines/connection-dark-390.png)
- [connection-light-1280.png](../../internal/web/e2e/visual-baselines/connection-light-1280.png)
- [connection-light-390.png](../../internal/web/e2e/visual-baselines/connection-light-390.png)
- [connection-saved-dark-1280.png](../../internal/web/e2e/visual-baselines/connection-saved-dark-1280.png)
- [connection-saved-dark-390.png](../../internal/web/e2e/visual-baselines/connection-saved-dark-390.png)
- [connection-saved-light-1280.png](../../internal/web/e2e/visual-baselines/connection-saved-light-1280.png)
- [connection-saved-light-390.png](../../internal/web/e2e/visual-baselines/connection-saved-light-390.png)
- [connection-writing-dark-1280.png](../../internal/web/e2e/visual-baselines/connection-writing-dark-1280.png)
- [connection-writing-dark-390.png](../../internal/web/e2e/visual-baselines/connection-writing-dark-390.png)
- [connection-writing-light-1280.png](../../internal/web/e2e/visual-baselines/connection-writing-light-1280.png)
- [connection-writing-light-390.png](../../internal/web/e2e/visual-baselines/connection-writing-light-390.png)
- [editor-conflict-dark-1280.png](../../internal/web/e2e/visual-baselines/editor-conflict-dark-1280.png)
- [editor-conflict-dark-390.png](../../internal/web/e2e/visual-baselines/editor-conflict-dark-390.png)
- [editor-conflict-light-1280.png](../../internal/web/e2e/visual-baselines/editor-conflict-light-1280.png)
- [editor-conflict-light-390.png](../../internal/web/e2e/visual-baselines/editor-conflict-light-390.png)
- [editor-dark-1280.png](../../internal/web/e2e/visual-baselines/editor-dark-1280.png)
- [editor-dark-390.png](../../internal/web/e2e/visual-baselines/editor-dark-390.png)
- [editor-light-1280.png](../../internal/web/e2e/visual-baselines/editor-light-1280.png)
- [editor-light-390.png](../../internal/web/e2e/visual-baselines/editor-light-390.png)
- [editor-short-320.png](../../internal/web/e2e/visual-baselines/editor-short-320.png)
- [focus-view-dark-1280.png](../../internal/web/e2e/visual-baselines/focus-view-dark-1280.png)
- [focus-view-dark-390.png](../../internal/web/e2e/visual-baselines/focus-view-dark-390.png)
- [focus-view-light-1280.png](../../internal/web/e2e/visual-baselines/focus-view-light-1280.png)
- [focus-view-light-390.png](../../internal/web/e2e/visual-baselines/focus-view-light-390.png)
- [focused-writing-dark-1280.png](../../internal/web/e2e/visual-baselines/focused-writing-dark-1280.png)
- [focused-writing-light-1280.png](../../internal/web/e2e/visual-baselines/focused-writing-light-1280.png)
- [global-capture-dark-1280.png](../../internal/web/e2e/visual-baselines/global-capture-dark-1280.png)
- [global-capture-dark-390.png](../../internal/web/e2e/visual-baselines/global-capture-dark-390.png)
- [global-capture-light-1280.png](../../internal/web/e2e/visual-baselines/global-capture-light-1280.png)
- [global-capture-light-390.png](../../internal/web/e2e/visual-baselines/global-capture-light-390.png)
- [graph-dark-1280.png](../../internal/web/e2e/visual-baselines/graph-dark-1280.png)
- [graph-dark-390.png](../../internal/web/e2e/visual-baselines/graph-dark-390.png)
- [graph-layout-dark-1280.png](../../internal/web/e2e/visual-baselines/graph-layout-dark-1280.png)
- [graph-layout-dark-390.png](../../internal/web/e2e/visual-baselines/graph-layout-dark-390.png)
- [graph-layout-light-1280.png](../../internal/web/e2e/visual-baselines/graph-layout-light-1280.png)
- [graph-layout-light-390.png](../../internal/web/e2e/visual-baselines/graph-layout-light-390.png)
- [graph-light-1280.png](../../internal/web/e2e/visual-baselines/graph-light-1280.png)
- [graph-light-390.png](../../internal/web/e2e/visual-baselines/graph-light-390.png)
- [graph-list-dark-1280.png](../../internal/web/e2e/visual-baselines/graph-list-dark-1280.png)
- [graph-list-dark-390.png](../../internal/web/e2e/visual-baselines/graph-list-dark-390.png)
- [graph-list-light-1280.png](../../internal/web/e2e/visual-baselines/graph-list-light-1280.png)
- [graph-list-light-390.png](../../internal/web/e2e/visual-baselines/graph-list-light-390.png)
- [graph-neighborhood-dark-1280.png](../../internal/web/e2e/visual-baselines/graph-neighborhood-dark-1280.png)
- [graph-neighborhood-dark-390.png](../../internal/web/e2e/visual-baselines/graph-neighborhood-dark-390.png)
- [graph-neighborhood-light-1280.png](../../internal/web/e2e/visual-baselines/graph-neighborhood-light-1280.png)
- [graph-neighborhood-light-390.png](../../internal/web/e2e/visual-baselines/graph-neighborhood-light-390.png)
- [graph-rest-dark-1280.png](../../internal/web/e2e/visual-baselines/graph-rest-dark-1280.png)
- [graph-rest-dark-390.png](../../internal/web/e2e/visual-baselines/graph-rest-dark-390.png)
- [graph-rest-light-1280.png](../../internal/web/e2e/visual-baselines/graph-rest-light-1280.png)
- [graph-rest-light-390.png](../../internal/web/e2e/visual-baselines/graph-rest-light-390.png)
- [health-error-dark-1280.png](../../internal/web/e2e/visual-baselines/health-error-dark-1280.png)
- [health-error-dark-390.png](../../internal/web/e2e/visual-baselines/health-error-dark-390.png)
- [health-error-light-1280.png](../../internal/web/e2e/visual-baselines/health-error-light-1280.png)
- [health-error-light-390.png](../../internal/web/e2e/visual-baselines/health-error-light-390.png)
- [keyboard-shortcuts-dark-1280.png](../../internal/web/e2e/visual-baselines/keyboard-shortcuts-dark-1280.png)
- [keyboard-shortcuts-dark-390.png](../../internal/web/e2e/visual-baselines/keyboard-shortcuts-dark-390.png)
- [keyboard-shortcuts-light-1280.png](../../internal/web/e2e/visual-baselines/keyboard-shortcuts-light-1280.png)
- [keyboard-shortcuts-light-390.png](../../internal/web/e2e/visual-baselines/keyboard-shortcuts-light-390.png)
- [keyboard-workspace-dark-1280.png](../../internal/web/e2e/visual-baselines/keyboard-workspace-dark-1280.png)
- [keyboard-workspace-dark-390.png](../../internal/web/e2e/visual-baselines/keyboard-workspace-dark-390.png)
- [keyboard-workspace-light-1280.png](../../internal/web/e2e/visual-baselines/keyboard-workspace-light-1280.png)
- [keyboard-workspace-light-390.png](../../internal/web/e2e/visual-baselines/keyboard-workspace-light-390.png)
- [library-dark-1280.png](../../internal/web/e2e/visual-baselines/library-dark-1280.png)
- [library-dark-390.png](../../internal/web/e2e/visual-baselines/library-dark-390.png)
- [library-empty-recovery-dark-1280.png](../../internal/web/e2e/visual-baselines/library-empty-recovery-dark-1280.png)
- [library-empty-recovery-dark-390.png](../../internal/web/e2e/visual-baselines/library-empty-recovery-dark-390.png)
- [library-empty-recovery-light-1280.png](../../internal/web/e2e/visual-baselines/library-empty-recovery-light-1280.png)
- [library-empty-recovery-light-390.png](../../internal/web/e2e/visual-baselines/library-empty-recovery-light-390.png)
- [library-empty.png](../../internal/web/e2e/visual-baselines/library-empty.png)
- [library-entry-dark-1280.png](../../internal/web/e2e/visual-baselines/library-entry-dark-1280.png)
- [library-entry-dark-390.png](../../internal/web/e2e/visual-baselines/library-entry-dark-390.png)
- [library-entry-light-1280.png](../../internal/web/e2e/visual-baselines/library-entry-light-1280.png)
- [library-entry-light-390.png](../../internal/web/e2e/visual-baselines/library-entry-light-390.png)
- [library-error.png](../../internal/web/e2e/visual-baselines/library-error.png)
- [library-light-1280.png](../../internal/web/e2e/visual-baselines/library-light-1280.png)
- [library-light-390.png](../../internal/web/e2e/visual-baselines/library-light-390.png)
- [library-start-dark-1280.png](../../internal/web/e2e/visual-baselines/library-start-dark-1280.png)
- [library-start-dark-390.png](../../internal/web/e2e/visual-baselines/library-start-dark-390.png)
- [library-start-light-1280.png](../../internal/web/e2e/visual-baselines/library-start-light-1280.png)
- [library-start-light-390.png](../../internal/web/e2e/visual-baselines/library-start-light-390.png)
- [manage-health-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-health-dark-1280.png)
- [manage-health-dark-390.png](../../internal/web/e2e/visual-baselines/manage-health-dark-390.png)
- [manage-health-light-1280.png](../../internal/web/e2e/visual-baselines/manage-health-light-1280.png)
- [manage-health-light-390.png](../../internal/web/e2e/visual-baselines/manage-health-light-390.png)
- [manage-history-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-history-dark-1280.png)
- [manage-history-dark-390.png](../../internal/web/e2e/visual-baselines/manage-history-dark-390.png)
- [manage-history-light-1280.png](../../internal/web/e2e/visual-baselines/manage-history-light-1280.png)
- [manage-history-light-390.png](../../internal/web/e2e/visual-baselines/manage-history-light-390.png)
- [manage-properties-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-properties-dark-1280.png)
- [manage-properties-dark-390.png](../../internal/web/e2e/visual-baselines/manage-properties-dark-390.png)
- [manage-properties-light-1280.png](../../internal/web/e2e/visual-baselines/manage-properties-light-1280.png)
- [manage-properties-light-390.png](../../internal/web/e2e/visual-baselines/manage-properties-light-390.png)
- [manage-properties-status-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-properties-status-dark-1280.png)
- [manage-properties-status-dark-390.png](../../internal/web/e2e/visual-baselines/manage-properties-status-dark-390.png)
- [manage-properties-status-light-1280.png](../../internal/web/e2e/visual-baselines/manage-properties-status-light-1280.png)
- [manage-properties-status-light-390.png](../../internal/web/e2e/visual-baselines/manage-properties-status-light-390.png)
- [manage-settings-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-settings-dark-1280.png)
- [manage-settings-dark-390.png](../../internal/web/e2e/visual-baselines/manage-settings-dark-390.png)
- [manage-settings-light-1280.png](../../internal/web/e2e/visual-baselines/manage-settings-light-1280.png)
- [manage-settings-light-390.png](../../internal/web/e2e/visual-baselines/manage-settings-light-390.png)
- [manage-sync-dark-1280.png](../../internal/web/e2e/visual-baselines/manage-sync-dark-1280.png)
- [manage-sync-dark-390.png](../../internal/web/e2e/visual-baselines/manage-sync-dark-390.png)
- [manage-sync-light-1280.png](../../internal/web/e2e/visual-baselines/manage-sync-light-1280.png)
- [manage-sync-light-390.png](../../internal/web/e2e/visual-baselines/manage-sync-light-390.png)
- [new-note-dark-1280.png](../../internal/web/e2e/visual-baselines/new-note-dark-1280.png)
- [new-note-dark-390.png](../../internal/web/e2e/visual-baselines/new-note-dark-390.png)
- [new-note-light-1280.png](../../internal/web/e2e/visual-baselines/new-note-light-1280.png)
- [new-note-light-390.png](../../internal/web/e2e/visual-baselines/new-note-light-390.png)
- [note-peek-dark-1280.png](../../internal/web/e2e/visual-baselines/note-peek-dark-1280.png)
- [note-peek-dark-390.png](../../internal/web/e2e/visual-baselines/note-peek-dark-390.png)
- [note-peek-light-1280.png](../../internal/web/e2e/visual-baselines/note-peek-light-1280.png)
- [note-peek-light-390.png](../../internal/web/e2e/visual-baselines/note-peek-light-390.png)
- [passage-saved-dark-1280.png](../../internal/web/e2e/visual-baselines/passage-saved-dark-1280.png)
- [passage-saved-dark-390.png](../../internal/web/e2e/visual-baselines/passage-saved-dark-390.png)
- [passage-saved-light-1280.png](../../internal/web/e2e/visual-baselines/passage-saved-light-1280.png)
- [passage-saved-light-390.png](../../internal/web/e2e/visual-baselines/passage-saved-light-390.png)
- [passage-selection-dark-1280.png](../../internal/web/e2e/visual-baselines/passage-selection-dark-1280.png)
- [passage-selection-dark-390.png](../../internal/web/e2e/visual-baselines/passage-selection-dark-390.png)
- [passage-selection-light-1280.png](../../internal/web/e2e/visual-baselines/passage-selection-light-1280.png)
- [passage-selection-light-390.png](../../internal/web/e2e/visual-baselines/passage-selection-light-390.png)
- [passage-writing-dark-1280.png](../../internal/web/e2e/visual-baselines/passage-writing-dark-1280.png)
- [passage-writing-dark-390.png](../../internal/web/e2e/visual-baselines/passage-writing-dark-390.png)
- [passage-writing-light-1280.png](../../internal/web/e2e/visual-baselines/passage-writing-light-1280.png)
- [passage-writing-light-390.png](../../internal/web/e2e/visual-baselines/passage-writing-light-390.png)
- [pinned-dark-1280.png](../../internal/web/e2e/visual-baselines/pinned-dark-1280.png)
- [pinned-dark-390.png](../../internal/web/e2e/visual-baselines/pinned-dark-390.png)
- [pinned-light-1280.png](../../internal/web/e2e/visual-baselines/pinned-light-1280.png)
- [pinned-light-390.png](../../internal/web/e2e/visual-baselines/pinned-light-390.png)
- [quiet-reading-dark-1280.png](../../internal/web/e2e/visual-baselines/quiet-reading-dark-1280.png)
- [quiet-reading-dark-390.png](../../internal/web/e2e/visual-baselines/quiet-reading-dark-390.png)
- [quiet-reading-light-1280.png](../../internal/web/e2e/visual-baselines/quiet-reading-light-1280.png)
- [quiet-reading-light-390.png](../../internal/web/e2e/visual-baselines/quiet-reading-light-390.png)
- [reading-dark-1280.png](../../internal/web/e2e/visual-baselines/reading-dark-1280.png)
- [reading-dark-390.png](../../internal/web/e2e/visual-baselines/reading-dark-390.png)
- [reading-entry-dark-1280.png](../../internal/web/e2e/visual-baselines/reading-entry-dark-1280.png)
- [reading-entry-dark-390.png](../../internal/web/e2e/visual-baselines/reading-entry-dark-390.png)
- [reading-entry-light-1280.png](../../internal/web/e2e/visual-baselines/reading-entry-light-1280.png)
- [reading-entry-light-390.png](../../internal/web/e2e/visual-baselines/reading-entry-light-390.png)
- [reading-light-1280.png](../../internal/web/e2e/visual-baselines/reading-light-1280.png)
- [reading-light-390.png](../../internal/web/e2e/visual-baselines/reading-light-390.png)
- [restore-comparison-dark-1280.png](../../internal/web/e2e/visual-baselines/restore-comparison-dark-1280.png)
- [restore-comparison-dark-390.png](../../internal/web/e2e/visual-baselines/restore-comparison-dark-390.png)
- [restore-comparison-light-1280.png](../../internal/web/e2e/visual-baselines/restore-comparison-light-1280.png)
- [restore-comparison-light-390.png](../../internal/web/e2e/visual-baselines/restore-comparison-light-390.png)
- [settings-confirmation-dark-1280.png](../../internal/web/e2e/visual-baselines/settings-confirmation-dark-1280.png)
- [settings-confirmation-dark-390.png](../../internal/web/e2e/visual-baselines/settings-confirmation-dark-390.png)
- [settings-confirmation-light-1280.png](../../internal/web/e2e/visual-baselines/settings-confirmation-light-1280.png)
- [settings-confirmation-light-390.png](../../internal/web/e2e/visual-baselines/settings-confirmation-light-390.png)
- [side-reference-dark-1280.png](../../internal/web/e2e/visual-baselines/side-reference-dark-1280.png)
- [side-reference-dark-390.png](../../internal/web/e2e/visual-baselines/side-reference-dark-390.png)
- [side-reference-light-1280.png](../../internal/web/e2e/visual-baselines/side-reference-light-1280.png)
- [side-reference-light-390.png](../../internal/web/e2e/visual-baselines/side-reference-light-390.png)
- [synthesis-dark-1280.png](../../internal/web/e2e/visual-baselines/synthesis-dark-1280.png)
- [synthesis-dark-390.png](../../internal/web/e2e/visual-baselines/synthesis-dark-390.png)
- [synthesis-light-1280.png](../../internal/web/e2e/visual-baselines/synthesis-light-1280.png)
- [synthesis-light-390.png](../../internal/web/e2e/visual-baselines/synthesis-light-390.png)
- [threads-empty-dark-1280.png](../../internal/web/e2e/visual-baselines/threads-empty-dark-1280.png)
- [threads-empty-dark-390.png](../../internal/web/e2e/visual-baselines/threads-empty-dark-390.png)
- [threads-empty-light-1280.png](../../internal/web/e2e/visual-baselines/threads-empty-light-1280.png)
- [threads-empty-light-390.png](../../internal/web/e2e/visual-baselines/threads-empty-light-390.png)
- [writing-format-dark-1280.png](../../internal/web/e2e/visual-baselines/writing-format-dark-1280.png)
- [writing-format-dark-390.png](../../internal/web/e2e/visual-baselines/writing-format-dark-390.png)
- [writing-format-light-1280.png](../../internal/web/e2e/visual-baselines/writing-format-light-1280.png)
- [writing-format-light-390.png](../../internal/web/e2e/visual-baselines/writing-format-light-390.png)

