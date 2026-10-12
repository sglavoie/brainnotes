import {
  draftStatusMessages,
  hideDraftOffer,
  setDraftStatus,
  showDraftOffer,
} from "./source-draft-panel";
import {
  decideDraftRecovery,
  describeDraftTime,
  offerFromEditor,
  type DraftOffer,
  type VaultReading,
} from "./source-draft-recovery";
import {
  draftSchemaVersion,
  type SourceDraft,
} from "./source-draft";
import { newSaveID } from "./source-save";

import type { DraftKeeperOptions } from "./source-editor-draft-types";
import { createSlotHold, ownSlotKeeps, reviseStanding, type DraftWriter } from "./source-editor-draft-writer";

// The offering half: which draft is on screen, what the three buttons do, and
// the reading each decision was made against.
type OfferPresenter = {
  present(kept: SourceDraft[], vault: VaultReading): void;
  revised(content: string, revision: string): void;
  offerRecreate(content: string): void;
  onRestore(): void;
  onRecreate(): void;
  onDiscard(): void;
};

export const createOfferPresenter = (options: DraftKeeperOptions, writer: DraftWriter): OfferPresenter => {
  const { store, elements } = options;
  const ownID = store?.draftID ?? "";
  const slot = createSlotHold(store, writer);
  let offer: DraftOffer | null = null;
  let reading: VaultReading = { kind: "unreachable" };

  const show = (next: DraftOffer | null): void => {
    offer = next;
    if (!next) {
      hideDraftOffer(elements);
      return;
    }
    showDraftOffer(elements, next, describeDraftTime(next.draft.updatedAt, options.now()));
  };

  // reoffer re-decides from what is left in storage, against the same reading,
  // so a second tab's draft is a second question rather than one that
  // disappears with the first. This tab's own slot is excluded once it has been
  // answered for, because by then it holds what is on screen.
  const reoffer = (): void => {
    const kept = (store?.list() ?? []).filter(
      (draft) => slot.unanswered() || draft.draftID !== ownID
    );
    const decision = decideDraftRecovery({ drafts: kept, ownDraftID: ownID, vault: reading });
    slot.drop(decision.superseded);
    show(decision.offer);
  };

  const panel = { show, drop: slot.drop, reoffer };

  return {
    ...createOfferActions(options, writer, {
      current: () => offer,
      ownHeld: slot.unanswered,
      declined: slot.declined,
      chosen: slot.release,
      show,
      reoffer,
    }),

    present(kept: SourceDraft[], vault: VaultReading): void {
      reading = vault;
      const decision = decideDraftRecovery({ drafts: kept, ownDraftID: ownID, vault });
      // Claimed before the drops, because a superseded own record takes the
      // hold straight back off again.
      slot.claim(kept);
      slot.drop(decision.superseded);
      show(decision.offer);
    },

    revised(content: string, revision: string): void {
      reading = { kind: "loaded", revision, content };
      reviseStanding(offer, ownID, reading, panel);
    },

    offerRecreate(content: string): void {
      // The save was refused because the note is gone, which is a fresh reading
      // of the vault and replaces the one the load made. Leaving the old one in
      // place would have a later discard re-offer another tab's draft for this
      // note as "the note has not changed since", about a note that is not there.
      reading = { kind: "missing" };
      show(offerFromEditor(syntheticDraft(options, content)));
    },
  };
};

// The three buttons. They are separated from the state above because each of
// them is the same short sentence — drop what the answer settles, tell the
// presenter which kind of answer it was, take the panel down — and nothing else
// in this file needs to know which of them a person pressed.
type OfferControls = {
  current(): DraftOffer | null;
  // ownHeld is asked by the one button that writes to the vault: recreate leaves
  // the page, so it has to know whether this tab's slot is still carrying a
  // question it would be taking the only copy of.
  ownHeld(): boolean;
  declined(decided: DraftOffer): void;
  chosen(): void;
  show(next: DraftOffer | null): void;
  reoffer(): void;
};

// restoreOffer is the restore button, out of line because what it does to the
// slot takes longer to explain than to do.
const restoreOffer = (
  options: DraftKeeperOptions,
  writer: DraftWriter,
  controls: OfferControls,
  restored: DraftOffer
): void => {
  const { store, elements, host } = options;
  // Asked before the answer clears it. Restoring another tab's draft spends this
  // tab's slot on the text being restored, and if that slot was still carrying an
  // unanswered record of its own, that record is what the flush below writes
  // over. There is no second slot to move it to, so the only thing to be done
  // about it is to say so.
  const spent = restored.draft.draftID !== store?.draftID && controls.ownHeld();
  host.restore(restored);
  // Recovery is over whichever record this was. The restored text is on screen
  // and unsaved, so it is what an interruption would take, and the slot goes to
  // it — including when the offer was another tab's, where a hold that never
  // ended would leave this text kept nowhere at all.
  controls.chosen();
  controls.show(null);
  // The sentence goes up first and the writer is told it has been said, so the
  // write that follows stays quiet on success and still speaks on a refusal. The
  // other order puts "the kept text is back" over "this browser would not keep a
  // copy — its storage is full", which is the one message this feature cannot
  // afford to swallow.
  setDraftStatus(
    elements,
    spent ? draftStatusMessages.replaced : draftStatusMessages.restored,
    "is-warning"
  );
  writer.spoke();
  writer.flush();
  // The other tab's record goes only once this tab's own slot is holding the
  // restored text — the same bytes in two places for an instant rather than in
  // none — and the slot is asked for *those bytes*, not merely for something:
  // the flush can be refused by a full store, and when the slot already held this
  // tab's own unanswered record a refusal leaves that older record sitting there
  // looking exactly like a write that landed. Dropping the source on the strength
  // of one that never happened is how a restore ends with the recovered text in
  // the DOM and nowhere else. Its own slot is deliberately not dropped at all:
  // the flush rewrites that very key, and on a full store dropping first is what
  // turns an overwrite the browser allows into a new key it refuses.
  if (restored.draft.draftID !== store?.draftID && ownSlotKeeps(store, restored.draft.content)) {
    store?.drop(restored.draft.draftID);
  }
};

const createOfferActions = (
  options: DraftKeeperOptions,
  writer: DraftWriter,
  controls: OfferControls
): Pick<OfferPresenter, "onRestore" | "onRecreate" | "onDiscard"> => {
  const { store, elements, host } = options;
  // The buttons stay on screen while a create is in flight, and that request is
  // what decides whether this page still exists. A second press would send a
  // second create; a discard landing in the gap would answer the offer whose
  // callback is about to act on the answer it no longer has, against a `pending`
  // captured before the await.
  let creating = false;
  // The two buttons that change what the editor holds are declined whole while a
  // request is in flight, rather than half-applied: see `DraftHost.saving` for
  // the save that answers over the top of a restore, and `creating` for the one
  // this panel sends itself. Discard is deliberately not one of them; see below.
  const busy = (): boolean => creating || host.saving();
  return {
    onRestore(): void {
      const restored = controls.current();
      if (!restored || busy()) {
        return;
      }
      restoreOffer(options, writer, controls, restored);
    },

    onRecreate(): void {
      const pending = controls.current();
      if (!pending || busy()) {
        return;
      }
      creating = true;
      void recreate(options, pending, () => {
        writer.stop();
        // The editor-sourced offer is text on screen, and its synthetic draft
        // carries this tab's own id — so dropping by that id while a previous
        // session's record is still unanswered in the slot would destroy the
        // one copy of text nobody has been asked about yet. The recreated
        // bytes are a file now; the unanswered ones are not.
        if (pending.source === "storage" || !controls.ownHeld()) {
          store?.drop(pending.draft.draftID);
        }
        controls.chosen();
        controls.show(null);
      }).finally(() => {
        creating = false;
      });
    },

    onDiscard(): void {
      const discarded = controls.current();
      // Deliberately not `busy()`. A discard re-pins no revision and puts nothing
      // on screen for a late answer to land on top of, and it is the only thing
      // that ends a hold — so declining it for the duration of a request would
      // mean a save that never answers, which is a dropped connection or a Pi
      // that went away mid-request, leaves this tab keeping nothing at all for
      // the rest of the session with the one button that could fix that inert.
      // The create is still excluded: its callback acts on a `pending` captured
      // before the await.
      if (!discarded || creating) {
        return;
      }
      // An offer raised from the editor is about text that is still on screen
      // and still unsaved, so "no thank you" there means stop asking — not stop
      // keeping. Dropping the record would take the safety copy away from text
      // the person is still looking at, and the line would say it had been
      // discarded while the next keystroke wrote it straight back.
      if (discarded.source === "storage") {
        store?.drop(discarded.draft.draftID);
      }
      controls.declined(discarded);
      controls.show(null);
      setDraftStatus(elements, draftStatusMessages.discarded);
      controls.reoffer();
      // A discard that answered another tab's record leaves this tab's own still
      // standing on the slot — and the sentence the hold had put on the line,
      // "what you are typing now is not being kept", has just been replaced by
      // this one. The writer is told its piece is gone, or the next keep sees it
      // as already said and the person goes on typing under a confirmation, with
      // nothing kept for the rest of the session.
      if (controls.ownHeld()) {
        writer.unannounce();
      }
      // The slot is free again, so whatever is in the editor now gets kept.
      writer.keep();
    },
  };
};

const syntheticDraft = (options: DraftKeeperOptions, content: string): SourceDraft => ({
  version: draftSchemaVersion,
  vaultID: options.config.vaultID,
  noteKey: options.config.key,
  relPath: options.config.relPath,
  baseRevision: options.host.baseRevision(),
  draftID: options.store?.draftID ?? "",
  content,
  updatedAt: options.now(),
});

// recreate writes the recovered text to the path this page was opened for, as a
// note that does not exist yet.
//
// It is the only request this module makes, and it is deliberately a *create*
// rather than a save: there is no revision to plan against, so the server
// answers "something is already there" rather than overwriting whatever took the
// path in the meantime.
const recreate = async (
  options: DraftKeeperOptions,
  offer: DraftOffer,
  done: () => void
): Promise<void> => {
  const { host, elements } = options;
  host.setStatus("Writing the kept text back as a new note…", "is-loading");
  try {
    const response = await options.client.create({
      relPath: offer.draft.relPath,
      content: offer.draft.content,
      saveID: newSaveID(),
      owner: options.claims?.owner,
      attachments: options.claims?.pending(),
    });
    options.claims?.committed(response.attachments);
    if (!response.ok) {
      host.setStatus(response.message || "The note could not be created.", "is-error");
      return;
    }
    done();
    setDraftStatus(elements, "");
    host.setStatus(response.message || "Created.", "is-success");
    // The page this tab is on is for a note that no longer exists, so there is
    // nothing here to stay for. The navigation is the direct consequence of the
    // button, and it happens only after the draft is dropped — leaving on a
    // successful write with the copy still in storage would offer it back later
    // as unsaved text that is, by then, a file.
    options.navigate(response.path);
  } catch (error: unknown) {
    host.setStatus(
      error instanceof Error && error.message !== ""
        ? error.message
        : "The note could not be created.",
      "is-error"
    );
  }
};
