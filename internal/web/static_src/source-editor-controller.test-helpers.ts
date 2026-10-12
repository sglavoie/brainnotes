import { vi } from "vitest";
import { createSourceEditorController } from "./source-editor-controller";
import {
  readSourceEditorConfig,
  readSourceEditorElements,
  type SourceEditorConfig,
  type SourceEditorElements,
} from "./source-editor-elements";
import {
  createFakeSurfaceFactory,
  editorMarkup,
  type FakeSurface,
} from "./source-editor.test-helpers";
import type { AttachmentClaims, SourceClient } from "./source-save";
import type { SourceSaveResponse } from "./api";

export const LOADED = "---\ntitle: Alpha\n---\n\nAlpha body.\n";

export type Harness = {
  elements: SourceEditorElements;
  config: SourceEditorConfig;
  controller: ReturnType<typeof createSourceEditorController>;
  surface(): FakeSurface;
  client: {
    read: ReturnType<typeof vi.fn>;
    preview: ReturnType<typeof vi.fn>;
    save: ReturnType<typeof vi.fn>;
    search: ReturnType<typeof vi.fn>;
  };
  status(): string;
  saveButton(): HTMLButtonElement;
  conflict(): HTMLElement;
};

// Every controller mounted here is torn down afterwards, and that is not
// tidiness. Two of its listeners are on the document and the window rather than
// on the markup, so clearing `document.body` leaves them bound to a detached
// fixture: a dirty editor from an earlier test goes on answering `beforeunload`
// for the rest of the file.
export const mounted: { destroy(): void }[] = [];

export const mount = (
  overrides: Partial<SourceClient> = {},
  previewDelayMs = 0,
  claims: AttachmentClaims | null = null
): Harness => {
  document.body.innerHTML = editorMarkup();
  document.title = "Edit Alpha - bn serve";
  const panel = document.querySelector<HTMLElement>("[data-source-editor]");
  if (!panel) {
    throw new Error("fixture is missing the editor panel");
  }
  const config = readSourceEditorConfig(panel);
  const elements = readSourceEditorElements(panel);
  if (!config || !elements) {
    throw new Error("fixture does not describe a usable editor");
  }

  const client = {
    read: vi.fn().mockResolvedValue({
      key: "alpha",
      path: "/n/alpha",
      rel_path: "Alpha.md",
      revision: "r1",
      content: LOADED,
    }),
    preview: vi.fn().mockResolvedValue("<p>Alpha body.</p>"),
    save: vi.fn().mockResolvedValue({ ok: true, revision: "r2", message: "Saved Alpha.md." }),
    search: vi.fn().mockResolvedValue([]),
    ...overrides,
  };

  const surfaces = createFakeSurfaceFactory();
  const controller = createSourceEditorController({
    elements,
    config,
    client: client as unknown as SourceClient,
    createSurface: surfaces.factory,
    previewDelayMs,
    claims,
  });
  mounted.push(controller);

  return {
    elements,
    config,
    controller,
    client: client as Harness["client"],
    surface: () => {
      const surface = surfaces.surface();
      if (!surface) {
        throw new Error("the surface has not been mounted yet");
      }
      return surface;
    },
    status: () => elements.status?.textContent ?? "",
    saveButton: () => {
      if (!elements.save) {
        throw new Error("fixture is missing the save button");
      }
      return elements.save;
    },
    conflict: () => {
      if (!elements.conflict) {
        throw new Error("fixture is missing the conflict panel");
      }
      return elements.conflict;
    },
  };
};

export const saveFailure = (payload: Partial<SourceSaveResponse> & { ok: false }): SourceSaveResponse =>
  ({
    code: "",
    message: "",
    revision: "",
    content: "",
    changed: false,
    attachments: [],
    ...payload,
  }) as SourceSaveResponse;

export const saveSuccess = (payload: Partial<SourceSaveResponse> = {}): SourceSaveResponse =>
  ({
    ok: true,
    key: "",
    path: "",
    rel_path: "",
    revision: "r2",
    message: "Saved.",
    changed: true,
    created: false,
    replayed: false,
    attachments: [],
    ...payload,
  }) as SourceSaveResponse;

// conflicting refuses the first save with the stale-revision envelope and
// accepts everything after it, which is what every conflict below starts from.
export const conflicting = (): Harness =>
  mount({
    save: vi
      .fn()
      .mockResolvedValueOnce(
        saveFailure({
          ok: false,
          code: "conflict",
          message: "source: Alpha.md changed since it was loaded, so nothing was written",
          revision: "r9",
          content: "Somebody else's text.\n",
        })
      )
      .mockResolvedValue({ ok: true, revision: "r10", message: "Saved Alpha.md." }) as unknown as SourceClient["save"],
  });

// inConflict drives one to the point where the panel is up and the draft is
// still the editor's.
export const inConflict = async (): Promise<Harness> => {
  const harness = conflicting();
  await harness.controller.load();
  harness.surface().type("my draft\n");
  await harness.controller.save();
  return harness;
};

export const deferred = <T,>(): { promise: Promise<T>; resolve: (value: T) => void } => {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((settle) => {
    resolve = settle;
  });
  return { promise, resolve };
};

// savingOverAConflict is the overlap the two conflict buttons have to survive:
// the panel is up, and a *later* save is in flight and has not answered. The
// second response is a promise this test resolves by hand, because nothing else
// can hold a request open across a click.
export const savingOverAConflict = async (): Promise<{
  harness: Harness;
  pending: { resolve: (value: SourceSaveResponse) => void };
  inFlight: Promise<void>;
}> => {
  const pending = deferred<SourceSaveResponse>();
  const harness = mount({
    save: vi
      .fn()
      .mockResolvedValueOnce(
        saveFailure({
          ok: false,
          code: "conflict",
          message: "source: Alpha.md changed since it was loaded, so nothing was written",
          revision: "r9",
          content: "Somebody else's text.\n",
        })
      )
      .mockReturnValueOnce(pending.promise) as unknown as SourceClient["save"],
  });
  await harness.controller.load();
  harness.surface().type("my draft\n");
  await harness.controller.save();

  harness.surface().type("my draft, revised\n");
  const inFlight = harness.controller.save();
  return { harness, pending, inFlight };
};

// A staged upload is a file on the server that no note mentions. The save is
// what turns it into part of the vault, so these tests are about the two halves
// of that handover: the claim going out with the request, and the page stopping
// owing an id only on the strength of what came back.
export type StubClaims = AttachmentClaims & { settled: readonly unknown[][] };

export const stubClaims = (pending: string[]): StubClaims => {
  const settled: readonly unknown[][] = [];
  return {
    owner: "tab-7",
    pending: () => pending,
    committed: (saved) => {
      (settled as unknown[][]).push([...saved]);
    },
    settled,
  };
};

export const committed = [{ upload_id: "u-1", rel_path: "attachments/shot.png" }];

export const cleanup = (): void => {
  while (mounted.length > 0) {
    mounted.pop()?.destroy();
  }
  document.body.innerHTML = "";
  vi.restoreAllMocks();
};
