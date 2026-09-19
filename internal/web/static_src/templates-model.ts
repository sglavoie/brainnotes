import type { PropertyConfigDiffLine, TemplateDirStatus, TemplateSummary } from "./api";

/**
 * TemplateDraft is what the editor holds: the file it was opened on, the bytes
 * it started with, and the revision those bytes were read at.
 *
 * `revision` is the whole concurrency story. It is empty for a file that does
 * not exist yet, which is how a create says so, and it is what the server pins
 * the write to — so a template edited in another window between the read and the
 * save is a refusal rather than an overwrite.
 */
export type TemplateDraft = {
  name: string;
  content: string;
  revision: string;
  /** created is true while the draft names a file the directory does not hold. */
  created: boolean;
};

export const emptyTemplateDraft = (): TemplateDraft => ({
  name: "",
  content: "",
  revision: "",
  created: false,
});

export const templateDraftOf = (summary: TemplateSummary): TemplateDraft => ({
  name: summary.name,
  content: summary.content,
  revision: summary.revision,
  created: false,
});

export const newTemplateDraft = (name: string, content: string): TemplateDraft => ({
  name,
  content,
  revision: "",
  created: true,
});

/**
 * Explains why a draft cannot be saved, or "" when it can.
 *
 * The name policy itself is the server's and is deliberately not restated here:
 * it depends on the configured extension, on what the directory already holds,
 * and on whether the resolved destination is still the file that was named. This
 * refuses only what no server could accept — a template with no name at all —
 * so the browser can never disagree with the vault about what a legal name is.
 */
export const templateSaveError = (draft: TemplateDraft): string => {
  if (draft.name.trim() === "") {
    return "Name the template before saving it.";
  }
  return "";
};

/**
 * suggestTemplateName appends the configured extension to a bare name.
 *
 * `bn new --template daily` resolves by stem, so a person naming one thinks in
 * stems while the file needs a suffix. Anything that already carries a dot is
 * left exactly as typed, so a deliberate `.md` beside a configured `.markdown`
 * still reaches the server, which is the only thing that knows both are legal.
 */
export const suggestTemplateName = (raw: string, extension: string): string => {
  const name = raw.trim();
  if (name === "" || extension === "" || name.includes(".")) {
    return name;
  }
  return name + extension;
};

/**
 * TemplatePendingSave is one issued, not-yet-redeemed receipt.
 *
 * `content` is the bytes the receipt was planned against, kept here rather than
 * re-read from the editor when the write lands: the server writes what the
 * preview described, so settling the draft from anything else would show a
 * person bytes the vault does not hold and call them saved.
 */
export type TemplatePendingSave = {
  name: string;
  op: "save" | "delete";
  content: string;
  previewID: string;
  diff: PropertyConfigDiffLine[];
  created: boolean;
};

export const templatePendingSave = (
  name: string,
  op: "save" | "delete",
  content: string,
  result: { preview_id: string; diff: PropertyConfigDiffLine[]; created: boolean }
): TemplatePendingSave => ({
  name,
  op,
  content,
  previewID: result.preview_id,
  diff: [...result.diff],
  created: result.created,
});

export const templateConfirmMessage = (pending: TemplatePendingSave): string => {
  if (pending.op === "delete") {
    return `Delete "${pending.name}"? Review the change, then confirm.`;
  }
  return pending.created
    ? `Create "${pending.name}"? Review the change, then confirm.`
    : `Overwrite "${pending.name}"? Review the change, then confirm.`;
};

export const templateSavedMessage = (pending: TemplatePendingSave): string => {
  if (pending.op === "delete") {
    return `Deleted "${pending.name}". Undo it from Recent changes.`;
  }
  return pending.created ? `Created "${pending.name}".` : `Saved "${pending.name}".`;
};

export const templateNoChangeMessage = (name: string): string =>
  `"${name}" already holds exactly those bytes.`;

/**
 * Describes the configured directory in the two terms that decide whether
 * editing a template here is safe.
 *
 * Both halves are read from the server rather than assumed, because a vault that
 * points `templates.dir` somewhere unusual gets a directory whose files would be
 * walked as notes, or would never reach a peer — and a person needs to be told
 * that before they put a skeleton in it, not after.
 */
export const templateDirNotice = (dir: string, status: TemplateDirStatus): string => {
  const where = dir === "" ? "The templates directory" : `${dir}/`;
  if (status.excluded_from_notes && status.portable) {
    return `${where} is excluded from note queries and synchronizes with your other vaults.`;
  }
  if (!status.excluded_from_notes && !status.portable) {
    return `${where} is indexed as ordinary notes and does not synchronize.`;
  }
  return status.excluded_from_notes
    ? `${where} is excluded from note queries but does not synchronize.`
    : `${where} synchronizes but is indexed as ordinary notes.`;
};
