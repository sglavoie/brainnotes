// @vitest-environment jsdom

import { beforeEach, describe, expect, it, vi } from "vitest";

import { harness, jsonResponse, manageMarkup, sent } from "./source-editor-manage.test-helpers";

// The note's own management, now reachable from the page that is editing it.
//
// Nothing new exists on the server: these are the note page's routes with a new
// caller. What is new is that every one of them now runs beside a buffer the
// vault has never seen, so the assertions below are as much about what does
// *not* happen over unsaved text as about the requests that are sent.

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
