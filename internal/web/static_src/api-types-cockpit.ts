// Cockpit / Daily Fixes ------------------------------------------------------

export type CockpitAction = {
  kind: string;
  label: string;
};

export type CockpitAffectedNote = {
  title: string;
  rel_path: string;
  folder?: string;
  location?: string;
};

export type CockpitTask = {
  id: string;
  signature: string;
  kind: string;
  priority: number;
  title: string;
  summary: string;
  detail: string;
  count: number;
  affected: CockpitAffectedNote[];
  actions: CockpitAction[];
  triage_only: boolean;
};

export type CockpitCounts = {
  active: number;
  done_hidden: number;
  snoozed_hidden: number;
  total_generated: number;
};

export type CockpitWarning = {
  message: string;
  path?: string;
};

export type CockpitQueueResult = {
  ok: boolean;
  code?: string;
  message?: string;
  tasks: CockpitTask[];
  counts: CockpitCounts;
  warnings: CockpitWarning[];
};

export type CockpitItemAction =
  | "mark_done"
  | "done"
  | "snooze"
  | "clear"
  | "exempt_dead_end";

export type CockpitSnooze = "tomorrow" | "week";

export type CockpitItemMutationRequest = {
  id: string;
  action: CockpitItemAction;
  token: string;
  snooze?: CockpitSnooze;
};

export type FixAction = "schema_repair" | "relink" | "create_stub";

export type FixMutationRequest = {
  id: string;
  action: FixAction;
  token: string;
  previewID?: string;
};

export type NotePreviewText = {
  present: boolean;
  text: string;
};

export type NotePreviewChange = {
  kind: string;
  key?: string;
  line?: number;
  old: NotePreviewText;
  new: NotePreviewText;
};

export type NotePreview = {
  rel_path: string;
  changes: NotePreviewChange[];
};

export type FixSkip = {
  rel_path?: string;
  target?: string;
  reason: string;
};

export type FixPreviewResult = {
  ok: boolean;
  code?: string;
  message?: string;
  action: string;
  task_id: string;
  previews: NotePreview[];
  skipped: FixSkip[];
  preview_id: string;
  expires_at: string;
};

export type FixApplyResult = {
  ok: boolean;
  code?: string;
  message?: string;
  action: string;
  task_id: string;
  changed: string[];
  created: string[];
  skipped: FixSkip[];
  journal_id?: string;
};
