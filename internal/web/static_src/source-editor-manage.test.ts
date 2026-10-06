// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

import { editorURLForKey, readManageOutcome } from "./source-manage-client";
import { initSourceManage, type ManageHost } from "./source-editor-manage";
import { readSourceManageElements, type SourceEditorConfig } from "./source-editor-elements";

// The note's own management, now reachable from the page that is editing it.
//
// Nothing new exists on the server: these are the note page's routes with a new
// caller. What is new is that every one of them now runs beside a buffer the
// vault has never seen, so the assertions below are as much about what does
// *not* happen over unsaved text as about the requests that are sent.

const manageMarkup = `
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

const config: SourceEditorConfig = {
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

const jsonResponse = (status: number, payload: unknown): Response =>
  new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });

type Harness = {
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

const harness = (options: { dirty?: boolean; confirm?: boolean } = {}): Harness => {
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

const sent = (fetcher: ReturnType<typeof vi.fn>, index = 0): { url: string; body: URLSearchParams } => {
  const [url, init] = fetcher.mock.calls[index] as [string, RequestInit];
  return { url, body: new URLSearchParams(init.body as string) };
};

beforeEach(() => {
  document.body.innerHTML = manageMarkup;
});

describe("initSourceManage: where the note lives", () => {
  it("refuses an empty field with a sentence rather than posting a blank one", () => {
    const h = harness();
    h.click("[data-source-rename]");
    expect(h.fetcher).not.toHaveBeenCalled();
    expect(h.status().text).toBe("Type the note's new title first.");
    expect(h.status().classes).toContain("is-error");
  });

  // The server is the only thing that knows what filename it settled on, so the
  // destination is built from the key it answered with.
  it("renames a clean note and reopens the editor on the key the server chose", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Renamed.", key: "notes/beta" }));
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");

    await vi.waitFor(() => expect(h.navigated).toEqual(["/edit?key=notes%2Fbeta"]));
    const request = sent(h.fetcher);
    expect(request.url).toBe("/api/notes/rename");
    expect(request.body.get("key")).toBe("notes/alpha");
    expect(request.body.get("title")).toBe("Beta");
    expect(request.body.get("token")).toBe("csrf-token");
  });

  // A rename files the kept draft under a note key that will not exist a moment
  // later, so this one never runs over unsaved text without a save first.
  it("holds a rename behind the gate while the editor is dirty", () => {
    const h = harness({ dirty: true });
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");

    expect(h.fetcher).not.toHaveBeenCalled();
    expect(h.elements.gate.panel?.hidden).toBe(false);
    expect(h.elements.gate.proceed?.hidden).toBe(true);
  });

  it("sends the rename once the gate's save has cleared the editor", async () => {
    const h = harness({ dirty: true });
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");
    h.elements.gate.save?.click();

    await vi.waitFor(() => expect(h.fetcher).toHaveBeenCalledOnce());
    expect(h.elements.gate.panel?.hidden).toBe(true);
  });

  it("moves a note to a folder and reopens on the new key", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Moved.", key: "area/alpha" }));
    h.type("[data-source-move-folder]", "area");
    h.click("[data-source-move]");

    await vi.waitFor(() => expect(h.navigated).toEqual(["/edit?key=area%2Falpha"]));
    expect(sent(h.fetcher).body.get("folder")).toBe("area");
  });

  it("asks before trashing, and leaves for the index once there is no note to edit", async () => {
    const h = harness();
    h.click("[data-source-trash]");
    await vi.waitFor(() => expect(h.navigated).toEqual(["/"]));
    expect(h.confirmed[0]).toContain("Move this note to Trash?");
    expect(sent(h.fetcher).url).toBe("/api/notes/trash");
  });

  it("does nothing at all when the trash confirmation is declined", () => {
    const h = harness({ confirm: false });
    h.click("[data-source-trash]");
    expect(h.fetcher).not.toHaveBeenCalled();
    expect(h.status().text).toContain("canceled");
  });

});

// The actions that rewrite this note's bytes behind the editor's back, and the
// two that touch the vault's own lists without touching them at all.
describe("initSourceManage: what the note holds", () => {
  it("re-reads the note after a property write when there is nothing unsaved", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Set area." }));
    h.type("[data-source-property-name]", "area");
    h.type("[data-source-property-value]", "work");
    h.click("[data-source-property-set]");

    await vi.waitFor(() => expect(h.reloads).toBe(1));
    const request = sent(h.fetcher);
    expect(request.url).toBe("/api/notes/frontmatter/set");
    expect(request.body.get("property")).toBe("area");
    expect(request.body.get("op")).toBe("=");
    expect(request.body.get("value")).toBe("work");
    expect(h.status().text).toBe("Set area.");
  });

  // The gate let them accept the note changing under them. It never let anything
  // replace the text in the editor, so a dirty editor is never reloaded.
  it("refuses to reload over unsaved text, and says what the state now is", async () => {
    const h = harness({ dirty: true });
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Set area." }));
    h.type("[data-source-property-name]", "area");
    h.click("[data-source-property-set]");
    h.elements.gate.proceed?.click();

    await vi.waitFor(() => expect(h.fetcher).toHaveBeenCalledOnce());
    await vi.waitFor(() => expect(h.status().text).toContain("Your unsaved text is still here"));
    expect(h.reloads).toBe(0);
  });

  it("removes a property by name alone", async () => {
    const h = harness();
    h.type("[data-source-property-name]", "area");
    h.click("[data-source-property-unset]");

    await vi.waitFor(() => expect(h.fetcher).toHaveBeenCalledOnce());
    const request = sent(h.fetcher);
    expect(request.url).toBe("/api/notes/frontmatter/unset");
    expect(request.body.get("property")).toBe("area");
    expect(request.body.has("value")).toBe(false);
  });

  it("pins and unpins without re-reading the note, because its bytes did not change", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Pinned." }));
    h.click("[data-source-pin]");
    await vi.waitFor(() => expect(h.status().text).toBe("Pinned."));
    expect(sent(h.fetcher).url).toBe("/api/pins/set");
    expect(h.reloads).toBe(0);

    h.click("[data-source-unpin]");
    await vi.waitFor(() => expect(h.fetcher).toHaveBeenCalledTimes(2));
    expect(sent(h.fetcher, 1).url).toBe("/api/pins/remove");
  });

});

// The actions that reach past this note — and the three answers that are not
// about any particular action.
describe("initSourceManage: the vault beyond this note", () => {
  // The bullet is echoed rather than reconstructed: the timestamp in it came
  // from the server's clock.
  it("captures a line, clears the box, and shows the bullet the server wrote", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(
      jsonResponse(200, { ok: true, message: "Captured.", detail: "- 09:41 buy milk" })
    );
    h.type("[data-source-capture-text]", "buy milk");
    h.click("[data-source-capture]");

    await vi.waitFor(() => expect(h.status().text).toBe("Captured. - 09:41 buy milk"));
    expect(sent(h.fetcher).body.get("text")).toBe("buy milk");
    expect(h.elements.captureText?.value).toBe("");
  });

  it("creates a note and names where it landed", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(
      jsonResponse(200, { ok: true, message: "Created.", rel_path: "notes/Beta.md" })
    );
    h.type("[data-source-note-create-target]", "Beta");
    h.click("[data-source-note-create]");

    await vi.waitFor(() => expect(h.status().text).toBe("Created. notes/Beta.md"));
    expect(sent(h.fetcher).body.get("target")).toBe("Beta");
    expect(h.elements.createTarget?.value).toBe("");
  });

  it("undoes the last change to the vault after asking", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, { ok: true, message: "Undid the rename." }));
    h.click("[data-source-undo]");

    await vi.waitFor(() => expect(h.reloads).toBe(1));
    expect(sent(h.fetcher).url).toBe("/api/undo");
    expect(h.status().text).toBe("Undid the rename.");
  });

  // A 409 from /api/undo means the paths on disk are not what the entry expected,
  // and the paths themselves are what a person needs to see.
  it("names the conflicting paths when an undo is refused, and where to force it", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(
      jsonResponse(409, {
        ok: false,
        message: "conflict",
        requires_force: true,
        conflicts: [
          { path: "notes/Alpha.md", expected_state: "missing", current_state: "present" },
        ],
      })
    );
    h.click("[data-source-undo]");

    await vi.waitFor(() => expect(h.status().classes).toContain("is-error"));
    expect(h.status().text).toContain("notes/Alpha.md");
    expect(h.status().text).toContain("the history panel can force it");
  });

  it("reports a refusal in the server's own words", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(409, { ok: false, message: "A note by that name exists." }));
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");

    await vi.waitFor(() => expect(h.status().text).toBe("A note by that name exists."));
    expect(h.navigated).toEqual([]);
  });

  it("reports a lost response as unconfirmed", async () => {
    const h = harness();
    h.fetcher.mockRejectedValue(new Error("offline"));
    h.click("[data-source-pin]");
    await vi.waitFor(() => expect(h.status().text).toContain("Couldn't confirm"));
    expect(h.status().text).not.toContain("Nothing was changed");
  });

  it("stops listening once disposed", () => {
    const h = harness();
    h.dispose();
    h.click("[data-source-pin]");
    expect(h.fetcher).not.toHaveBeenCalled();
  });
});

describe("readManageOutcome", () => {
  it("carries the key and path the operation ended at", () => {
    const outcome = readManageOutcome(200, true, {
      ok: true,
      message: "Renamed.",
      key: "notes/beta",
      rel_path: "notes/Beta.md",
    });
    expect(outcome).toMatchObject({
      ok: true,
      key: "notes/beta",
      relPath: "notes/Beta.md",
      message: "Renamed.",
    });
  });

  it("reads an ok:false body served with a 200 as a refusal", () => {
    const outcome = readManageOutcome(200, true, { ok: false, message: "No." });
    expect(outcome.ok).toBe(false);
    expect(outcome.message).toBe("No.");
  });

  it("falls back to the status when the body explains nothing", () => {
    expect(readManageOutcome(500, false, null).message).toBe("Request failed (500).");
  });

  it("defaults a success with no message to something a person can read", () => {
    expect(readManageOutcome(200, true, { ok: true }).message).toBe("Done.");
  });
});

// There is no field a response could carry that would turn a rename into a
// redirect somewhere else: the route comes from the page and the key is encoded.
describe("editorURLForKey", () => {
  it("encodes the key into the page's own editor route", () => {
    expect(editorURLForKey("/edit", "a/b c.md")).toBe("/edit?key=a%2Fb%20c.md");
  });

  it("cannot be talked into another origin", () => {
    expect(editorURLForKey("/edit", "https://evil.example/")).toBe(
      "/edit?key=https%3A%2F%2Fevil.example%2F"
    );
  });
});

const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};

describe("editor management confirmation", () => {
  it.each([null, {}, [], "Done", { ok: "true" }])("does not confirm a malformed success: %j", (payload) => {
    expect(readManageOutcome(200, true, payload)).toMatchObject({ ok: false, unconfirmed: true });
  });

  it.each(["lost", "invalid JSON", "missing confirmation"])("keeps a capture after %s", async (failure) => {
    const h = harness();
    if (failure === "lost") h.fetcher.mockRejectedValue(new Error("response lost"));
    else if (failure === "invalid JSON") h.fetcher.mockResolvedValue(new Response("{", {
      headers: { "Content-Type": "application/json" },
    }));
    else h.fetcher.mockResolvedValue(jsonResponse(200, {}));
    h.type("[data-source-capture-text]", "Keep this thought");
    h.click("[data-source-capture]");
    await vi.waitFor(() => expect(h.status().text).toContain("Check your inbox before retrying"));
    expect(h.elements.captureText?.value).toBe("Keep this thought");
    expect(h.elements.capture?.disabled).toBe(false);
    expect(h.fetcher).toHaveBeenCalledOnce();
    expect(h.reloads).toBe(0);
  });

  it("keeps the create target when creation is unconfirmed", async () => {
    const h = harness();
    h.fetcher.mockResolvedValue(jsonResponse(200, null));
    h.type("[data-source-note-create-target]", "New thought");
    h.click("[data-source-note-create]");
    await vi.waitFor(() => expect(h.status().text).toContain("Couldn't confirm"));
    expect(h.elements.createTarget?.value).toBe("New thought");
  });

  it.each(["rename", "move"])("does not navigate after %s without a confirmed destination", async (action) => {
    const h = harness();
    h.type("[data-source-rename-title]", "Beta");
    h.type("[data-source-move-folder]", "Notes");
    h.click(`[data-source-${action}]`);
    await vi.waitFor(() => expect(h.status().text).toContain("Couldn't confirm"));
    expect(h.navigated).toEqual([]);
  });
});

describe("editor management pending state", () => {
  it("blocks repeated and competing actions until the pending response is consumed", async () => {
    const h = harness();
    const response = deferred<Response>();
    h.fetcher.mockReturnValue(response.promise);
    h.type("[data-source-capture-text]", "One thought");
    h.click("[data-source-capture]");
    expect(h.elements.capture?.disabled).toBe(true);
    expect(h.elements.rename?.disabled).toBe(true);
    expect(h.elements.undo?.disabled).toBe(true);
    h.click("[data-source-capture]");
    h.elements.undo?.dispatchEvent(new MouseEvent("click"));
    expect(h.fetcher).toHaveBeenCalledOnce();
    response.resolve(jsonResponse(200, { ok: true, detail: "- One thought" }));
    await vi.waitFor(() => expect(h.elements.capture?.disabled).toBe(false));
    expect(h.elements.rename?.disabled).toBe(false);
    expect(h.elements.captureText?.value).toBe("");
    expect(h.fetcher).toHaveBeenCalledOnce();
  });

  it("restores prior disabled states after a refusal", async () => {
    const h = harness();
    h.elements.undo!.disabled = true;
    h.fetcher.mockResolvedValue(jsonResponse(409, { ok: false, message: "Conflict." }));
    h.click("[data-source-pin]");
    await vi.waitFor(() => expect(h.status().text).toBe("Conflict."));
    expect(h.elements.pin?.disabled).toBe(false);
    expect(h.elements.undo?.disabled).toBe(true);
  });

  it("blocks duplicate saves and cancels the continuation while saving", async () => {
    const h = harness({ dirty: true });
    const saved = deferred<void>();
    h.host.save = vi.fn().mockReturnValue(saved.promise);
    // initSourceManage captures host.save, so bind again with this delayed host.
    h.dispose();
    const dispose = initSourceManage({ elements: h.elements, config, host: h.host, fetch: h.fetcher as unknown as typeof fetch });
    h.type("[data-source-rename-title]", "Beta");
    h.click("[data-source-rename]");
    h.elements.gate.save?.click();
    h.elements.gate.save?.dispatchEvent(new MouseEvent("click"));
    h.click("[data-source-pin]");
    expect(h.host.save).toHaveBeenCalledOnce();
    expect(h.elements.pin?.disabled).toBe(true);
    h.elements.gate.cancel?.click();
    expect(h.status().text).toContain("The save is still in progress; this action will not run.");
    h.setDirty(false);
    saved.resolve();
    await vi.waitFor(() => expect(h.elements.pin?.disabled).toBe(false));
    expect(h.fetcher).not.toHaveBeenCalled();
    dispose();
  });
});

describe("editor management local feedback", () => {
  it("keeps loading, error, and success beside the control that initiated them", async () => {
    const h = harness();
    const group = document.createElement("div");
    group.dataset.sourceManageFeedback = "capture";
    const status = document.createElement("p");
    status.dataset.sourceManageStatus = "";
    h.elements.root.append(group);
    group.append(h.elements.capture!, status);
    const response = deferred<Response>();
    h.fetcher.mockReturnValueOnce(response.promise);
    h.type("[data-source-capture-text]", "Local feedback");
    h.click("[data-source-capture]");
    expect(status.textContent).toBe("Capture this line…");
    expect(h.status().text).toBe("");
    response.reject(new Error("lost"));
    await vi.waitFor(() => expect(status.textContent).toContain("Couldn't confirm"));
    h.fetcher.mockResolvedValueOnce(jsonResponse(200, { ok: true, message: "Captured." }));
    h.click("[data-source-capture]");
    await vi.waitFor(() => expect(status.textContent).toBe("Captured."));
    expect(h.status().text).toBe("");
  });
});
