import { draftStatusMessages, setDraftStatus } from "./source-draft-panel";
import type { SourceDraft, SourceDraftStore } from "./source-draft";
import type { SourceEditorElements } from "./source-editor-elements";
import type { DraftKeeper, DraftKeeperOptions } from "./source-editor-draft-types";
import { createOfferPresenter } from "./source-editor-draft-offer";
import { createDraftWriter } from "./source-editor-draft-writer";

export type {
  DraftHost,
  DraftKeeper,
  DraftKeeperOptions,
  DraftTextStatus,
} from "./source-editor-draft-types";

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
      // The vault holds `content`, but the editor may already hold more: text
      // typed while the save was in flight. That text exists nowhere else, so
      // the record is rewritten against the new revision rather than dropped.
      if (host.currentContent() !== content) {
        writer.flush();
      } else {
        writer.adopted();
      }
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
