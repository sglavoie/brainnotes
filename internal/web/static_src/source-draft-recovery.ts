import type { SourceDraft } from "./source-draft";

// Deciding what a kept draft means, and what to say about it.
//
// Every function here is pure: drafts and one reading of the note go in, an
// offer comes out. That is deliberate — the rules are the part worth pinning
// with tests, and the part that must never quietly change into "and then submit
// it". Nothing in this module writes, fetches, or touches the DOM.
//
// The one rule the whole file exists to enforce: **a recovered draft is never
// applied by itself.** The offer says what is on hand and what the vault holds;
// a person picks.

export type DraftOfferKind =
  // resume — the note is exactly where the draft left it. Putting the text back
  // is the whole remedy: the save that follows is an ordinary one.
  | "resume"
  // stale — the note moved on since the draft was written. The text is still
  // worth having, but it is not based on what the vault holds now, so restoring
  // it has to put the two versions side by side rather than arm a save.
  | "stale"
  // missing — the note is no longer in the vault. There is nothing to save over
  // and nothing to compare against, so the only forward choice is a new note.
  | "missing"
  // unreachable — nobody answered for the vault. Nothing can be compared, so
  // nothing is claimed: the text is shown, kept, and left alone.
  | "unreachable";

export type DraftOfferSource = "storage" | "editor";

export type DraftOffer = {
  kind: DraftOfferKind;
  draft: SourceDraft;
  // sameTab is false when the draft was left by a different tab — which is a
  // fact worth saying out loud, because "your unsaved text" about words somebody
  // typed in a window they still have open is a different claim.
  sameTab: boolean;
  source: DraftOfferSource;
  // vaultContent and vaultRevision are what the note holds now. They are empty
  // for the two kinds decided without a reading, and for "stale" they are what
  // the conflict panel is filled from.
  vaultContent: string;
  vaultRevision: string;
};

// VaultReading is one answer about the note, in the three shapes that change
// what a draft means. "missing" and "unreachable" are deliberately not one
// "failed" case: one of them is a fact about the vault and the other is the
// absence of any fact at all, and telling somebody their note is gone because a
// proxy hiccupped would be the worse of the two mistakes.
export type VaultReading =
  | { kind: "loaded"; revision: string; content: string }
  | { kind: "missing" }
  | { kind: "unreachable" };

export type DraftDecision = {
  offer: DraftOffer | null;
  // superseded names drafts the vault already holds the text of, by draft id.
  // They are dropped rather than offered: showing somebody a choice between two
  // identical texts is a question with no content, and refusing to clear them
  // would mean every save leaves a permanent offer behind.
  //
  // It is only ever populated from a reading that actually happened. A vault
  // nobody could reach supersedes nothing.
  superseded: string[];
};

export const decideDraftRecovery = (input: {
  drafts: SourceDraft[];
  ownDraftID: string;
  vault: VaultReading;
}): DraftDecision => {
  const ordered = [...input.drafts].sort((left, right) => right.updatedAt - left.updatedAt);
  if (input.vault.kind !== "loaded") {
    const draft = ordered[0];
    return {
      offer: draft ? offerFor(input.vault.kind, draft, input.ownDraftID, "", "") : null,
      superseded: [],
    };
  }

  const { revision, content } = input.vault;
  const superseded: string[] = [];
  let offer: DraftOffer | null = null;
  for (const draft of ordered) {
    if (draft.content === content) {
      superseded.push(draft.draftID);
      continue;
    }
    // Only the newest survivor is offered. The rest stay in storage and are
    // decided again after this one is answered, so two tabs' drafts are two
    // questions asked in turn rather than one panel trying to be about both.
    offer ??= offerFor(
      draft.baseRevision === revision ? "resume" : "stale",
      draft,
      input.ownDraftID,
      content,
      revision
    );
  }
  return { offer, superseded };
};

// offerFromEditor is the save-refusal door: the note turned out to be gone while
// the text was on screen, so the offer is about what the editor is holding
// rather than about anything read back from storage.
export const offerFromEditor = (draft: SourceDraft): DraftOffer => ({
  kind: "missing",
  draft,
  sameTab: true,
  source: "editor",
  vaultContent: "",
  vaultRevision: "",
});

const offerFor = (
  kind: DraftOfferKind,
  draft: SourceDraft,
  ownDraftID: string,
  vaultContent: string,
  vaultRevision: string
): DraftOffer => ({
  kind,
  draft,
  sameTab: draft.draftID === ownDraftID,
  source: "storage",
  vaultContent,
  vaultRevision,
});

export type DraftOfferCopy = {
  heading: string;
  lede: string;
  restore: boolean;
  recreate: boolean;
};

// draftOfferCopy is every sentence this feature says about a draft, in one
// place.
//
// They are written under one constraint: **not one of them may read as a save.**
// The text being described has never left the browser — it is not on the Pi, it
// is not in the vault's history, and it is not in a backup. A person who reads
// "kept" as "saved" will close the tab, and the wording is the only thing
// standing between them and that.
export const draftOfferCopy = (offer: DraftOffer, when: string): DraftOfferCopy => {
  const rel = offer.draft.relPath || "this note";
  const kept =
    offer.source === "editor"
      ? "The text in the editor has not been written anywhere."
      : `${offer.sameTab ? "This tab" : "Another tab"} kept this text in this browser ${when}, and it never reached the vault.`;

  switch (offer.kind) {
    case "resume":
      return {
        heading: "Unsaved text kept in this browser",
        lede: `${kept} ${rel} has not changed since, so putting it back leaves you where you were. It is still not saved until you save it.`,
        restore: true,
        recreate: false,
      };
    case "stale":
      return {
        heading: "Unsaved text kept in this browser, and the note has moved on",
        lede: `${kept} ${rel} has changed since, so this text is not based on what the vault holds now. Putting it back shows you both versions and writes neither.`,
        restore: true,
        recreate: false,
      };
    case "missing":
      return {
        heading: "The note is gone, and this text is still here",
        lede: `${kept} ${rel} is no longer in the vault — renamed, moved, or deleted — so there is nothing to save over. You can write it back as a new note at that path, or discard it.`,
        restore: false,
        recreate: true,
      };
    default:
      return {
        heading: "Unsaved text kept in this browser",
        lede: `${kept} The vault could not be reached, so nothing here has been compared against it and nothing has been thrown away. This text will still be here when the note can be loaded again.`,
        restore: false,
        recreate: false,
      };
  }
};

// describeDraftTime says when, without a locale.
//
// Intl.DateTimeFormat would put the machine's locale and time zone into a string
// a test has to assert on, and the precision is not what is being asked for
// anyway: the question behind "when was this kept" is "is this the thing I was
// just typing, or something I have forgotten about".
export const describeDraftTime = (updatedAt: number, now: number): string => {
  const seconds = Math.max(0, Math.round((now - updatedAt) / 1000));
  if (seconds < 45) {
    return "moments ago";
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `${count(minutes, "minute")} ago`;
  }
  const hours = Math.round(minutes / 60);
  if (hours < 24) {
    return `${count(hours, "hour")} ago`;
  }
  return `${count(Math.round(hours / 24), "day")} ago`;
};

const count = (value: number, unit: string): string =>
  `${value} ${unit}${value === 1 ? "" : "s"}`;
