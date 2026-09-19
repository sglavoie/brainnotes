// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { renderSyncOperations, renderSyncPlan } from "./sync-conflict-render";
import {
  acceptIncoming,
  keepBoth,
  keepCurrent,
  manualMergeChoice,
  syncBinaryVersion,
  syncBlocked,
  syncChoice,
  syncContestedPath,
  syncMissingVersion,
  syncOperation,
  syncTextVersion,
} from "./sync.test-helpers";
import type { SyncConflictOperation } from "./api";

// The review markup. Two of its properties are the session's own: both versions
// are rendered in full, because reading them is the whole task; and a choice the
// engine refuses is rendered disabled *with its reason* rather than dropped,
// because a button that was never there is what somebody hunts for.

let host: HTMLElement;

beforeEach(() => {
  document.body.innerHTML = "<div></div>";
  host = document.querySelector("div") as HTMLElement;
});

const render = (...operations: SyncConflictOperation[]) =>
  renderSyncOperations(host, operations, document);

const versionText = (branch: string): string | null =>
  host.querySelector(`[data-sync-version-text="${branch}"]`)?.textContent ?? null;

const versionNote = (branch: string): string =>
  host.querySelector(`[data-sync-version="${branch}"] .sync-version-note`)?.textContent ?? "";

const choiceRadio = (choice: string): HTMLInputElement | null =>
  host.querySelector<HTMLInputElement>(`[data-sync-choice="${choice}"]`);

const reasonFor = (choice: string): string =>
  choiceRadio(choice)?.closest("label")?.querySelector(".sync-choice-reason")?.textContent ?? "";

it("renders both versions in full, with the baseline behind a disclosure", () => {
  render(syncOperation());

  expect(versionText("local")).toBe("mine\n");
  expect(versionText("remote")).toBe("theirs\n");
  expect(host.querySelector("details.sync-baseline")?.textContent).toContain("both\n");
  expect(host.querySelector("[data-sync-version='local'] .sync-version-title")?.textContent).toBe(
    "This vault's version"
  );
});

it("says which kind of unshowable a version is instead of rendering it empty", () => {
  render(
    syncOperation({
      paths: [
        syncContestedPath({
          local: syncBinaryVersion(4096),
          remote: syncMissingVersion(),
          baseline: syncTextVersion("", { shown: false, too_large: true, size: 2_000_000 }),
          mergeable: false,
        }),
      ],
    })
  );

  expect(versionNote("local")).toBe("Not text (4096 bytes), so it is not shown here.");
  expect(versionNote("remote")).toBe(
    "Not there: the other replica's change removes this file from this path."
  );
  expect(versionNote("baseline")).toBe("Too large to show here (2000000 bytes).");
  expect(versionText("local")).toBeNull();
});

it("renders a refused choice disabled and carrying its reason", () => {
  render(
    syncOperation({
      paths: [
        syncContestedPath({
          available: [keepCurrent(), acceptIncoming()],
          refused: [
            syncChoice("keep-both", {
              reason: "a configuration conflict has nowhere to put a second copy.",
            }),
            syncChoice("manual-merge", { reason: "" }),
          ],
          mergeable: false,
        }),
      ],
    })
  );

  expect(choiceRadio("keep-both")?.disabled).toBe(true);
  expect(reasonFor("keep-both")).toBe("a configuration conflict has nowhere to put a second copy.");
  // A refusal the engine gave no words for still gets a sentence.
  expect(reasonFor("manual-merge")).toBe("Not available for this conflict.");
  expect(choiceRadio("keep-current")?.disabled).toBe(false);
});

it("offers the merge disabled with its reason when the page cannot seed it", () => {
  render(
    syncOperation({
      paths: [
        syncContestedPath({
          local: syncBinaryVersion(),
          remote: syncBinaryVersion(8192),
          mergeable: false,
        }),
      ],
    })
  );

  expect(choiceRadio("manual-merge")?.disabled).toBe(true);
  expect(reasonFor("manual-merge")).toBe(
    "Both versions have to be text shown on this page before they can be merged here."
  );
  // No editor either: a merge pane for an attachment is an invitation to retype it.
  expect(host.querySelector("[data-sync-merge]")).toBeNull();
});

it("builds the merge pane hidden, with the parts the controller mounts into", () => {
  const rendered = render(syncOperation());
  const path = rendered[0].paths[0];

  expect(path.merge?.hidden).toBe(true);
  expect(path.mergeHost?.getAttribute("data-sync-merge-host")).toBe("Alpha.md");
  expect(path.mergeNotice?.hidden).toBe(true);
  expect(path.mergeReset?.textContent).toBe("Start again");
  expect(path.radios.map((radio) => radio.value)).toEqual([
    "keep-current",
    "accept-incoming",
    "keep-both",
    "manual-merge",
  ]);
});

it("names what each choice discards, so a destructive one is visible before it is taken", () => {
  render(syncOperation());

  const discards = [...host.querySelectorAll(".sync-choice-discards")].map(
    (node) => node.textContent
  );
  expect(discards).toEqual([
    "Discards the other replica's version.",
    "Discards this vault's version.",
  ]);
});

it("offers a remedy when the engine can take no decision at all", () => {
  render(
    syncOperation({
      paths: [
        syncContestedPath({
          available: [],
          refused: [keepCurrent(), acceptIncoming(), keepBoth(), manualMergeChoice()],
          mergeable: false,
          remedy: "Rename one of the two files, then run bn sync.",
        }),
      ],
    })
  );

  expect(host.querySelector(".sync-path-remedy")?.textContent).toBe(
    "Rename one of the two files, then run bn sync."
  );
});

it("warns when this vault's version moved after the conflict was recorded", () => {
  render(syncOperation({ paths: [syncContestedPath({ local_changed: true })] }));

  expect(host.querySelector(".sync-path-warning")?.textContent).toContain(
    "changed after the conflict was recorded"
  );
});

it("heads a rename with its destination and a collision with its rival", () => {
  render(
    syncOperation({
      paths: [
        syncContestedPath({ path: "Notes/Gamma.md", renamed_to: "Notes/Delta.md" }),
        syncContestedPath({ path: "alpha.md", collides: "Alpha.md" }),
      ],
    })
  );

  const headings = [...host.querySelectorAll(".sync-path-name")].map((node) => node.textContent);
  expect(headings).toEqual(["Notes/Gamma.md → Notes/Delta.md", "alpha.md (collides with Alpha.md)"]);
});

it("names what is waiting behind this conflict, and what each one waits on", () => {
  render(
    syncOperation({
      blocked: [syncBlocked(), syncBlocked({ package_id: "pkg-3", sequence: 6, waiting_on: "pkg-2" })],
    })
  );

  expect(host.querySelector(".sync-blocked-summary")?.textContent).toBe(
    "2 later changes are waiting behind this one and will be applied once it is resolved."
  );
  const items = [...host.querySelectorAll(".sync-blocked-item")].map((node) => node.textContent);
  expect(items[0]).toBe(
    "pkg-2 from device-b (#5): predecessor behind pkg-1 — waiting for operation op-1"
  );
  expect(items[1]).toContain("pkg-3 from device-b (#6)");
  expect(items[1]).toContain("behind pkg-2");
});

it("counts one blocked change in the singular", () => {
  render(syncOperation({ blocked: [syncBlocked()] }));

  expect(host.querySelector(".sync-blocked-summary")?.textContent).toBe(
    "1 later change is waiting behind this one and will be applied once it is resolved."
  );
});

it("offers no decision on a hold it cannot describe", () => {
  const rendered = render(
    syncOperation({ paths: [syncContestedPath()], problem: "the retained copy could not be read" })
  );

  expect(host.querySelector(".sync-operation-problem")?.textContent).toBe(
    "This change cannot be described here: the retained copy could not be read"
  );
  expect(rendered[0].paths).toEqual([]);
  expect(rendered[0].review).toBeNull();
  expect(rendered[0].apply).toBeNull();
  // Nothing on this card can be submitted, so no radio is rendered to suggest it.
  expect(host.querySelector("[data-sync-choice]")).toBeNull();
});

it("names the package and the operation, so a hold can be matched to the command line", () => {
  render(syncOperation());

  expect(host.querySelector(".sync-operation-title")?.textContent).toBe(
    "Change from device-b (#4)"
  );
  expect(host.querySelector(".sync-operation-meta")?.textContent).toBe(
    "Package pkg-1. Operation op-1. Recorded 2026-02-03T10:00:00Z."
  );
});

it("replaces what it rendered before, so a reload leaves nothing of the old holds", () => {
  render(syncOperation());
  render(syncOperation({ package_id: "pkg-9" }));

  expect(host.querySelectorAll("[data-sync-operation]")).toHaveLength(1);
  expect(host.querySelector("[data-sync-operation]")?.getAttribute("data-sync-operation")).toBe(
    "pkg-9"
  );
});

it("lists the plan step by step, and says so when a choice revises nothing", () => {
  const plan = document.createElement("div");
  plan.hidden = true;

  renderSyncPlan(
    plan,
    [
      {
        path: "Alpha.md",
        kind: "concurrent-edit",
        choice: "keep-current",
        label: "Keep this vault's version",
        steps: [{ path: "Alpha.md", action: "write" }],
      },
      {
        path: "Beta.md",
        kind: "delete-edit",
        choice: "accept-incoming",
        label: "Take the other replica's version",
        steps: [],
      },
    ],
    document
  );

  expect(plan.hidden).toBe(false);
  const items = [...plan.querySelectorAll(".sync-plan-item")].map((node) => node.textContent);
  // The words the radio used, not the engine's slug: this box is read by somebody
  // about to apply it, and "keep-current" is the one audience that never wanted it.
  expect(items[0]).toBe("Alpha.md — Keep this vault's version: write Alpha.md");
  expect(items[1]).toBe(
    "Beta.md — Take the other replica's version: the other replica's change is applied " +
      "and nothing further is written"
  );
});

it("falls back to the choice a server sent no words for", () => {
  const plan = document.createElement("div");

  renderSyncPlan(
    plan,
    [
      {
        path: "Alpha.md",
        kind: "concurrent-edit",
        choice: "keep-current",
        label: "",
        steps: [{ path: "Alpha.md", action: "write" }],
      },
    ],
    document
  );

  // A nameless choice is still a decision this box has to describe, and the slug
  // says more than the empty string between two dashes would.
  expect(plan.querySelector(".sync-plan-item")?.textContent).toBe(
    "Alpha.md — keep-current: write Alpha.md"
  );
});
