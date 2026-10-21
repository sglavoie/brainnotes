// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  buildUndoForm,
  managementMutationEventName,
  normalizeHistoryEntries,
  type HistoryEntry,
} from "./api";
import {
  formatHistoryTimestamp,
  historyConflictSummary,
  historyFileCountLabel,
  historyForceConsentMessage,
  initHistoryPanel,
} from "./history-panel";
import { jsonResponse, waitForExpect } from "./shared.test-helpers";

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

describe("history panel exact undo", () => {
  const setup = async (preview: unknown = previewBody({ requires_force: false, conflicts: [] }), confirm = true) => {
    renderPanel();
    const bodies: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const target = String(input);
      if (target === "/api/history/preview") {
        bodies.push(`preview:${String(init?.body)}`);
        return jsonResponse(preview);
      }
      if (target === "/api/undo") {
        bodies.push(`undo:${String(init?.body).replace(/&operation_id=[0-9a-f]{32}$/, "&operation_id=<id>")}`);
        return jsonResponse({ ok: true, message: "Undo restored 1 path.", entry_id: "entry-new" });
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    const confirmer = vi.fn(() => confirm);
    initHistoryPanel({ fetch: fetcher, confirm: confirmer });
    await waitForExpect(() => expect(document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.disabled).toBe(false));
    return { fetcher, bodies, confirmer };
  };

  it("previews latest, describes its paths and timestamp, and submits its exact identity", async () => {
    const { bodies, confirmer } = await setup();
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path."));
    expect(bodies).toEqual([
      "preview:token=csrf-token",
      "undo:token=csrf-token&id=entry-new&preview_id=preview-1&operation_id=<id>",
    ]);
    expect(confirmer).toHaveBeenCalledWith(expect.stringContaining("bn web frontmatter set"));
    expect(confirmer).toHaveBeenCalledWith(expect.stringContaining("Source.md"));
    expect(confirmer).toHaveBeenCalledWith(expect.stringContaining(formatHistoryTimestamp("2026-01-02T03:04:05Z")));
  });

  it("cancels after reading a preview without sending a write", async () => {
    const { bodies } = await setup(undefined, false);
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo canceled."));
    expect(bodies).toEqual(["preview:token=csrf-token"]);
  });

  it("discovers conflicts before writing and previews the same entry again before forcing", async () => {
    const { bodies, confirmer } = await setup(previewBody());
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    const force = document.querySelector<HTMLButtonElement>("[data-history-force]")!;
    await waitForExpect(() => expect(force.disabled).toBe(false));
    expect(confirmer).not.toHaveBeenCalled();
    force.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path."));
    expect(bodies).toEqual([
      "preview:token=csrf-token",
      "preview:token=csrf-token&id=entry-new",
      "undo:token=csrf-token&id=entry-new&force=1&confirm_force=1&preview_id=preview-1&operation_id=<id>",
    ]);
    expect(confirmer).toHaveBeenCalledWith(expect.stringContaining("replaces newer work"));
  });

  it("does not write or offer forcing when the preview identifies no entry", async () => {
    const { bodies, confirmer } = await setup(previewBody({ entry_id: "" }));
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toContain("Couldn't identify"));
    expect(confirmer).not.toHaveBeenCalled();
    expect(document.querySelector<HTMLButtonElement>("[data-history-force]")?.disabled).toBe(true);
    expect(bodies).toEqual(["preview:token=csrf-token"]);
  });

  it("does not reattempt a write when its pinned receipt is stale", async () => {
    const { fetcher, bodies } = await setup();
    fetcher.mockResolvedValueOnce(jsonResponse(previewBody({ requires_force: false, conflicts: [] })))
      .mockResolvedValueOnce(jsonResponse({ ok: false, code: "preview_stale", message: "Preview expired. Read it again." }, 409));
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toContain("Preview expired"));
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(bodies).toEqual([]);
  });
});

describe("history panel lost undo reply", () => {
  it("offers an outcome check when the undo reply is lost, and reloads once it is confirmed", async () => {
    renderPanel();
    let operationID = "";
    const fetcher = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const target = String(input);
      if (target === "/api/history/preview") return jsonResponse(previewBody({ requires_force: false, conflicts: [] }));
      if (target === "/api/undo") {
        operationID = new URLSearchParams(String(init?.body)).get("operation_id") || "";
        throw new TypeError("network down");
      }
      if (target.startsWith("/api/operations/outcome")) {
        expect(target).toBe(`/api/operations/outcome?id=${operationID}`);
        return jsonResponse({ ok: true, status: "confirmed", journal_id: "entry-undo" });
      }
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    initHistoryPanel({ fetch: fetcher, confirm: () => true });
    await waitForExpect(() => expect(document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")?.disabled).toBe(false));
    document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!.click();
    await waitForExpect(() => expect(document.querySelector(".write-outcome-check")).not.toBeNull());
    expect(operationID).toMatch(/^[0-9a-f]{32}$/);
    document.querySelector<HTMLButtonElement>(".write-outcome-check")!.click();
    await waitForExpect(() => expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo confirmed."));
  });
});

describe("history panel reloads", () => {
  type Pending = { resolve: (response: Response) => void };
  const deferred = (): { promise: Promise<Response> } & Pending => {
    let resolve: (response: Response) => void = () => {};
    const promise = new Promise<Response>((done) => { resolve = done; });
    return { promise, resolve };
  };
  const status = (): string => document.querySelector("[data-history-status]")?.textContent || "";
  const undoLatest = (): HTMLButtonElement => document.querySelector<HTMLButtonElement>("[data-history-undo-latest]")!;

  const setup = async (route: (target: string) => Promise<Response> | null) => {
    renderPanel();
    const historyLoads: string[] = [];
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      const target = String(input);
      const routed = route(target);
      if (routed) return routed;
      if (target === "/api/history/preview") return jsonResponse(previewBody({ requires_force: false, conflicts: [] }));
      if (target === "/api/undo") return jsonResponse({ ok: true, message: "Undo restored 1 path." });
      historyLoads.push(target);
      return jsonResponse({ ok: true, entries: [historyEntry()] });
    });
    initHistoryPanel({ fetch: fetcher, confirm: () => true });
    await waitForExpect(() => expect(undoLatest().disabled).toBe(false));
    return { historyLoads };
  };

  it("announces a confirmed undo to other panels and reloads itself once", async () => {
    const { historyLoads } = await setup(() => null);
    const announced = vi.fn();
    window.addEventListener(managementMutationEventName, announced);
    undoLatest().click();
    await waitForExpect(() => expect(status()).toBe("Undo restored 1 path."));
    window.removeEventListener(managementMutationEventName, announced);
    expect(announced).toHaveBeenCalledTimes(1);
    expect(historyLoads).toHaveLength(2);
  });

  it("returns focus to the rerendered row after undoing from it", async () => {
    await setup(() => null);
    const row = document.querySelector<HTMLButtonElement>('[data-history-undo-entry="entry-new"]')!;
    row.focus();
    row.click();
    await waitForExpect(() => expect(status()).toBe("Undo restored 1 path."));
    expect(row.isConnected).toBe(false);
    expect(document.activeElement?.getAttribute("data-history-undo-entry")).toBe("entry-new");
  });

  it("holds a mutation reload until an in-flight undo finishes", async () => {
    const undo = deferred();
    const { historyLoads } = await setup((target) => (target === "/api/undo" ? undo.promise : null));
    undoLatest().click();
    await waitForExpect(() => expect(status()).toBe("Undoing change…"));
    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(historyLoads).toHaveLength(1);
    expect(undoLatest().disabled).toBe(true);
    undo.resolve(jsonResponse({ ok: true, message: "Undo restored 1 path." }));
    await waitForExpect(() => expect(status()).toBe("Undo restored 1 path."));
    expect(historyLoads).toHaveLength(2);
  });

  it("keeps the newest list when an older reload answers last", async () => {
    const first = deferred();
    const second = deferred();
    const queue: Array<{ promise: Promise<Response> }> = [];
    await setup((target) => (target.startsWith("/api/history?") || target === "/api/history" ? queue.shift()?.promise ?? null : null));
    queue.push(first, second);
    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    window.dispatchEvent(new CustomEvent(managementMutationEventName));
    second.resolve(jsonResponse({ ok: true, entries: [historyEntry({ id: "entry-newest", summary: "Newest" })] }));
    await waitForExpect(() => expect(status()).toBe("History refreshed."));
    first.resolve(jsonResponse({ ok: true, entries: [historyEntry({ id: "entry-stale", summary: "Stale" })] }));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(document.querySelector("[data-history-list]")?.textContent).toContain("Newest");
    expect(document.querySelector("[data-history-list]")?.textContent).not.toContain("Stale");
  });
});
