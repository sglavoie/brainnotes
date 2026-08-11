import type { ManagementResult } from "./api-types-notes";

export type UndoRequest = {
  token: string;
  entryID?: string;
  force?: boolean;
  confirmForce?: boolean;
};

export type HistoryFile = {
  path: string;
  before: string;
  after: string;
};

export type HistoryEntry = {
  id: string;
  timestamp: string;
  command: string;
  file_count: number;
  files: HistoryFile[];
};

export type HistoryResult = ManagementResult & {
  entries?: HistoryEntry[];
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
