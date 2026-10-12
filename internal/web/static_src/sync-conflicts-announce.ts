import type { StatusKind } from "./status";
import type { SyncMergeEditor } from "./sync-merge-editor";
import type { SyncFailure } from "./sync-requests";
import type { SyncResolvedResult } from "./api";


// SyncAnnouncement is a sentence a re-read carries onto the panel's status line,
// for the reason in the header: the node it would otherwise be said in is one of
// the ones the re-read throws away.
export type SyncAnnouncement = {
  message: string;
  kind: StatusKind;
};

// MergeSafety is what a re-read can honestly promise about the merges on screen:
// nothing at all when there are none, that they come back when every one of them is
// kept in this browser, and that they are gone otherwise.
export type MergeSafety = "none" | "kept" | "lost";

// mergeSafetyOf asks the editors rather than asking whether this browser has storage.
// A browser whose quota is full *has* storage and refuses the write anyway, and a
// page served without a vault identity keeps nothing — both answer "there is
// somewhere to keep one" and lose the text, so the question is what the last keep
// actually did. It flushes first, or a merge typed a keystroke ago would be judged
// on a write whose debounce had not fired.
//
// It takes the editors rather than a card, because a reload replaces every card on
// the page: the merge at risk is often on one nobody clicked.
export const mergeSafetyOf = (editors: SyncMergeEditor[]): MergeSafety => {
  if (editors.length === 0) {
    return "none";
  }
  let kept = true;
  for (const editor of editors) {
    editor.flush();
    kept = editor.kept() && kept;
  }
  return kept ? "kept" : "lost";
};

// staleAnnouncement is what a plan that has to be read again says.
//
// The re-read seeds each merge editor from the copy in this browser, so the promise
// is true exactly when that copy exists. Telling somebody their merge is still there
// before replacing the editor it was typed in is the one wording here that could
// lose the text it is about, which is why it is not said on the strength of storage
// being present.
export const staleAnnouncement = (failure: SyncFailure, merges: MergeSafety): SyncAnnouncement => ({
  message: `${reReadOpening(failure)} ${mergeClause(merges)}`,
  kind: "is-warning",
});

// The two refusals `stalePreview` covers share a remedy and not a cause, so they
// share the re-read and not the first sentence. `preview_stale` is a fact about the
// vault: something moved under the decision. `preview_required` is a fact about this
// page's receipt — unknown, already spent, or past its five minutes — and saying the
// vault moved on there is a claim about somebody's notes that is not true, which
// sends them looking for a change nobody made.
const reReadOpening = (failure: SyncFailure): string =>
  failure.code === "preview_stale"
    ? "This vault moved on while the decision was being made, so nothing was applied."
    : "This review is too old to apply, so nothing was applied.";

const mergeClause = (merges: MergeSafety): string => {
  if (merges === "none") {
    return "Both versions are being read again.";
  }
  if (merges === "kept") {
    return "Both versions are being read again; any merge you composed is still here.";
  }
  return `Both versions are being read again, and a ${lostMergeClause}`;
};

// The one sentence about a merge this browser could not keep, said in two places
// because both of them replace the editor it is about.
const lostMergeClause =
  "merge you composed has to be typed again: this page could not keep a copy of it.";

// failureAnnouncement carries a refusal that is not about staleness through the
// re-read, and adds the merge clause only when there is a loss to report.
//
// The refusal is the engine's own sentence about the vault, and "your merge is still
// here" beside it is noise — but a merge this browser could not keep is about to be
// destroyed by the reload this failure triggers, and nothing else on the page would
// ever say so.
export const failureAnnouncement = (message: string, merges: MergeSafety): SyncAnnouncement => ({
  message: merges === "lost" ? `${ended(message)} A ${lostMergeClause}` : message,
  kind: "is-error",
});

// resolvedSentence reports what durably happened, in the order it matters.
//
// The summary is the server's and is not restated. It is written from the durable
// facts — which commits there were, and whether the peer was acknowledged — so a
// page adding its own sentence about the acknowledgment printed the same claim
// twice in slightly different words. The clause survives for the one answer that
// carries no summary at all, where nothing else would say it: the acknowledgment is
// the difference between a conflict that is finished and one whose package the peer
// will keep offering until a receipt exists.
export const resolvedSentence = (result: SyncResolvedResult): string => {
  const parts = [result.summary || "The decision was applied."];
  if (result.summary === "" && !result.acknowledged) {
    parts.push(
      "The other replica has not been told yet, so it still counts this change as waiting."
    );
  }
  for (const abandoned of result.abandoned) {
    parts.push(`An earlier unfinished resolution of ${abandoned.package_id} was dropped: ${abandoned.reason}`);
  }
  return parts.join(" ");
};

// join puts an announcement in front of what the panel says anyway, and finishes
// it first: an announcement is often the engine's own sentence, which stops
// wherever the error did, and an unpunctuated one runs into the count behind it as
// though the two were one claim.
export const join = (announced: string | undefined, sentence: string): string =>
  announced === undefined || announced === "" ? sentence : `${ended(announced)} ${sentence}`;

const ended = (text: string): string => (/[.!?…]$/.test(text) ? text : `${text}.`);

export const emptyMessage = (count: number): string =>
  count === 0
    ? "Nothing is waiting for a decision."
    : `${count === 1 ? "1 change is" : `${count} changes are`} waiting for a decision.`;
