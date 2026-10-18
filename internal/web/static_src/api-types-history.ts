import type { ManagementResult } from "./api-types-notes";

export type UndoRequest = {
  token: string;
  entryID?: string;
  force?: boolean;
  confirmForce?: boolean;
  /**
   * previewID redeems a restore preview. The server requires one before it will
   * force a restore over newer work, and verifies one whenever it is sent: the
   * receipt is what proves somebody was shown what the restore would replace.
   */
  previewID?: string;
};

export type HistoryPreviewRequest = {
  token: string;
  entryID?: string;
};

export type HistoryFile = {
  path: string;
  before: string;
  after: string;
  /** class is the server's name for what kind of file this is: note, attachment,
   * template, configuration, state, other. */
  class: string;
  /** recorded says restoring this path becomes a commit in the content history. */
  recorded: boolean;
  /** travels says that commit reaches the other replica. The two are separate
   * questions and the page reports them separately, because a restored path can
   * be recorded here and stay here. */
  travels: boolean;
};

export type HistoryClassCount = {
  class: string;
  label: string;
  count: number;
};

export type HistoryEntry = {
  summary?: string;
  id: string;
  timestamp: string;
  command: string;
  /** origin is where the change came from, classified by the server from the
   * recorded command. The raw command stays alongside it so a reader who does not
   * believe the classification can check. */
  origin: string;
  origin_label: string;
  file_count: number;
  classes: HistoryClassCount[];
  files: HistoryFile[];
};

/** HistoryOption is one filter value and the name to show for it. The vocabulary
 * is read from the server rather than compiled in, so the names in the filter and
 * the names on the rows cannot disagree. */
export type HistoryOption = {
  value: string;
  label: string;
};

export type HistoryWarning = {
  id: string;
  message: string;
};

export type HistoryResult = ManagementResult & {
  entries?: HistoryEntry[];
  total?: number;
  offset?: number;
  limit?: number;
  has_more?: boolean;
  origins?: HistoryOption[];
  classes?: HistoryOption[];
  warnings?: HistoryWarning[];
  warning_count?: number;
};

/** HistoryQuery is the window and the filters one request asks for. */
export type HistoryQuery = {
  limit?: number;
  offset?: number;
  origins?: string[];
  classes?: string[];
  path?: string;
  pathMatch?: "contains" | "exact";
};

export type UndoChange = {
  path: string;
  current_state: string;
  restored_state: string;
};

export type UndoConflict = {
  path: string;
  expected_state: string;
  current_state: string;
};

export type UndoResult = ManagementResult & {
  entry_id?: string;
  command?: string;
  timestamp?: string;
  changes?: UndoChange[];
  conflicts?: UndoConflict[];
  requires_force?: boolean;
};

export type HistoryPreviewChange = UndoChange & {
  comparison?: { current: string; restored: string };
  comparison_unavailable?: string;
  class: string;
  recorded: boolean;
  travels: boolean;
  /** conflict marks a path where this restore would replace newer work. */
  conflict: boolean;
};

export type HistoryPreviewResult = ManagementResult & {
  preview_id?: string;
  expires_at?: string;
  entry_id?: string;
  command?: string;
  origin?: string;
  origin_label?: string;
  timestamp?: string;
  requires_force?: boolean;
  changes?: HistoryPreviewChange[];
  conflicts?: UndoConflict[];
};
