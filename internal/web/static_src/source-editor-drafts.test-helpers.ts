import { expect, vi } from "vitest";
import type { SourceSaveResponse } from "./api-types-source";
import { createSourceDraftStore } from "./source-draft";
import {
  memoryDraftStorage,
  readDrafts,
  seedDraft,
  type FakeDraftStorage,
} from "./source-draft.test-helpers";
import { createSourceEditorController } from "./source-editor-controller";
import {
  readSourceEditorConfig,
  readSourceEditorElements,
  type SourceEditorElements,
} from "./source-editor-elements";
import {
  createFakeSurfaceFactory,
  editorMarkup,
  type FakeSurface,
} from "./source-editor.test-helpers";
import { notFoundCode, type AttachmentClaims, type SourceClient } from "./source-save";

// The draft machinery through the editor that owns it.
//
// These are deliberately controller-level rather than unit tests of the keeper.
// The session's obligations are about what happens to somebody's words across a
// reload, a refusal and a lost server, and each of them is a statement about the
// editor, the store and the transport together. The rule underneath all of them
// — nothing here ever submits a recovered draft by itself — is asserted case by
// case, as `save` and `create` never having been called.

export const VAULT = "sync:vault-under-test";
export const KEY = "alpha";
export const LOADED = "---\ntitle: Alpha\n---\n\nAlpha body.\n";
export const EDITED = "---\ntitle: Alpha\n---\n\nAlpha body, edited.\n";

export const saveSuccess = (payload: Partial<SourceSaveResponse> = {}): SourceSaveResponse =>
  ({
    ok: true,
    key: KEY,
    path: "/n/alpha",
    rel_path: "Alpha.md",
    revision: "r2",
    message: "Saved Alpha.md.",
    changed: true,
    created: false,
    replayed: false,
    ...payload,
  }) as SourceSaveResponse;

export const saveFailure = (payload: {
  code: string;
  message?: string;
  revision?: string;
  content?: string;
}): SourceSaveResponse => ({
  ok: false,
  message: "",
  revision: "",
  content: "",
  changed: false,
  attachments: [],
  ...payload,
});

export type ClientMocks = {
  read: ReturnType<typeof vi.fn>;
  preview: ReturnType<typeof vi.fn>;
  save: ReturnType<typeof vi.fn>;
  create: ReturnType<typeof vi.fn>;
  search: ReturnType<typeof vi.fn>;
};

export type Harness = {
  elements: SourceEditorElements;
  controller: ReturnType<typeof createSourceEditorController>;
  storage: FakeDraftStorage;
  surface(): FakeSurface;
  client: ClientMocks;
  navigate: ReturnType<typeof vi.fn>;
  status(): string;
  draftStatus(): string;
  recovery(): HTMLElement;
  recoveryKind(): string;
  drafts(): ReturnType<typeof readDrafts>;
};

export type MountOptions = {
  client?: Partial<Record<keyof SourceClient, unknown>>;
  storage?: FakeDraftStorage | null;
  draftID?: string;
  claims?: AttachmentClaims | null;
};

// Every controller mounted here is torn down afterwards. Two of its listeners
// are on the document and the window rather than on the markup, so clearing the
// body alone would leave an earlier test's editor still answering `storage` and
// `beforeunload` for the rest of the file — and that window is the very channel
// two of these tests use.
export const mounted: { destroy(): void }[] = [];

export const mount = (options: MountOptions = {}): Harness => {
  document.body.innerHTML = editorMarkup();
  const panel = document.querySelector<HTMLElement>("[data-source-editor]");
  if (!panel) {
    throw new Error("fixture is missing the editor panel");
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    throw new Error("fixture does not describe a usable editor");
  }
  expect(config.vaultID).toBe(VAULT);

  const client = {
    read: vi.fn().mockResolvedValue({
      key: KEY,
      path: "/n/alpha",
      rel_path: "Alpha.md",
      revision: "r1",
      content: LOADED,
    }),
    preview: vi.fn().mockResolvedValue("<p>Alpha body.</p>"),
    save: vi.fn().mockResolvedValue(saveSuccess()),
    create: vi.fn().mockResolvedValue(saveSuccess({ created: true })),
    search: vi.fn().mockResolvedValue([]),
    ...options.client,
  } as unknown as ClientMocks;

  const storage = options.storage === undefined ? memoryDraftStorage() : options.storage;
  const now = () => 10_000;
  const surfaces = createFakeSurfaceFactory();
  const navigate = vi.fn();
  const controller = createSourceEditorController({
    elements,
    config,
    client: client as unknown as SourceClient,
    createSurface: surfaces.factory,
    previewDelayMs: 0,
    drafts: createSourceDraftStore({
      vaultID: config.vaultID,
      noteKey: config.key,
      draftID: options.draftID ?? "tab-this",
      storage,
      now,
    }),
    draftDelayMs: 0,
    now,
    navigate,
    claims: options.claims ?? null,
  });
  mounted.push(controller);

  return {
    elements,
    controller,
    client,
    navigate,
    storage: storage as FakeDraftStorage,
    surface: () => {
      const surface = surfaces.surface();
      if (!surface) {
        throw new Error("the surface was never mounted");
      }
      return surface;
    },
    status: () => elements.status?.textContent ?? "",
    draftStatus: () => elements.draftStatus?.textContent ?? "",
    recovery: () => {
      if (!elements.recovery) {
        throw new Error("fixture is missing the recovery panel");
      }
      return elements.recovery;
    },
    recoveryKind: () => elements.recovery?.getAttribute("data-source-recovery-kind") ?? "",
    drafts: () => readDrafts(storage as FakeDraftStorage),
  };
};

// The keep is debounced, so a test that types has to let the timer run. Zero is
// still a macrotask.
export const tick = (): Promise<void> => new Promise((resolve) => setTimeout(resolve, 0));

export const seedResume = (storage: FakeDraftStorage, overrides: Record<string, unknown> = {}) =>
  seedDraft(storage, {
    vaultID: VAULT,
    noteKey: KEY,
    draftID: "tab-earlier",
    relPath: "Alpha.md",
    baseRevision: "r1",
    content: EDITED,
    updatedAt: 9_000,
    ...overrides,
  });

// The save that comes back "there is no such note": a rename, a move, or a
// deletion that happened somewhere else while this tab was open.
export const refusedSave = () => ({
  save: vi
    .fn()
    .mockResolvedValue(
      saveFailure({ code: notFoundCode, message: "source: Alpha.md is no longer in the vault" })
    ),
});

export const cleanup = (): void => {
  while (mounted.length > 0) {
    mounted.pop()?.destroy();
  }
  document.body.innerHTML = "";
  vi.restoreAllMocks();
};
