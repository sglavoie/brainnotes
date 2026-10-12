// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  cleanup,
  waitForExpect,
  renderWorkspace,
  expectReadyPreview,
  listBody,
  previewBody,
  contestedPreview,
  startWorkspace,
  listOnly,
  answerFor,
} from "./history-workspace.test-helpers";

afterEach(cleanup);

describe("history workspace restoring", () => {
  it("previews before it restores and sends no force when nothing is contested", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(previewBody(), {
      ok: true,
      message: "Undo restored 1 path.",
      entry_id: "entry-1",
    }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    expect(document.querySelector("[data-history-restore-facts]")?.textContent).toContain("Browser");
    expect(document.querySelector("[data-history-restore-changes]")?.textContent).toContain(
      "travels to your other replica"
    );
    expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(true);
    const confirm = document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]");
    expect(confirm?.disabled).toBe(false);
    expect(confirm?.textContent).toBe("Restore");

    confirm?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });
    expect(calls.map((call) => call.url)).toEqual([
      "/api/history?limit=20",
      "/api/history/preview",
      "/api/undo",
      "/api/history?limit=20",
    ]);
    expect(calls[2]?.body).toBe("token=csrf-token&id=entry-1&preview_id=preview-1");
    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  });

  it("will not restore over newer work until the replacement is acknowledged", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(contestedPreview(), {
      ok: true,
      message: "Undo restored 1 path.",
      entry_id: "entry-1",
    }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(false);
    });
    expect(document.querySelector("[data-history-restore-conflict-list]")?.textContent).toContain(
      "Source.md: the history holds file(sha256=a,size=1), this vault now holds file(sha256=b,size=1)"
    );
    const confirm = document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]");
    expect(confirm?.disabled).toBe(true);
    expect(confirm?.textContent).toBe("Restore and replace newer work");

    const consent = document.querySelector<HTMLInputElement>("[data-history-restore-consent]");
    if (!consent) {
      throw new Error("consent checkbox missing");
    }
    consent.checked = true;
    consent.dispatchEvent(new Event("change", { bubbles: true }));
    expect(confirm?.disabled).toBe(false);

    confirm?.click();
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe("Undo restored 1 path.");
    });
    expect(calls[2]?.body).toBe(
      "token=csrf-token&id=entry-1&force=1&confirm_force=1&preview_id=preview-1"
    );
  });

});

describe("history workspace restore refusals", () => {
  it("closes the panel and says to look again when the receipt went stale", async () => {
    renderWorkspace();
    startWorkspace(answerFor(
      contestedPreview(),
      { ok: false, code: "preview_stale", message: "Source.md changed since the preview." },
      409
    ));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expect(document.querySelector<HTMLElement>("[data-history-restore-conflicts]")?.hidden).toBe(false);
    });
    const consent = document.querySelector<HTMLInputElement>("[data-history-restore-consent]");
    if (!consent) {
      throw new Error("consent checkbox missing");
    }
    consent.checked = true;
    consent.dispatchEvent(new Event("change", { bubbles: true }));
    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();

    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe(
        "Source.md changed since the preview. Preview it again to see what changed."
      );
    });
    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
  });

  it("cancels without touching anything", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(previewBody(), { ok: true }));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    document.querySelector<HTMLButtonElement>("[data-history-restore-cancel]")?.click();

    expect(document.querySelector<HTMLElement>("[data-history-restore]")?.hidden).toBe(true);
    expect(document.querySelector("[data-history-status]")?.textContent).toBe(
      "Restore canceled. Nothing was changed."
    );
    expect(calls.some((call) => call.url === "/api/undo")).toBe(false);
  });

  it("refuses to restore with no preview in hand", async () => {
    renderWorkspace();
    const calls = startWorkspace(listOnly(listBody()));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();
    expect(document.querySelector("[data-history-status]")?.textContent).toBe(
      "Preview a change before restoring it."
    );
    expect(calls).toHaveLength(1);
  });

  // A restore error is not proof that nothing happened: a snapshot failure means
  // the files are already back and only the safety commit is missing, and a
  // failed rollback means content moved and could not be put back. Both leave a
  // vault the list on screen no longer describes.
  it("re-reads the history after a refused restore, not only after a successful one", async () => {
    renderWorkspace();
    const calls = startWorkspace(answerFor(
      previewBody(),
      { ok: false, code: "snapshot_failed", message: "The restore landed but could not be committed." },
      500
    ));
    await waitForExpect(() => {
      expect(document.querySelectorAll("[data-history-preview-entry]")).toHaveLength(1);
    });

    document.querySelector<HTMLButtonElement>("[data-history-preview-entry]")?.click();
    await waitForExpect(() => {
      expectReadyPreview();
    });
    document.querySelector<HTMLButtonElement>("[data-history-restore-confirm]")?.click();

    await waitForExpect(() => {
      expect(calls.filter((call) => call.url.startsWith("/api/history?")).length).toBe(2);
    });
    // The refusal is still what the person is left reading — the re-read must not
    // overwrite it with "Showing 1 of 1 recorded changes."
    await waitForExpect(() => {
      expect(document.querySelector("[data-history-status]")?.textContent).toBe(
        "The restore landed but could not be committed."
      );
    });
  });
});
