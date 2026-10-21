import { setStatusText, type StatusKind } from "./status";
import { draftOfferCopy, type DraftOffer } from "./source-draft-recovery";

// The draft half of the page: one status line and one recovery panel.
//
// It is a separate module from the conflict panel's handling in the controller
// for the same reason the template gives it a separate status line — the two are
// about different subjects. The conflict panel is about two versions the *vault*
// has seen. This panel is about text the vault has never seen at all, and every
// sentence on it has to keep saying so.

export type DraftPanelElements = {
  draftStatus: HTMLElement | null;
  recovery: HTMLElement | null;
  recoveryHeading: HTMLElement | null;
  recoveryLede: HTMLElement | null;
  recoverySource: HTMLElement | null;
  recoveryRestore: HTMLButtonElement | null;
  recoveryRecreate: HTMLButtonElement | null;
  recoveryDiscard: HTMLButtonElement | null;
};

// draftStatusMessages is every sentence the draft line can hold.
//
// None of them contains the word "saved", and that is not a style choice. This
// server usually runs on a Raspberry Pi somebody else's laptop is talking to,
// and the distance between "kept" and "saved" is the distance between text that
// survives the tab closing and text that does not.
export const draftStatusMessages = {
  kept: "Draft kept in this browser. Save to write it to the vault.",
  unavailable:
    "Draft only in this tab; browser storage is unavailable. Save before leaving.",
  refused:
    "Draft only in this tab; browser storage is full. Save before leaving.",
  // There is one slot per tab, and after a reload the recovered text is in it.
  // Text typed on top of an unanswered offer cannot go there as well, and a
  // person typing into a page that is quietly keeping nothing is exactly who
  // this feature exists for — so the page says which of the two it is holding.
  deferred:
    "The recovered text is still being kept in this browser, so what you are typing now is not. Restore or discard it and this tab will start keeping the new text.",
  restored: "The kept text is back in the editor. It is still not in the vault until you save.",
  // The same answer, when taking it cost the other text. There is one slot per
  // tab: restoring another tab's draft puts it in this tab's slot, and whatever
  // this tab was keeping there — a previous session's record nobody had answered
  // for yet — is overwritten by it. Saying so is the whole of what can be done
  // about it, and saying nothing would be a copy quietly disappearing at the one
  // moment somebody was told their text was safe.
  replaced:
    "The kept text is back in the editor. It is still not in the vault until you save. This tab keeps one copy, so the other text it had kept from an earlier session is gone.",
  discarded: "The kept text has been discarded. The editor is unchanged.",
  peerSaved: "Another tab saved this note. This tab is still showing the version it loaded.",
} as const;

export const setDraftStatus = (
  elements: DraftPanelElements,
  message: string,
  kind: StatusKind = ""
): void => {
  setStatusText(elements.draftStatus, message, kind);
};

// showDraftOffer fills the panel and nothing else. It arms no save, moves no
// revision and writes to no store — every one of those belongs to the click it
// is waiting for.
export const showDraftOffer = (
  elements: DraftPanelElements,
  offer: DraftOffer,
  when: string
): void => {
  if (!elements.recovery) {
    return;
  }
  const copy = draftOfferCopy(offer, when);
  setText(elements.recoveryHeading, copy.heading);
  setText(elements.recoveryLede, copy.lede);
  setText(elements.recoverySource, offer.draft.content);
  // The kind is published on the element because it is what the panel *means*,
  // and a test — jsdom or browser — should be able to read that back without
  // matching prose.
  elements.recovery.setAttribute("data-source-recovery-kind", offer.kind);
  toggle(elements.recoveryRestore, copy.restore);
  toggle(elements.recoveryRecreate, copy.recreate);
  toggle(elements.recoveryDiscard, true);
  elements.recovery.hidden = false;
};

export const hideDraftOffer = (elements: DraftPanelElements): void => {
  if (!elements.recovery) {
    return;
  }
  elements.recovery.hidden = true;
  elements.recovery.removeAttribute("data-source-recovery-kind");
  // The text goes with the panel. Leaving a copy of somebody's discarded draft
  // in a hidden element keeps it on the page — in the DOM, in a "save page as",
  // in the next person's view of the screen — after they asked for it to be
  // gone.
  setText(elements.recoverySource, "");
};

const setText = (element: HTMLElement | null, value: string): void => {
  if (element) {
    element.textContent = value;
  }
};

const toggle = (element: HTMLButtonElement | null, shown: boolean): void => {
  if (element) {
    element.hidden = !shown;
  }
};
