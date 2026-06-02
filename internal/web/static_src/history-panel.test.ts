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
  file_count: 1,
  files: [{ path: "Source.md", before: "file", after: "file" }],
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
          file_count: "2",
          files: [{ path: " A.md ", before: " file ", after: " missing " }],
        },
        { id: "" },
      ],
    });

    expect(entries).toEqual([{
      id: "entry-1",
      timestamp: "2026-01-02T03:04:05Z",
      command: "bn web trash",
      file_count: 2,
      files: [{ path: "A.md", before: "file", after: "missing" }],
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
    });
    expect(body.get("token")).toBe("csrf-token");
    expect(body.get("id")).toBe("entry-1");
    expect(body.get("force")).toBe("1");
    expect(body.get("confirm_force")).toBe("1");
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

  it("shows conflict previews and force-undoes after confirmation", async () => {
    renderPanel();
    let undoCalls = 0;
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input) === "/api/undo") {
        undoCalls += 1;
        if (undoCalls === 1) {
          expect(String(init?.body)).toBe("token=csrf-token&id=entry-new");
          return jsonResponse({
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
          }, 409);
        }
        expect(String(init?.body)).toBe(
          "token=csrf-token&id=entry-new&force=1&confirm_force=1"
        );
        return jsonResponse({
          ok: true,
          message: "Undo restored 1 path.",
          entry_id: "entry-new",
        });
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
    expect(confirmMock).toHaveBeenCalledWith("Undo this change?");
    expect(confirmMock).toHaveBeenCalledWith("Force undo and overwrite conflicting paths?");
    expect(undoCalls).toBe(2);
  });
});
