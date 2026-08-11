import type { ManagementResult } from "./api-types-notes";

export type BulkAction =
  | "trash"
  | "move"
  | "frontmatter_set"
  | "frontmatter_unset";

export type BulkNotesRequest = {
  action: BulkAction;
  keys: string[];
  token: string;
  folder?: string;
  property?: string;
  op?: "=" | "+=" | "-=";
  value?: string;
  previewID?: string;
};

export type BulkPreviewRequest = BulkNotesRequest;

export type BulkApplyRequest = BulkNotesRequest & {
  previewID: string;
};

export type BulkFailure = {
  key?: string;
  rel_path?: string;
  code?: string;
  message?: string;
};

export type BulkManagementItem = {
  key?: string;
  path?: string;
  rel_path?: string;
  changed: boolean;
};

export type BulkManagementResult = ManagementResult & {
  changed?: number;
  unchanged?: number;
  failed?: number;
  results?: BulkManagementItem[];
  failures?: BulkFailure[];
};

export type BulkManagementPath = {
  key: string;
  path: string;
  rel_path: string;
};

export type BulkAffectedPath = {
  role: string;
  key: string;
  path: string;
  rel_path: string;
  exists: boolean;
};

export type BulkPreviewValue = {
  present: boolean;
  value?: unknown;
};

export type BulkFrontmatterChange = {
  key: string;
  before: BulkPreviewValue;
  after: BulkPreviewValue;
  changed: boolean;
};

export type BulkMovePreview = {
  rewrite_count: number;
  rewrite_file_count: number;
  body_rewrite_count: number;
  frontmatter_rewrite_count: number;
};

export type BulkPreviewRow = {
  key: string;
  path: string;
  rel_path: string;
  source: BulkManagementPath;
  destination?: BulkManagementPath;
  changed: boolean;
  move?: BulkMovePreview;
  frontmatter: BulkFrontmatterChange[];
  affected: BulkAffectedPath[];
};

export type BulkPreviewManagementResult = ManagementResult & {
  action?: BulkAction;
  changed?: number;
  unchanged?: number;
  failed?: number;
  preview_id?: string;
  expires_at?: string;
  results?: BulkPreviewRow[];
  failures?: BulkFailure[];
};
