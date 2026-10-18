// Every spec has one declared home; CI checks this catalog before preparing vaults.
export const browserSuites = {
  "pr": [
    "smoke.spec.js",
    "screen-polish.spec.js",
    "editor.spec.js",
    "workspace-polish.spec.js",
    "reading-performance.spec.js",
    "settings-feedback.spec.js",
    "polish-continuity.spec.js",
    "focused-workspace.spec.js",
    "editor-management-feedback.spec.js",
    "library-editor-facets.spec.js",
    "library-position-history.spec.js",
    "note-navigation.spec.js",
    "sync.spec.js",
    "new-note.spec.js",
    "polish-refinement.spec.js",
    "workflow-completion.spec.js"
  ],
  "weekly": [
    "browser-tweaks.spec.js",
    "capture-reading-search.spec.js",
    "creation-feedback.spec.js",
    "editor-journey.spec.js",
    "guards.spec.js",
    "history-backlinks-polish.spec.js",
    "history.spec.js",
    "isolation.spec.js",
    "library-copy-dates.spec.js",
    "library-polish.spec.js",
    "library-range.spec.js",
    "note-polish.spec.js",
    "polish-details.spec.js",
    "property-query-polish.spec.js",
    "query-polish.spec.js",
    "reading-polish.spec.js",
    "search-capture-polish.spec.js",
    "settings.spec.js",
    "usability.spec.js"
  ],
  "hosted": [
    "qualification.spec.js"
  ],
  "readOnly": [
    "read-only.spec.js"
  ],
  "performance": [
    "vault-performance.spec.js"
  ],
  "visual": [
    "visual-polish.spec.js"
  ]
};
