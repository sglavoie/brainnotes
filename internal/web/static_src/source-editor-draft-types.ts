import type { StatusKind } from "./status";
import type { DraftOffer, VaultReading } from "./source-draft-recovery";
import type { SourceDraft, SourceDraftStore } from "./source-draft";
import type { AttachmentClaims, SourceClient } from "./source-save";
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
export const defaultDelayMs = 300;
