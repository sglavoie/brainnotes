export type OpenNoteRequest = {
  key: string;
  token: string;
};

export type NoteInsightResult = {
  ok: boolean;
  code?: string;
  message?: string;
  enabled: boolean;
  key: string;
  output: string;
  rendered_html: string;
};

export type NoteInsightRequest = {
  key: string;
  prompt: string;
  token: string;
};

export type NoteKeyMutationRequest = {
  key: string;
  token: string;
};

export type CreateNoteRequest = NoteKeyMutationRequest & {
  target: string;
};

export type FrontmatterSetRequest = NoteKeyMutationRequest & {
  property: string;
  op: "=" | "+=" | "-=";
  value: string;
};

export type FrontmatterUnsetRequest = NoteKeyMutationRequest & {
  property: string;
};

export type CaptureRequest = {
  captureID?: string;
  text: string;
  token: string;
};

export type CaptureResult = {
  path?: string;
  ok: boolean;
  code?: string;
  message: string;
  detail: string;
  rel_path: string;
};

export type OpenNoteResult =
  | {
      ok: true;
    }
  | {
      ok: false;
      message: string;
    };

export type ManagementSuccessResult = {
  ok: true;
  message?: string;
  key?: string;
  path?: string;
  rel_path?: string;
  journal_id?: string;
};

export type ManagementErrorResult = {
  ok: false;
  code?: string;
  message?: string;
};

export type ManagementResult = ManagementSuccessResult | ManagementErrorResult;
