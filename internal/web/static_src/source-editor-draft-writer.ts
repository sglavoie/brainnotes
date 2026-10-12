import {
  draftStatusMessages,
  setDraftStatus,
} from "./source-draft-panel";
import {
  decideDraftRecovery,
  type DraftOffer,
  type VaultReading,
} from "./source-draft-recovery";
import {
  type DraftWriteOutcome,
  type SourceDraft,
  type SourceDraftStore,
} from "./source-draft";
import type { SourceEditorElements } from "./source-editor-elements";

import type { DraftKeeperOptions } from "./source-editor-draft-types";
import { defaultDelayMs } from "./source-editor-draft-types";

// The writing half: the debounce, whether "kept" is currently on screen, and
// nothing about the panel.
export type DraftWriter = {
  keep(): void;
  flush(): void;
  adopted(): void;
  cancel(): void;
  // stop ends the keeping for good, and exists for exactly one exit: the
  // recreate button writes the kept text to the vault as a new note and then
  // navigates away, while this editor's own state never learns it — the surface
  // still differs from the content that was loaded, so it is still dirty.
  // Without this, the unload flush on the way out would put the copy straight
  // back into storage after the drop, leaving a record of unsaved text for bytes
  // that are, by then, a file.
  stop(): void;
  // hold says an unanswered offer is standing on this tab's own slot. While one
  // is, the writer neither drops nor overwrites that slot: the record behind the
  // offer is the only copy of that text, and neither "the editor matches the
  // vault" nor "there is newer text now" is an answer to a question nobody has
  // answered yet. An offer about another tab's record holds nothing, because
  // nothing here writes to another tab's slot.
  hold(held: boolean): void;
  // holding is the one answer to "is an unanswered record still standing on this
  // tab's slot", and everything that needs to know asks it here rather than
  // remembering its own copy. It is a question rather than a flag because the
  // record can go while nobody is looking — another tab on another note, out of
  // room, evicts the oldest draft on the origin — and a second remembered copy
  // of the answer is a second thing to be wrong.
  holding(): boolean;
  // unannounce says the draft line now carries something else, so the next keep
  // must state its piece again rather than assume it is still on screen.
  unannounce(): void;
  // spoke is its opposite: the caller has just put a sentence on the line that
  // says what the next successful write would say, so that write stays quiet. A
  // refusal still speaks, because it says something else entirely.
  spoke(): void;
};

export const createDraftWriter = (options: DraftKeeperOptions): DraftWriter => {
  const delayMs = options.delayMs ?? defaultDelayMs;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let announced = false;
  let stopped = false;
  let held = false;

  const cancel = (): void => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  };

  const hold = (next: boolean): void => {
    // The two states have different sentences, so crossing between them un-says
    // whichever one is on screen. Without this, "what you are typing now is not
    // being kept" would leave `announced` true and the first genuine "kept in
    // this browser only" after the hold ended would be swallowed as something
    // already said.
    if (held !== next) {
      announced = false;
    }
    held = next;
  };

  // A hold is only ever worth as much as the record underneath it. Going on
  // holding for a record that is gone is the worst state this module has —
  // nothing kept, and a line saying the recovered text still is — so the hold
  // ends the moment anybody asks, rather than at an answer that may never come.
  // It does not come back: the next write puts the live text in that slot, and
  // the slot is then this editor's.
  const holding = (): boolean => {
    if (held && !ownSlotHolds(options.store)) {
      hold(false);
    }
    return held;
  };

  const write = (): void => {
    if (stopped) {
      return;
    }
    // Asked before `announced` is read, not in the argument list beside it:
    // ending a hold un-says the sentence the hold put on the line, and reading
    // `announced` first passes the stale answer — the write then stays quiet and
    // "what you are typing now is not being kept" stays on screen over a slot
    // that now holds exactly what they are typing.
    const heldNow = holding();
    announced = writeDraft(options, announced, heldNow);
  };

  return {
    keep(): void {
      if (timer !== null) {
        return;
      }
      timer = setTimeout(() => {
        timer = null;
        write();
      }, delayMs);
    },

    flush(): void {
      cancel();
      write();
    },

    adopted(): void {
      cancel();
      adoptDraft(options, holding());
      announced = false;
    },

    cancel,
    hold,
    holding,

    unannounce(): void {
      announced = false;
    },

    spoke(): void {
      announced = true;
    },

    stop(): void {
      stopped = true;
      cancel();
    },
  };
};

// ownSlotHolds asks whether this tab's own slot still holds anything at
// all. It is a list rather than a remembered flag because the disappearance is
// somebody else's doing: a store under quota pressure evicts the oldest draft on
// the whole origin, and the tab losing its record is not the tab that wrote.
const ownSlotHolds = (store: SourceDraftStore | null): boolean =>
  store === null || store.list().some((draft) => draft.draftID === store.draftID);

// ownSlotKeeps is the stronger question, and the one a restore has to ask: does
// this tab's slot hold *these* bytes.
//
// "Is there anything in the slot" cannot answer it. The slot a restore writes to
// is very often already occupied — by this tab's own unanswered record from an
// earlier session — and a browser that refuses the write leaves that older
// record exactly where it was. A non-empty slot would then read as a write that
// landed, and the record the text was restored *from* would be dropped on the
// strength of it, leaving the only copy of it in the DOM.
export const ownSlotKeeps = (store: SourceDraftStore | null, content: string): boolean =>
  store !== null &&
  store.list().some((draft) => draft.draftID === store.draftID && draft.content === content);

// adoptDraft is what the writer's `adopted` does to storage and to the line, out
// of line because its reasoning is longer than its code.
//
// It runs when the vault is known to hold what the editor shows *because this
// editor put it there* — a save, or taking over the other writer's version from
// the conflict panel. Dropping the draft there is the only way the offer ever
// ends by itself, and it is safe for exactly that reason: the text is not being
// thrown away, it is being superseded by a copy in a file. A load is deliberately
// not one of these; see the editor controller's adopt.
const adoptDraft = (options: DraftKeeperOptions, held: boolean): void => {
  const { store, elements } = options;
  // Not while an offer is unanswered. `drop()` takes this tab's own slot, and
  // while an offer about that slot is up, the slot is the only copy of the text
  // on the panel. A save is this editor's text reaching the vault and says
  // nothing about older text nobody has answered for — dropping it here would
  // leave the panel offering a record that is already gone.
  if (!held) {
    store?.drop();
  }
  // Only the keeper's own sentence is taken back. A browser that will not keep a
  // copy is still not keeping one after a successful save, and clearing the line
  // here would replace a standing warning with silence — for a store that is
  // null, permanently, since nothing writes to bring it back.
  if (store?.available()) {
    setDraftStatus(elements, "");
  }
};

// writeDraft is one debounce tick. It returns whether "kept in this browser" is
// on screen afterwards, which is the only state the write leaves behind.
//
// Two of its three branches write nothing at all, and both of those are about
// not throwing text away on a page that is on its way out: this also runs from
// `beforeunload` and `pagehide`.
const writeDraft = (
  options: DraftKeeperOptions,
  announced: boolean,
  held: boolean
): boolean => {
  const { store, elements, config, host } = options;
  const status = host.status();
  if (!store || status === "unknown") {
    return announced;
  }
  // The slot belongs to the offer until somebody answers it. Straight after a
  // reload the editor shows exactly what the vault holds while the record in
  // storage holds the text this page exists to hand back; dropping it because
  // the two agree would answer for the person, and writing over it with what
  // they type next would destroy it while the panel went on offering it.
  if (held) {
    if (status === "unsaved") {
      // Said only once there is genuinely new text going unkept — before that
      // there is nothing to warn about.
      if (!announced) {
        setDraftStatus(elements, draftStatusMessages.deferred, "is-warning");
      }
      return true;
    }
    // Back to what the vault holds, so there is no new text going unkept and
    // the sentence saying there is has to go with it.
    if (announced) {
      setDraftStatus(elements, "");
    }
    return false;
  }
  // Text that matches what the vault holds is not a draft. Dropping rather than
  // skipping matters: undoing an edit back to the saved text is a person saying
  // they no longer want it, and a stored copy would offer it back to them
  // tomorrow.
  if (status === "held") {
    store.drop();
    if (announced) {
      setDraftStatus(elements, "");
    }
    return false;
  }
  const outcome = store.keep({
    relPath: config.relPath,
    baseRevision: host.baseRevision(),
    content: host.currentContent(),
  });
  return reportWriteOutcome(elements, outcome, announced);
};

// The hold on this tab's own slot, and the three things that end it.
//
// It is its own object because "is the writer allowed this slot" is a different
// question from "which draft is on screen", and the presenter below asks it from
// four places — the load, a superseded record, a discard, and an answer.
type SlotHold = {
  unanswered(): boolean;
  // claim reads the load's drafts and holds the slot when one of them is this
  // tab's own. The hold covers the slot, not the panel: a load that offers
  // another tab's newer draft first still has this tab's own unanswered record
  // underneath it, and the writer must leave that alone until the person has
  // worked their way down to it.
  claim(kept: SourceDraft[]): void;
  // release is "this one" — a restore or a recreate — which ends the recovery
  // entirely. From that moment the editor holds text a person deliberately put
  // there, and that is what an interruption would take, so it gets the slot.
  // There is only one, and going on protecting an older record in it would leave
  // the text on screen kept nowhere at all, which is the failure this whole
  // feature exists to prevent.
  release(): void;
  // declined is "not this one" — a discard — which frees the slot only when the
  // record it threw away was the one in it. A discard of another tab's draft
  // says nothing about this tab's own, which is still unanswered and still the
  // only copy of its text; the next question is about that record and the writer
  // must keep off it until then.
  declined(decided: DraftOffer): void;
  // drop takes superseded records out of storage — ones the vault already holds
  // the text of, so nothing is thrown away — and ends the hold with this tab's
  // own. That half has to happen everywhere and not just at the load: a hold
  // kept for a record that is no longer there protects nothing and stops the
  // live text from being kept.
  drop(ids: string[]): void;
};

export const createSlotHold = (store: SourceDraftStore | null, writer: DraftWriter): SlotHold => {
  const ownID = store?.draftID ?? "";

  // The hold itself lives in the writer and is not remembered a second time
  // here. It can end without anybody answering — another tab, out of room,
  // evicting the record it was protecting — and a presenter holding its own copy
  // of the answer would go on excluding a slot that by then holds the live text,
  // and offer somebody back the words they are looking at, with a Discard button
  // pointed at their only copy of them.
  const release = (): void => {
    writer.hold(false);
  };

  return {
    unanswered: writer.holding,

    claim(kept: SourceDraft[]): void {
      writer.hold(kept.some((draft) => draft.draftID === ownID));
    },

    release,

    declined(decided: DraftOffer): void {
      if (decided.source === "storage" && decided.draft.draftID === ownID) {
        release();
      }
    },

    drop(ids: string[]): void {
      for (const id of ids) {
        store?.drop(id);
        if (id === ownID) {
          release();
        }
      }
    },
  };
};

// reviseStanding re-decides the offer on screen once this editor has moved the
// note on — a save of its own, or taking over the other writer's version from
// the conflict panel.
//
// The offer was decided against the reading the load made, and that reading is
// now this tab's own history. Left alone, a panel would go on saying "Alpha.md
// has not changed since, so putting it back leaves you where you were" over a
// note this tab changed a minute ago — and its Restore, still a "resume", would
// arm an ordinary save of pre-save text against the post-save revision and
// overwrite the save with no conflict anywhere. Re-deciding turns it into the
// "stale" it now is: both versions side by side, and a revision the restore
// re-pins.
//
// An offer that does not survive is taken down by *re-asking* rather than by
// hiding the panel, for the same reason a discard re-asks: this editor's save
// answers nothing about a record somebody else's tab left in storage, and a
// panel that simply goes dark over a still-unanswered own record would leave the
// writer holding that slot for a question with no buttons on screen — every
// keystroke from then on kept nowhere, under a line telling the person to
// restore or discard something they can no longer see.
export const reviseStanding = (
  standing: DraftOffer | null,
  ownID: string,
  reading: VaultReading,
  panel: { show(next: DraftOffer | null): void; drop(ids: string[]): void; reoffer(): void }
): void => {
  if (!standing) {
    return;
  }
  // An offer raised from the editor is about text that had nowhere to go, and a
  // successful write is the answer to it: the note exists and holds this
  // editor's bytes. It comes down rather than being re-decided, which would
  // describe text that has never been in storage as a record kept there.
  if (standing.source === "editor") {
    panel.reoffer();
    return;
  }
  // Only the draft already on screen is re-decided, never re-read from storage:
  // the question is about one specific text, and swapping which text it is about
  // under somebody's cursor would be a different question wearing the same
  // buttons.
  const decision = decideDraftRecovery({
    drafts: [standing.draft],
    ownDraftID: ownID,
    vault: reading,
  });
  panel.drop(decision.superseded);
  if (!decision.offer) {
    panel.reoffer();
    return;
  }
  panel.show(decision.offer);
};

// reportWriteOutcome says something only when the answer changed, and the first
// successful write says it once. A line that re-announced "kept in this browser"
// on every keystroke would be noise a person stops reading, which is exactly the
// line that must still be believed when it says storage is full.
//
// It returns whether "kept" is currently on screen, so a failure un-announces
// it: a write refused for want of room and then accepted again has to say so,
// or the line stays stuck on the worse of the two answers.
const reportWriteOutcome = (
  elements: SourceEditorElements,
  outcome: DraftWriteOutcome,
  announced: boolean
): boolean => {
  if (outcome === "refused") {
    setDraftStatus(elements, draftStatusMessages.refused, "is-error");
    return false;
  }
  if (outcome === "unavailable") {
    setDraftStatus(elements, draftStatusMessages.unavailable, "is-warning");
    return false;
  }
  if (!announced) {
    setDraftStatus(elements, draftStatusMessages.kept);
  }
  return true;
};
