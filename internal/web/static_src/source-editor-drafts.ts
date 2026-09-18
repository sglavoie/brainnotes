import type { StatusKind } from "./status";
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
  type DraftWriteOutcome,
  type SourceDraft,
  type SourceDraftStore,
} from "./source-draft";
import { newSaveID, type AttachmentClaims, type SourceClient } from "./source-save";
import type { SourceEditorConfig, SourceEditorElements } from "./source-editor-elements";

// The draft machinery as the editor uses it: keep the text, offer it back, and
// do neither of those things behind anybody's back.
//
// It sits between the controller and the store so that the controller keeps
// owning the editor's state — the surface, the pinned revision, the conflict
// panel — and this file owns only the question of what is kept and what is
// asked. The seam is `DraftHost`: five things the keeper may ask of the editor,
// none of which is "save".
//
// There is no path through this module that writes to the vault without a click
// first. `restore` fills the editor and stops. `recreate` is the one request it
// makes, and it is a button.

// DraftTextStatus is what the editor can honestly say about the text on screen,
// and it is three answers rather than two on purpose.
//
// "held" is what makes a draft worth keeping at all: without it every note
// somebody merely opened would leave a full copy of itself in local storage on
// the way out — a few hundred notes into a browsing session that is the quota
// gone, spent on text the vault already holds, and the draft that actually
// matters is the one that gets refused for want of room.
//
// "unknown" is the load that never produced an editor — a restarted Pi, a
// dropped connection, an Access session that lapsed. It is *not* "nothing
// unsaved": nothing was compared, so nothing may be concluded, and a boolean
// would have to report it as one of the other two. Reporting it as "held" is
// the worse of the two, because that is the answer that drops the copy.
export type DraftTextStatus = "unsaved" | "held" | "unknown";

export type DraftHost = {
  currentContent(): string;
  baseRevision(): string;
  status(): DraftTextStatus;
  // saving says a save is in flight. The recovery panel stays up for the
  // duration of a request — the two states genuinely overlap — so every button
  // on it is declined while one is, exactly as the conflict panel's are: a
  // restore that lands mid-request re-pins the revision and fills the editor,
  // and the answer still to come adopts the submitted text over the top of it,
  // leaving pre-save text on screen pinned to the post-save revision with
  // nothing to stop the next save overwriting what this one just wrote.
  saving(): boolean;
  // restore puts a recovered draft back in the editor. What that means differs
  // by offer kind — a stale one also has to put the vault's version on screen —
  // so the whole offer is handed over rather than just its text.
  restore(offer: DraftOffer): void;
  setStatus(message: string, kind: StatusKind): void;
};

export type DraftKeeper = {
  // kept is what storage held when the page loaded, read before anything
  // adopted or dropped a thing.
  kept(): SourceDraft[];
  keep(): void;
  flush(): void;
  // adopted carries what the vault now holds, because it changes two things at
  // once: the copy in storage is superseded, and any offer still on screen was
  // decided against a reading this editor has just replaced.
  adopted(content: string, revision: string): void;
  announce(revision: string): void;
  present(kept: SourceDraft[], vault: VaultReading): void;
  offerRecreate(content: string): void;
  dispose(): void;
};

export type DraftKeeperOptions = {
  store: SourceDraftStore | null;
  elements: SourceEditorElements;
  config: SourceEditorConfig;
  client: SourceClient;
  host: DraftHost;
  view: Window | null;
  now: () => number;
  delayMs?: number;
  navigate: (url: string) => void;
  // claims is this page's staged uploads. Recreating a recovered draft is a
  // write like any other and owes them exactly as a save does: the text being
  // written back is the text the links are in, so a recreate that carried no
  // ids would land a note referencing files that never left staging.
  claims?: AttachmentClaims | null;
};

// defaultDelayMs debounces the write. Local storage is synchronous and on the
// main thread, so writing a 100 kB note on every keystroke would be felt; a
// third of a second is short enough that the text lost to a crash is a few
// characters and long enough that ordinary typing writes once a word or so.
const defaultDelayMs = 300;

// The keeper is two halves that share nothing but the options they were built
// from: one owns the debounce and what is in storage, the other owns what is on
// screen and what the buttons do. They are wired together here because the wiring
// is the only place that knows about both — a discard re-asks, a restore writes.
export const createDraftKeeper = (options: DraftKeeperOptions): DraftKeeper => {
  const { store, elements, host } = options;
  const writer = createDraftWriter(options);
  const presenter = createOfferPresenter(options, writer);

  const unbind = bindDraftEvents({
    elements,
    view: options.view,
    store,
    baseRevision: () => host.baseRevision(),
    onRestore: presenter.onRestore,
    onRecreate: presenter.onRecreate,
    onDiscard: presenter.onDiscard,
    onFlush: writer.flush,
    onPeerSave: () => {
      // The draft line is about to carry somebody else's news, so the writer is
      // told that its own sentence is no longer on screen. Without that, the
      // next keep sees "already announced" and stays quiet, and the one line
      // that says this text has never left the browser is replaced for the rest
      // of the session by one containing the word "saved".
      writer.unannounce();
      setDraftStatus(elements, draftStatusMessages.peerSaved, "is-warning");
    },
  });

  // Said before a single keystroke, not after the first failed write. A person
  // typing into a browser that will not keep a copy should learn that while the
  // note is still short enough to retype.
  if (!store?.available()) {
    setDraftStatus(elements, draftStatusMessages.unavailable, "is-warning");
  }

  return {
    kept: (): SourceDraft[] => store?.list() ?? [],
    keep: writer.keep,
    flush: writer.flush,

    adopted(content: string, revision: string): void {
      writer.adopted();
      presenter.revised(content, revision);
    },

    announce(revision: string): void {
      store?.announceSaved(revision);
    },

    present: presenter.present,
    offerRecreate: presenter.offerRecreate,

    dispose(): void {
      writer.cancel();
      unbind();
    },
  };
};

// The writing half: the debounce, whether "kept" is currently on screen, and
// nothing about the panel.
type DraftWriter = {
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

const createDraftWriter = (options: DraftKeeperOptions): DraftWriter => {
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
const ownSlotKeeps = (store: SourceDraftStore | null, content: string): boolean =>
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

const createSlotHold = (store: SourceDraftStore | null, writer: DraftWriter): SlotHold => {
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
const reviseStanding = (
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

const createOfferPresenter = (options: DraftKeeperOptions, writer: DraftWriter): OfferPresenter => {
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

type DraftEventOptions = {
  elements: SourceEditorElements;
  view: Window | null;
  store: SourceDraftStore | null;
  baseRevision: () => string;
  onRestore: () => void;
  onRecreate: () => void;
  onDiscard: () => void;
  onFlush: () => void;
  onPeerSave: () => void;
};

const bindDraftEvents = (options: DraftEventOptions): (() => void) => {
  const { elements, view } = options;
  elements.recoveryRestore?.addEventListener("click", options.onRestore);
  elements.recoveryRecreate?.addEventListener("click", options.onRecreate);
  elements.recoveryDiscard?.addEventListener("click", options.onDiscard);

  // Both unload events, because neither is reliable alone: `beforeunload` does
  // not fire when a mobile browser discards a backgrounded tab, and `pagehide`
  // is the one that does. Writing twice is free — the record is idempotent —
  // and writing never is the whole failure this feature exists to prevent.
  const onHide = (): void => options.onFlush();
  view?.addEventListener("beforeunload", onHide);
  view?.addEventListener("pagehide", onHide);

  // A `storage` event fires in every *other* tab on this origin, which is what
  // makes it a usable channel and also what makes it only ever a notice: this
  // tab has its own unsaved text and its own pinned revision, and adopting
  // somebody else's save here would overwrite the first with a version of the
  // second nobody asked for.
  const onStorage = (event: StorageEvent): void => {
    const revision = options.store?.peerSavedRevision(event.key, event.newValue);
    if (revision && revision !== options.baseRevision()) {
      options.onPeerSave();
    }
  };
  view?.addEventListener("storage", onStorage);

  return () => {
    elements.recoveryRestore?.removeEventListener("click", options.onRestore);
    elements.recoveryRecreate?.removeEventListener("click", options.onRecreate);
    elements.recoveryDiscard?.removeEventListener("click", options.onDiscard);
    view?.removeEventListener("beforeunload", onHide);
    view?.removeEventListener("pagehide", onHide);
    view?.removeEventListener("storage", onStorage);
  };
};
