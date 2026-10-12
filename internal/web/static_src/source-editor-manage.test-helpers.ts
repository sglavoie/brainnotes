import { vi } from "vitest";

import { readSourceManageElements, type SourceEditorConfig } from "./source-editor-elements";
import { initSourceManage, type ManageHost } from "./source-editor-manage";

export const manageMarkup = `
<section data-source-editor>
  <details data-source-manage open>
    <p data-source-manage-status></p>
    <section data-source-gate hidden>
      <p data-source-gate-message></p>
      <button type="button" data-source-gate-save>Save first</button>
      <button type="button" data-source-gate-proceed hidden>Continue anyway</button>
      <button type="button" data-source-gate-cancel>Cancel</button>
    </section>
    <input type="text" data-source-rename-title>
    <button type="button" data-source-rename>Rename</button>
    <input type="text" data-source-move-folder>
    <button type="button" data-source-move>Move</button>
    <button type="button" data-source-trash>Move to Trash</button>
    <input type="text" data-source-property-name>
    <input type="text" data-source-property-value>
    <button type="button" data-source-property-set>Set</button>
    <button type="button" data-source-property-unset>Remove</button>
    <button type="button" data-source-pin>Pin</button>
    <button type="button" data-source-unpin>Unpin</button>
    <textarea data-source-capture-text></textarea>
    <button type="button" data-source-capture>Capture</button>
    <input type="text" data-source-note-create-target>
    <button type="button" data-source-note-create>Create</button>
    <button type="button" data-source-undo>Undo</button>
  </details>
</section>
`;

export const config: SourceEditorConfig = {
  key: "notes/alpha",
  token: "csrf-token",
  relPath: "notes/Alpha.md",
  vaultID: "sync:vault-under-test",
  maxBytes: 1048576,
  endpoints: {
    read: "/api/source",
    preview: "/api/source/preview",
    save: "/api/source/save",
    create: "/api/source/create",
    search: "/api/search",
  },
  attachments: { upload: "/api/attachments/upload", cancel: "/api/attachments/cancel" },
  manage: {
    editor: "/edit",
    home: "/",
    capture: "/api/capture",
    create: "/api/notes/create",
    rename: "/api/notes/rename",
    move: "/api/notes/move",
    trash: "/api/notes/trash",
    frontmatterSet: "/api/notes/frontmatter/set",
    frontmatterUnset: "/api/notes/frontmatter/unset",
    pinSet: "/api/pins/set",
    pinRemove: "/api/pins/remove",
    undo: "/api/undo",
  },
  maxAttachmentBytes: 26214400,
};

export const jsonResponse = (status: number, payload: unknown): Response =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });

export type Harness = {
  elements: NonNullable<ReturnType<typeof readSourceManageElements>>;
  host: ManageHost;
  fetcher: ReturnType<typeof vi.fn>;
  navigated: string[];
  reloads: number;
  confirmed: string[];
  setDirty(value: boolean): void;
  status(): { text: string; classes: string };
  click(selector: string): void;
  type(selector: string, value: string): void;
  dispose(): void;
};

export const harness = (options: { dirty?: boolean; confirm?: boolean } = {}): Harness => {
  document.body.innerHTML = manageMarkup;
  const panel = document.querySelector("[data-source-editor]") as HTMLElement;
  const elements = readSourceManageElements(panel);
  if (!elements) {
    throw new Error("the manage fixture is missing its root");
  }

  let dirty = options.dirty ?? false;
  let reloads = 0;
  const navigated: string[] = [];
  const confirmed: string[] = [];
  const fetcher = vi.fn().mockResolvedValue(jsonResponse(200, { ok: true, message: "Done." }));

  const host: ManageHost = {
    key: () => config.key,
    dirty: () => dirty,
    reload: async () => {
      reloads += 1;
    },
    navigate: (url) => navigated.push(url),
    save: async () => {
      dirty = false;
    },
    confirm: (message) => {
      confirmed.push(message);
      return options.confirm ?? true;
    },
  };

  const dispose = initSourceManage({
    elements,
    config,
    host,
    fetch: fetcher as unknown as typeof fetch,
  });

  return {
    elements,
    host,
    fetcher,
    navigated,
    confirmed,
    get reloads(): number {
      return reloads;
    },
    setDirty: (value: boolean) => {
      dirty = value;
    },
    status: () => ({
      text: elements.status?.textContent ?? "",
      classes: elements.status?.className ?? "",
    }),
    click: (selector: string) => panel.querySelector<HTMLButtonElement>(selector)?.click(),
    type: (selector: string, value: string) => {
      const input = panel.querySelector<HTMLInputElement | HTMLTextAreaElement>(selector);
      if (input) {
        input.value = value;
      }
    },
    dispose,
  };
};

export const sent = (fetcher: ReturnType<typeof vi.fn>, index = 0): { url: string; body: URLSearchParams } => {
  const [url, init] = fetcher.mock.calls[index] as [string, RequestInit];
  return { url, body: new URLSearchParams(init.body as string) };
};

export const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
