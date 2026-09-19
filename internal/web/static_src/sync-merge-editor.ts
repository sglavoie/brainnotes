import {
  createSourceDraftStore,
  mergeNoteKeyPrefix,
  type DraftStorage,
  type DraftWriteOutcome,
  type SourceDraft,
  type SourceDraftStore,
} from "./source-draft";
import type { SourceSurface, SourceSurfaceFactory } from "./source-surface";

// The merge editor: the same text surface the /edit page mounts, over one
// contested path.
//
// It is the editor rather than a textarea because a manual merge is Markdown
// being written — wrapped prose, bracket matching, an undo history — and because
// a person composing one is doing the same work `/edit` exists for. The surface
// seam is what makes that reuse free: this module takes a `SourceSurfaceFactory`
// and never imports CodeMirror, so everything it decides is testable under jsdom.
//
// It keeps the composed text in the browser for the same reason the editor does,
// and here the reason is stronger. A merge is the one text on the review page that
// exists nowhere else: the vault holds this version and the peer holds that one,
// and the third one — the one being typed — has no copy anywhere until it is
// applied. That is why a stale preview never clears it (see sync-conflicts-panel)
// and why a reload seeds from the kept copy rather than from the vault's version.
//
// The draft key is `sync-merge|<package>|<path>`, under the same vault identity
// and the same per-tab identity the editor's drafts use, so a merge for one vault
// is never offered over another vault's conflict on the same origin, and two tabs
// resolving the same conflict are two merges rather than one overwriting the other.

export type SyncMergeEditorDeps = {
  host: HTMLElement;
  notice: HTMLElement;
  reset: HTMLButtonElement | null;
  factory: SourceSurfaceFactory;
  vaultID: string;
  packageID: string;
  // path is the contested vault path, which is part of the draft key rather than
  // of the text: one package can contest several paths and each is its own merge.
  path: string;
  // seed is the text a fresh merge opens with — this vault's version, or the other
  // replica's where this vault holds nothing at the path. See mergeSeed.
  seed: string;
  // baseRevision is the digest of *this vault's* version of the path, which is the
  // one that moves while a merge is being composed: the other replica's travels
  // inside the package and cannot change under it. It is empty when this vault holds
  // nothing there, and that is a base like any other — a note re-created locally
  // since the merge was started no longer matches it.
  //
  // A kept merge whose base no longer matches is still offered, with a sentence
  // saying so: it is somebody's unfinished work and this layer will not throw it
  // away over a digest.
  baseRevision: string;
  storage: DraftStorage | null;
  draftID: string;
  onText(text: string): void;
  now?: () => number;
  delayMs?: number;
  view?: Window | null;
};

export type SyncMergeEditor = {
  text(): string;
  /** restart puts the seed back and drops the kept copy. */
  restart(): void;
  /** applied says the merge is in the vault, so the kept copy is superseded. */
  applied(): void;
  /** flush writes the pending keep now. */
  flush(): void;
  /**
   * kept answers whether the text on screen would survive this editor being
   * replaced, which is the question the page has to ask before it re-renders and
   * tells somebody their merge is still there. It is the *outcome* of the last
   * keep rather than the presence of storage: a browser with no room left has
   * storage and refuses the write anyway. Flush first, or it answers about a
   * keystroke whose debounce has not fired.
   */
  kept(): boolean;
  destroy(): void;
};

// defaultDelayMs matches the editor's debounce for the same reason: local storage
// is synchronous on the main thread, and a third of a second writes about once a
// word while losing only a few characters to a crash.
const defaultDelayMs = 300;

// The prefix comes from source-draft.ts because that module's eviction reads it: a
// key built here by hand would be evicted as an ordinary draft the day somebody
// retyped it.
export const mergeDraftKey = (packageID: string, path: string): string =>
  `${mergeNoteKeyPrefix}${packageID}|${path}`;

// keptMerge is the newest kept merge for this conflict, this tab's own preferred.
// A merge left by an earlier load of this tab is the same person's work and is
// offered back; another tab's is too, because there is nothing else to offer and
// a page that ignored it would be quietly dropping text.
const keptMerge = (store: SourceDraftStore, ownID: string): SourceDraft | null => {
  const kept = store.list();
  return kept.find((draft) => draft.draftID === ownID) ?? kept[0] ?? null;
};

const noticeFor = (kept: SourceDraft | null, baseRevision: string, available: boolean): string => {
  if (!kept) {
    return available
      ? ""
      : "This browser will not keep a copy of the merge, so it exists only on this page.";
  }
  if (kept.baseRevision !== baseRevision) {
    return (
      "A merge kept in this browser was loaded. It was started from a different version " +
      "of this vault's copy, so read it against the versions above before applying it."
    );
  }
  return "A merge kept in this browser was loaded.";
};

// MergeKeeper is the debounce and the slot, and nothing about the surface or the
// page. It is separate because "when is the copy written" is the one thing here a
// test drives with a fake clock, and because everything else in this module is a
// single statement once it exists.
type MergeKeeper = {
  keep(): void;
  flush(): void;
  cancel(): void;
  drop(): void;
  /** stored reports the last write's answer: see SyncMergeEditor.kept. */
  stored(): boolean;
};

const createMergeKeeper = (
  deps: SyncMergeEditorDeps,
  store: SourceDraftStore,
  read: () => string,
  report: (outcome: DraftWriteOutcome) => void
): MergeKeeper => {
  const delayMs = deps.delayMs ?? defaultDelayMs;
  let timer: ReturnType<typeof setTimeout> | null = null;
  // dropped latches a deliberate discard. Both `applied` and `restart` say the kept
  // copy must go, and a re-render tears the editor down with a *flush* — so without
  // the latch a resolution that succeeded would put the merge straight back in this
  // browser, and "start again" would keep the text it just abandoned. A keystroke
  // clears it, because that is text nobody asked to lose.
  let dropped = false;
  // outcome is the last write's answer, and null means there is nothing outstanding
  // for one to be about: no keystroke has been kept yet, or the copy was dropped
  // deliberately. Both of those are safe — the text on screen is the seed, which the
  // page can put back — so they read as kept.
  let outcome: DraftWriteOutcome | null = null;

  const cancel = (): void => {
    if (timer !== null) {
      clearTimeout(timer);
      timer = null;
    }
  };

  const write = (): void => {
    outcome = store.keep({ relPath: deps.path, baseRevision: deps.baseRevision, content: read() });
    report(outcome);
  };

  return {
    keep(): void {
      dropped = false;
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
      if (dropped) {
        return;
      }
      write();
    },

    cancel,

    drop(): void {
      cancel();
      dropped = true;
      outcome = null;
      store.drop();
    },

    stored: (): boolean => outcome === null || outcome === "stored",
  };
};

// writeNotice is what a keep that did not happen says. Both answers are the same
// fact from a person's point of view — the merge exists on this page and nowhere
// else — and it is worth saying while the text is still on screen, because the
// alternative is finding out by losing it.
//
// A browser that never offered storage has already said so at load, and says it
// again here rather than being treated as a change of answer: the load's sentence
// is about an empty editor and this one is about text that has been typed.
const writeNotice = (outcome: DraftWriteOutcome): string => {
  if (outcome === "refused") {
    return (
      "This browser has no room left to keep a copy of the merge, so it exists only " +
      "on this page. Copy it somewhere else before leaving."
    );
  }
  return "This browser will not keep a copy of the merge, so it exists only on this page.";
};

// MergeNotice owns the one line of prose under the editor. It is its own thing
// because that line answers two independent questions — where the text on screen
// came from, and whether this browser is keeping a copy of it — and the rule
// between them is the whole of what there is to get right here.
type MergeNotice = {
  /** report says what the last keep did, and takes its own sentence back down. */
  report(outcome: DraftWriteOutcome): void;
  /** dismiss is the deliberate discard: nothing on the line is about anything now. */
  dismiss(): void;
};

// standing is the load's own sentence, and a keep that failed and then worked puts
// it back rather than clearing the line. The one that matters is "started from a
// different version of this vault's copy": that is a warning about the text on
// screen and it stays true however the keep went, so clearing the line outright
// would answer a storage problem by silently dropping advice about the merge.
//
// A failure's own sentence is taken back down when a later keep works, because a
// line still saying the text is unkept once it is kept is the same wrong answer in
// the other direction. Only its own — the load's sentence is not a failure, and a
// recovered merge is written straight back at mount, which would otherwise wipe it
// at once.
const createMergeNotice = (host: HTMLElement, standing: string): MergeNotice => {
  const show = (sentence: string): void => {
    host.textContent = sentence;
    host.hidden = sentence === "";
  };
  let line = standing;
  let warned = false;
  show(line);
  return {
    report: (outcome: DraftWriteOutcome): void => {
      if (outcome !== "stored") {
        warned = true;
        show(writeNotice(outcome));
        return;
      }
      if (warned) {
        warned = false;
        show(line);
      }
    },
    // After a restart or an application there is no recovered merge for the load's
    // sentence to be about either, so it goes with everything else.
    dismiss: (): void => {
      warned = false;
      line = "";
      show("");
    },
  };
};

export const createSyncMergeEditor = (deps: SyncMergeEditorDeps): SyncMergeEditor => {
  const store = createSourceDraftStore({
    vaultID: deps.vaultID,
    noteKey: mergeDraftKey(deps.packageID, deps.path),
    draftID: deps.draftID,
    storage: deps.storage,
    now: deps.now,
  });
  const kept = keptMerge(store, store.draftID);
  const initial = kept ? kept.content : deps.seed;

  const notice = createMergeNotice(
    deps.notice,
    noticeFor(kept, deps.baseRevision, store.available())
  );

  const surface: SourceSurface = deps.factory(deps.host, initial);
  const keeper = createMergeKeeper(deps, store, () => surface.getValue(), notice.report);

  // The decision model is told on every change and storage is told on a debounce.
  // Those are different deadlines on purpose: what is being applied has to be the
  // text on screen at the moment Apply is pressed, while what is kept only has to
  // survive the tab going away.
  surface.onChange(() => {
    deps.onText(surface.getValue());
    keeper.keep();
  });

  const onHide = (): void => keeper.flush();
  deps.view?.addEventListener("beforeunload", onHide);
  deps.view?.addEventListener("pagehide", onHide);

  const restart = (): void => {
    surface.setValue(deps.seed);
    // setValue is not a user edit, so nothing above hears it. Both listeners are
    // told by hand, in this order: the decision model must not go on holding the
    // abandoned text, and storage must not go on holding it either.
    deps.onText(deps.seed);
    keeper.drop();
    notice.dismiss();
  };

  deps.reset?.addEventListener("click", restart);

  // A recovered merge is written back now rather than at the first keystroke: one
  // recovered from *another* tab's slot has never been in this tab's, and a person
  // who reloads without touching it would find it gone.
  if (kept) {
    deps.onText(initial);
    keeper.flush();
  }

  return {
    text: (): string => surface.getValue(),
    restart,

    applied(): void {
      keeper.drop();
      notice.dismiss();
    },

    flush: keeper.flush,
    kept: keeper.stored,

    destroy(): void {
      // Deliberately a flush and not a cancel: a re-render of the page tears the
      // editor down, and the text in it is the only copy there is.
      keeper.flush();
      deps.view?.removeEventListener("beforeunload", onHide);
      deps.view?.removeEventListener("pagehide", onHide);
      deps.reset?.removeEventListener("click", restart);
      surface.destroy();
    },
  };
};
