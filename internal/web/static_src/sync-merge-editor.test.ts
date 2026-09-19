// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { createSyncMergeEditor, mergeDraftKey, type SyncMergeEditor } from "./sync-merge-editor";
import { createFakeSurfaceFactory, type FakeSurface } from "./source-editor.test-helpers";
import {
  draftKey,
  memoryDraftStorage,
  readDrafts,
  seedDraft,
  type FakeDraftStorage,
} from "./source-draft.test-helpers";
import type { SourceDraft } from "./source-draft";

// The merge editor, which is the only text on the review page that exists nowhere
// else: the vault holds one version, the peer holds the other, and the merge being
// typed has no copy anywhere until it is applied. Everything below is about not
// losing it — to a reload, to a tab closing, or to a re-render.

const vaultID = "sync:vault-under-test";
const noteKey = mergeDraftKey("pkg-1", "Alpha.md");
const ownSlot = draftKey(vaultID, noteKey, "tab-1");
const seed = "mine\n";

let storage: FakeDraftStorage;
let texts: string[];
let surface: () => FakeSurface | null;

type MergeOptions = {
  storage?: FakeDraftStorage | null;
  baseRevision?: string;
};

const mount = (options: MergeOptions = {}): SyncMergeEditor => {
  document.body.innerHTML = `
    <div data-host></div>
    <p data-notice hidden></p>
    <button type="button" data-reset>Start again</button>
  `;
  const factory = createFakeSurfaceFactory();
  surface = factory.surface;
  return createSyncMergeEditor({
    host: document.querySelector("[data-host]") as HTMLElement,
    notice: document.querySelector("[data-notice]") as HTMLElement,
    reset: document.querySelector("[data-reset]") as HTMLButtonElement,
    factory: factory.factory,
    vaultID,
    packageID: "pkg-1",
    path: "Alpha.md",
    seed,
    baseRevision: options.baseRevision ?? "sha-mine",
    storage: options.storage === undefined ? storage : options.storage,
    draftID: "tab-1",
    onText: (text: string): void => void texts.push(text),
    // A fixed clock, because the store prunes against one: a seeded record with a
    // plausible timestamp would otherwise be collected as a fortnight old.
    now: () => 10_000,
    view: window,
  });
};

const seedMerge = (content: string, overrides: Partial<SourceDraft> = {}): SourceDraft =>
  seedDraft(storage, {
    vaultID,
    noteKey,
    relPath: "Alpha.md",
    baseRevision: "sha-mine",
    draftID: "tab-1",
    content,
    updatedAt: 9_000,
    ...overrides,
  });

const kept = (): SourceDraft[] => readDrafts(storage);
const notice = (): HTMLElement => document.querySelector("[data-notice]") as HTMLElement;
const type = (text: string): void => (surface() as FakeSurface).type(text);

beforeEach(() => {
  storage = memoryDraftStorage();
  texts = [];
});

afterEach(() => {
  vi.useRealTimers();
});

it("opens on this vault's version and keeps a copy once the typing settles", () => {
  vi.useFakeTimers();
  mount();

  expect(surface()?.getValue()).toBe(seed);
  expect(notice().hidden).toBe(true);
  expect(kept()).toEqual([]);

  type("merged\n");

  // The decision model hears every keystroke; storage hears them on a deadline.
  expect(texts).toEqual(["merged\n"]);
  expect(kept()).toEqual([]);

  vi.advanceTimersByTime(300);

  expect(storage.keys()).toContain(ownSlot);
  expect(kept()).toHaveLength(1);
  expect(kept()[0]).toMatchObject({
    vaultID,
    noteKey: "sync-merge|pkg-1|Alpha.md",
    relPath: "Alpha.md",
    baseRevision: "sha-mine",
    draftID: "tab-1",
    content: "merged\n",
  });
});

it("coalesces a burst of keystrokes into the text that was on screen last", () => {
  vi.useFakeTimers();
  mount();

  type("m");
  type("me");
  type("mer");
  vi.advanceTimersByTime(300);

  expect(kept()).toHaveLength(1);
  expect(kept()[0].content).toBe("mer");
});

it("offers a kept merge back, says so, and seeds from it rather than from the vault", () => {
  seedMerge("half composed\n");
  mount();

  expect(surface()?.getValue()).toBe("half composed\n");
  expect(notice().textContent).toBe("A merge kept in this browser was loaded.");
  expect(notice().hidden).toBe(false);
  // The decision model is told without a keystroke: an Apply pressed immediately
  // must submit the recovered merge, not the vault's version.
  expect(texts).toEqual(["half composed\n"]);
});

it("keeps a merge started from another version rather than dropping it over a digest", () => {
  seedMerge("older merge\n", { baseRevision: "sha-before" });
  mount();

  expect(surface()?.getValue()).toBe("older merge\n");
  expect(notice().textContent).toContain("started from a different version");
  expect(notice().textContent).toContain("read it against the versions above before applying it");
});

it("prefers this tab's own merge over another tab's newer one", () => {
  seedMerge("this tab\n", { draftID: "tab-1", updatedAt: 8_000 });
  seedMerge("another tab\n", { draftID: "tab-2", updatedAt: 9_500 });

  mount();

  expect(surface()?.getValue()).toBe("this tab\n");
});

it("writes another tab's merge into this tab's slot straight away", () => {
  seedMerge("from the other tab\n", { draftID: "tab-2" });
  mount();

  // Not at the first keystroke: a person who reloads without touching the
  // recovered text would otherwise find it gone.
  const mine = kept().find((draft) => draft.draftID === "tab-1");
  expect(mine?.content).toBe("from the other tab\n");
  expect(surface()?.getValue()).toBe("from the other tab\n");
});

it("says the merge exists only on this page when the browser keeps nothing", () => {
  const editor = mount({ storage: null });

  expect(notice().textContent).toBe(
    "This browser will not keep a copy of the merge, so it exists only on this page."
  );
  // And nothing here throws over it: a missing safety net must not become the
  // accident.
  type("merged\n");
  editor.flush();
  expect(texts).toEqual(["merged\n"]);
});

it("says so when this browser had no room for the merge, and stops claiming it is kept", () => {
  vi.useFakeTimers();
  // A quota measured in characters, which is how a browser measures one: the record
  // is refused for want of room rather than for want of storage, and there is no
  // foreign draft to evict, so the store's one retry has nothing to try.
  storage.maxBytes = 20;
  const editor = mount();
  expect(editor.kept()).toBe(true);

  type("a merge that will not fit\n");
  vi.advanceTimersByTime(300);

  expect(kept()).toEqual([]);
  expect(notice().textContent).toContain("no room left to keep a copy of the merge");
  expect(notice().textContent).toContain("Copy it somewhere else before leaving");
  expect(notice().hidden).toBe(false);
  // The page asks this before it re-renders and tells somebody their merge survived
  // the reload. Storage is present and answered no, so the promise is not available.
  expect(editor.kept()).toBe(false);
});

it("takes the no-room sentence back down once a copy is kept", () => {
  vi.useFakeTimers();
  storage.maxBytes = 20;
  const editor = mount();
  type("a merge that will not fit\n");
  vi.advanceTimersByTime(300);
  expect(editor.kept()).toBe(false);

  storage.maxBytes = Number.POSITIVE_INFINITY;
  type("short\n");
  vi.advanceTimersByTime(300);

  expect(kept()[0].content).toBe("short\n");
  expect(editor.kept()).toBe(true);
  // A line still saying the text is unkept once it is kept is the same wrong answer
  // in the other direction.
  expect(notice().textContent).toBe("");
  expect(notice().hidden).toBe(true);
});

it("leaves the load's own sentence alone when the recovered merge is written back", () => {
  seedMerge("half composed\n");
  const editor = mount();

  // Mounting writes the recovered merge back immediately, which is a successful
  // keep — and must not be read as "a failure cleared" and wipe the sentence
  // explaining where the text on screen came from.
  expect(notice().textContent).toBe("A merge kept in this browser was loaded.");
  expect(editor.kept()).toBe(true);
});

it("puts the seed back on start again, and stops holding the abandoned text", () => {
  vi.useFakeTimers();
  mount();
  type("abandoned\n");
  vi.advanceTimersByTime(300);
  expect(kept()).toHaveLength(1);

  (document.querySelector("[data-reset]") as HTMLButtonElement).click();

  expect(surface()?.getValue()).toBe(seed);
  // setValue is not a user edit, so both the decision model and storage are told
  // by hand — a model still holding "abandoned\n" would submit it.
  expect(texts.at(-1)).toBe(seed);
  expect(kept()).toEqual([]);
  expect(notice().textContent).toBe("");
  expect(notice().hidden).toBe(true);
});

it("drops the kept copy once the merge is in the vault, and does not put it back", () => {
  seedMerge("composed\n");
  const editor = mount();
  expect(kept()).toHaveLength(1);

  editor.applied();

  expect(kept()).toEqual([]);
  expect(notice().hidden).toBe(true);

  // A successful resolution re-renders the page, and a re-render tears this editor
  // down with a flush. The text is in the vault now, so that flush must write
  // nothing — or the merge would be offered back over a conflict that is settled.
  editor.destroy();
  expect(kept()).toEqual([]);
});

it("does not keep the text that start again abandoned", () => {
  vi.useFakeTimers();
  const editor = mount();
  type("abandoned\n");
  vi.advanceTimersByTime(300);

  (document.querySelector("[data-reset]") as HTMLButtonElement).click();
  editor.destroy();

  expect(kept()).toEqual([]);
});

it("flushes the pending copy when the tab goes away", () => {
  vi.useFakeTimers();
  mount();
  type("unfinished\n");
  expect(kept()).toEqual([]);

  window.dispatchEvent(new Event("pagehide"));

  expect(kept()[0].content).toBe("unfinished\n");
});

it("flushes rather than cancels when the page tears the editor down", () => {
  vi.useFakeTimers();
  const editor = mount();
  type("mid sentence\n");

  editor.destroy();

  // A re-render destroys this editor, and the text in it is the only copy there
  // is — so teardown writes it instead of discarding the pending write.
  expect(kept()[0].content).toBe("mid sentence\n");
  expect(surface()?.destroyed()).toBe(true);
  // The window listeners went with it: a later pagehide must not write through a
  // destroyed surface.
  window.dispatchEvent(new Event("pagehide"));
  expect(kept()).toHaveLength(1);
});

it("keys the merge by vault, package and path, so no two conflicts share a slot", () => {
  vi.useFakeTimers();
  mount();
  type("alpha merge\n");
  vi.advanceTimersByTime(300);

  expect(mergeDraftKey("pkg-2", "Alpha.md")).not.toBe(noteKey);
  expect(mergeDraftKey("pkg-1", "Beta.md")).not.toBe(noteKey);
  expect(storage.keys()).toEqual([ownSlot]);
  expect(storage.keys()[0]).toContain(encodeURIComponent(vaultID));
  expect(storage.keys()[0]).toContain(encodeURIComponent("sync-merge|pkg-1|Alpha.md"));
});
