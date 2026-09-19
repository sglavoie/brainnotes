// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildUndoForm,
  normalizeHistoryEntries,
  type HistoryEntry,
} from "./api";
import {
  historyConflictSummary,
  historyFileCountLabel,
  historyForceConsentMessage,
  initHistoryPanel,
} from "./history-panel";

const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

const historyEntry = (overrides: Partial<HistoryEntry> = {}): HistoryEntry => ({
  id: "entry-new",
  timestamp: "2026-01-02T03:04:05Z",
  command: "bn web frontmatter set",
  origin: "browser",
  origin_label: "Browser",
  file_count: 1,
  classes: [{ class: "note", label: "Notes", count: 1 }],
  files: [{
    path: "Source.md",
    before: "file",
    after: "file",
    class: "note",
    recorded: true,
    travels: true,
  }],
  ...overrides,
});

const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

const renderPanel = (): void => {
  document.body.innerHTML = `
    <section data-history-panel data-history-csrf="csrf-token" aria-busy="false">
      <button type="button" data-history-refresh>Refresh</button>
      <button type="button" data-history-undo-latest disabled>Undo latest change</button>
      <p data-history-status></p>
      <div data-history-conflict hidden>
        <p data-history-conflict-summary></p>
        <button type="button" data-history-force disabled>Force undo</button>
      </div>
      <ul data-history-list></ul>
    </section>
  `;
};

const conflictBody = (): unknown => ({
  ok: false,
  code: "conflict",
  message: "Undo has conflicts.",
  entry_id: "entry-new",
  requires_force: true,
  conflicts: [{
    path: "Source.md",
    expected_state: "file(sha256=a,size=1)",
    current_state: "file(sha256=b,size=1)",
  }],
});

const previewBody = (overrides: Record<string, unknown> = {}): unknown => ({
  ok: true,
  preview_id: "preview-1",
  expires_at: "2026-01-02T03:09:05Z",
  entry_id: "entry-new",
  command: "bn web frontmatter set",
  origin: "browser",
  origin_label: "Browser",
  timestamp: "2026-01-02T03:04:05Z",
  requires_force: true,
  changes: [{
    path: "Source.md",
    current_state: "file(sha256=b,size=1)",
    restored_state: "file(sha256=a,size=1)",
    class: "note",
    recorded: true,
    travels: true,
    conflict: true,
  }],
  conflicts: [{
    path: "Source.md",
    expected_state: "file(sha256=a,size=1)",
    current_state: "file(sha256=b,size=1)",
  }],
  ...overrides,
});

afterEach(() => {
  document.body.innerHTML = "";
});

describe("history API helpers", () => {
  it("normalizes history entries and builds confirmed force undo forms", () => {
    const entries = normalizeHistoryEntries({
      entries: [
        {
          id: " entry-1 ",
          timestamp: " 2026-01-02T03:04:05Z ",
          command: " bn web trash ",
          origin: " browser ",
          origin_label: " Browser ",
          file_count: "2",
          classes: [{ class: " note ", label: " Notes ", count: "2" }, { class: "" }],
          files: [{
            path: " A.md ",
            before: " file ",
            after: " missing ",
            class: " note ",
            recorded: true,
            travels: true,
          }],
        },
        { id: "" },
      ],
    });

    expect(entries).toEqual([{
      id: "entry-1",
      timestamp: "2026-01-02T03:04:05Z",
      command: "bn web trash",
      origin: "browser",
      origin_label: "Browser",
      file_count: 2,
      classes: [{ class: "note", label: "Notes", count: 2 }],
      files: [{
        path: "A.md",
        before: "file",
        after: "missing",
        class: "note",
        recorded: true,
        travels: true,
      }],
    }]);
    expect(historyFileCountLabel(1)).toBe("1 path");
    expect(historyFileCountLabel(2)).toBe("2 paths");
    expect(historyConflictSummary([{
      path: "Source.md",
      expected_state: "file(sha256=a,size=1)",
      current_state: "file(sha256=b,size=1)",
    }])).toContain("Source.md: expected file(sha256=a,size=1), current file(sha256=b,size=1)");

    const body = buildUndoForm({
      token: "csrf-token",
      entryID: " entry-1 ",
      force: true,
      confirmForce: true,
      previewID: " preview-1 ",
    });
    expect(body.get("token")).toBe("csrf-token");
    expect(body.get("id")).toBe("entry-1");
    expect(body.get("force")).toBe("1");
    expect(body.get("confirm_force")).toBe("1");
    expect(body.get("preview_id")).toBe("preview-1");
  });

  it("names what forcing replaces rather than how many paths refused", () => {
    expect(historyForceConsentMessage([], 3)).toBe("Restore 3 paths and replace no newer work?");
    expect(historyForceConsentMessage(["A.md"], 1)).toBe(
      "Restore 1 path and replace newer work in A.md?"
    );
    expect(historyForceConsentMessage(["A.md", "B.md", "C.md", "D.md", "E.md"], 5)).toBe(
      "Restore 5 paths and replace newer work in A.md, B.md, C.md, and 2 more?"
    );
  });
});

describe("history panel DOM behavior", () => {
  it("loads history and posts a non-force latest undo", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/undo") {
        expect(String(init?.body)).toBe("token=csrf-token");
        return jsonResponse({
          ok: true,
          message: "Undo restored 1 path.",
          entry_id: "entry-new",
          changes: [{ path: "Source.md", current_state: "file", restored_state: "file" }],
        });
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    const confirmMock = vi.fn(() => true);

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: confirmMock });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-undo-entry]")).toHaveLength(1);
    });
    expect(
      document.querySelector<HTMLButtonElement>("[data-history-undo-entry]")?.getAttribute("aria-label")
    ).toBe("Undo bn web frontmatter set");

    const latest = document.querySelector<HTMLButtonElement>("[data-history-undo-latest]");
    expect(latest?.disabled).toBe(false);
    latest?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });
    expect(confirmMock).toHaveBeenCalledWith("Undo this change?");
    expect(fetchMock).toHaveBeenCalledWith("/api/undo", expect.objectContaining({
      method: "POST",
    }));
  });

  it("previews before forcing and sends the receipt back with the force", async () => {
    renderPanel();
    const bodies: string[] = [];
    let undoCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const target = String(input);
      if (target === "/api/history/preview") {
        bodies.push(`preview:${String(init?.body)}`);
        return jsonResponse(previewBody());
      }
      if (target === "/api/undo") {
        undoCalls += 1;
        bodies.push(`undo:${String(init?.body)}`);
        if (undoCalls === 1) {
          return jsonResponse(conflictBody(), 409);
        }
        return jsonResponse({ ok: true, message: "Undo restored 1 path.", entry_id: "entry-new" });
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    const confirmMock = vi.fn(() => true);

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: confirmMock });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-undo-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-undo-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-conflict]")?.hidden).toBe(false);
    });
    expect(document.querySelector("[data-history-conflict-summary]")?.textContent).toContain(
      "Source.md: expected file(sha256=a,size=1), current file(sha256=b,size=1)"
    );

    document.querySelector<HTMLButtonElement>("[data-history-force]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });

    expect(bodies).toEqual([
      "undo:token=csrf-token&id=entry-new",
      "preview:token=csrf-token&id=entry-new",
      "undo:token=csrf-token&id=entry-new&force=1&confirm_force=1&preview_id=preview-1",
    ]);
    expect(confirmMock).toHaveBeenCalledWith("Undo this change?");
    expect(confirmMock).toHaveBeenCalledWith(
      "Restore 1 path and replace newer work in Source.md?"
    );
    expect(undoCalls).toBe(2);
  });

});

// "Undo latest change" is posted with no identifier — the server resolves it.
// The refusal is therefore the only thing that says which entry was refused, and
// forcing has to name that entry: re-posting "the latest" with force would
// resolve it a second time, against a journal that may have gained an entry in
// between, and force is exactly the flag that would then overwrite without
// asking.
describe("history panel undo-latest conflicts", () => {
  it("forces the entry the refusal named rather than the shorthand it sent", async () => {
    renderPanel();
    const bodies: string[] = [];
    let undoCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const target = String(input);
      if (target === "/api/history/preview") {
        bodies.push(`preview:${String(init?.body)}`);
        return jsonResponse(previewBody());
      }
      if (target === "/api/undo") {
        undoCalls += 1;
        bodies.push(`undo:${String(init?.body)}`);
        if (undoCalls === 1) {
          return jsonResponse(conflictBody(), 409);
        }
        return jsonResponse({ ok: true, message: "Undo restored 1 path.", entry_id: "entry-new" });
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: vi.fn(() => true) });
    await waitForExpect(() => {
      expect(document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.disabled).toBe(false);
    });

    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-conflict]")?.hidden).toBe(false);
    });
    const force = document.querySelector<HTMLButtonElement>("[data-history-force]");
    expect(force?.disabled).toBe(false);

    force?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });

    expect(bodies).toEqual([
      "undo:token=csrf-token",
      "preview:token=csrf-token&id=entry-new",
      "undo:token=csrf-token&id=entry-new&force=1&confirm_force=1&preview_id=preview-1",
    ]);
  });

  it("leaves forcing unavailable when the refusal named no entry", async () => {
    renderPanel();
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      if (String(input) === "/api/undo") {
        return jsonResponse({ ...(conflictBody() as object), entry_id: "" }, 409);
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: vi.fn(() => true) });
    await waitForExpect(() => {
      expect(document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.disabled).toBe(false);
    });

    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-conflict]")?.hidden).toBe(false);
    });
    expect(document.querySelector<HTMLButtonElement>("[data-history-force]")?.disabled).toBe(true);
  });
});

describe("history panel force refusals", () => {
  it("refuses to force on a stale receipt instead of retrying without one", async () => {
    renderPanel();
    let undoCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const target = String(input);
      if (target === "/api/history/preview") {
        return jsonResponse({
          ok: false,
          code: "preview_stale",
          message: "Source.md changed since the preview. Read it again.",
        }, 409);
      }
      if (target === "/api/undo") {
        undoCalls += 1;
        return jsonResponse(conflictBody(), 409);
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    const confirmMock = vi.fn(() => true);

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: confirmMock });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-undo-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-undo-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-conflict]")?.hidden).toBe(false);
    });

    document.querySelector<HTMLButtonElement>("[data-history-force]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe(
        "Source.md changed since the preview. Read it again."
      );
    });
    expect(undoCalls).toBe(1);
    expect(confirmMock).toHaveBeenCalledTimes(1);
  });

  it("does not force when the preview is refused at the confirmation", async () => {
    renderPanel();
    let undoCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
      const target = String(input);
      if (target === "/api/history/preview") {
        return jsonResponse(previewBody());
      }
      if (target === "/api/undo") {
        undoCalls += 1;
        return jsonResponse(conflictBody(), 409);
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    const confirmMock = vi.fn((message: string) => message === "Undo this change?");

    initHistoryPanel({ fetch: fetchMock as typeof fetch, confirm: confirmMock });
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-undo-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-undo-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-conflict]")?.hidden).toBe(false);
    });

    document.querySelector<HTMLButtonElement>("[data-history-force]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Force undo canceled.");
    });
    expect(undoCalls).toBe(1);
  });
});
