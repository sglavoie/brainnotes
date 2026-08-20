export type SynthesisNoteIdentity = {
  key: string;
  title: string;
  path: string;
  route: string;
  folder: string;
};

export type SynthesisExcerpt = {
  path: string;
  start_line: number;
  end_line: number;
};

export type SynthesisManifest = {
  source_path: string;
  max_bytes: number;
  byte_count: number;
  notes: SynthesisNoteIdentity[];
  excerpts: SynthesisExcerpt[];
};

// candidates are ordered supporting-note keys; the server preserves the order it
// receives them in, so it is the user's ordering rather than a ranking.
export type SynthesisContextRequest = {
  source: string;
  candidates: string[];
  purpose: string;
  prompt: string;
  token: string;
};

export type SynthesisContextResponse = {
  context_id: string;
  expires_at: string;
  vault_id: string;
  agent_enabled: boolean;
  purpose: string;
  prompt: string;
  anchor: SynthesisNoteIdentity;
  supporters: SynthesisNoteIdentity[];
  manifest: SynthesisManifest;
  markdown: string;
};

export type SynthesisGenerateRequest = {
  context_id: string;
  token: string;
};

// enabled is false when no [agent].command is configured. That is a successful
// response carrying a message rather than an error, so the workbench reports it
// without discarding anything the user typed.
export type SynthesisGenerateResponse = {
  enabled: boolean;
  message: string;
  context_id: string;
  output: string;
  rendered_html: string;
};

export type SynthesisPreviewRequest = {
  context_id: string;
  title: string;
  folder: string;
  draft: string;
  token: string;
};

export type SynthesisPreviewResponse = {
  preview_id: string;
  expires_at: string;
  context_id: string;
  destination: string;
  markdown: string;
  rendered_html: string;
};

export type SynthesisApplyRequest = {
  preview_id: string;
  token: string;
};

export type SynthesisApplyResponse = {
  message: string;
  key: string;
  path: string;
  rel_path: string;
  journal_id: string;
};

export type SynthesisFetchRequest = {
  url: string;
  init: RequestInit;
};
