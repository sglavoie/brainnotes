// Saved views transport -------------------------------------------------------

import type { PropertyConfigDiffLine, PropertyConfigStatus } from "./api-types";

// SavedViewSummary is one resolved saved view. A view whose query stopped
// compiling arrives with `enabled: false` and its `detail` — the caret block for
// a query that no longer parses, a one-line message for an unusable sort spec —
// rather than being dropped, so every consumer renders it disabled.
export type SavedViewSummary = {
  name: string;
  description: string;
  query: string;
  search?: string;
  // normalized is the canonical Expr.String() spelling, empty for a disabled
  // view.
  normalized: string;
  sort: string;
  relative_to: string;
  limit: number;
  enabled: boolean;
  detail: string;
  needs_graph: boolean;
};

export type SavedViewOp = "upsert" | "rename" | "delete";

// SavedViewDefinition is the stored half of one view. The name travels beside
// it on the request, so a payload cannot disagree with itself.
export type SavedViewDefinition = {
  filter_params?: string;
  query: string;
  search?: string;
  sort: string;
  relative_to: string;
  limit: number;
  description: string;
};

export type SavedViewMutationRequest = {
  token: string;
  op: SavedViewOp;
  name: string;
  new_name?: string;
  view?: SavedViewDefinition;
};

export type SavedViewApplyRequest = {
  preview_id: string;
};

export type SavedViewsRequestBody = SavedViewMutationRequest | SavedViewApplyRequest;

// SavedViewsResult is the one shape all three routes answer with: a list fills
// only `views`, a preview adds `preview_id`/`expires_at`/`created`, an apply
// adds `journal_id`.
export type SavedViewsResult = {
  ok: boolean;
  code?: string;
  message?: string;
  generation: number;
  action: string;
  name: string;
  new_name: string;
  views: SavedViewSummary[];
  // created reports that an upsert introduces a block that does not exist yet.
  // It is what lets the client chain preview into apply for a creation while
  // still confirming the rendered diff for an overwrite.
  created: boolean;
  config_diff: PropertyConfigDiffLine[];
  preview_id: string;
  expires_at: string;
  journal_id: string;
  config: PropertyConfigStatus;
};
