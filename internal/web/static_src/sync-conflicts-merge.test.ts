// @vitest-environment jsdom
import { expect, it, vi } from "vitest";
import {
  chooseSyncPath,
  createSyncServer,
  mountSyncConflicts,
  syncAction,
  syncMergeNotice,
  syncMergePane,
  syncOperationStatus,
  syncPanelStatus,
} from "./sync-conflicts.test-helpers";
import { memoryDraftStorage, readDrafts, seedDraft } from "./source-draft.test-helpers";
import { mergeDraftKey } from "./sync-merge-editor";
import { syncContestedPath, syncJSON, syncOperation } from "./sync.test-helpers";

// The manual merge, driven through the review page rather than in isolation: the
// same editor the /edit page mounts, over one contested path, with the composed
// text travelling as part of the decision.
//
// The property this file exists for is the last one: a resolution the vault refused
// because it moved on must not cost somebody the merge they wrote, because that
// text is the only copy of itself anywhere.

const vaultID = "sync:vault-under-test";
const CONFLICTS = "/api/sync/conflicts";
const PREVIEW = "/api/sync/conflicts/preview";
const RESOLVE = "/api/sync/conflicts/resolve";

it("mounts the editor only once the merge is chosen, seeded with this vault's version", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  // A surface per contested path on a page full of conflicts is a lot of editor
  // nobody asked for, so nothing is mounted until it is wanted.
  expect(harness.surface()).toBeNull();
  expect(syncMergePane().hidden).toBe(true);

  chooseSyncPath("Alpha.md", "manual-merge");

  expect(syncMergePane().hidden).toBe(false);
  expect(harness.surface()?.getValue()).toBe("mine\n");
});

it("sends the composed merge as the decision's own text", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("mine and theirs\n");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  expect(server.lastBody(PREVIEW)).toEqual({
    token: "csrf-token",
    package_id: "pkg-1",
    paths: [{ path: "Alpha.md", choice: "manual-merge", merged: "mine and theirs\n" }],
  });
});

it("drops the reviewed plan when the merge is edited after it was read", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  harness.surface()?.type("second thoughts\n");

  expect(syncOperationStatus().textContent).toBe(
    "The merge changed, so it has to be reviewed again."
  );
  expect(syncAction("apply").hidden).toBe(true);
  expect(syncAction("review").hidden).toBe(false);
});

it("keeps the merge when the vault moved on under it, and offers it back after the re-read", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      {
        ok: false,
        code: "preview_stale",
        message: "this vault moved on while the decision was being made",
      },
      409
    );
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("hand written merge\n");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  syncAction("apply").click();

  // The advice and the re-read arrive together, on the panel's own status rather
  // than on the card: the re-read replaces every card, so a sentence written onto
  // this decision would have lived for one network round trip.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "This vault moved on while the decision was being made, so nothing was applied. " +
        "Both versions are being read again; any merge you composed is still here. " +
        "1 change is waiting for a decision."
    )
  );
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW, RESOLVE, CONFLICTS]);

  // The reload tore the editor down, which is precisely when the only copy of the
  // merge had to be written: it is in this browser, keyed to this conflict.
  const drafts = readDrafts(harness.storage);
  expect(drafts).toHaveLength(1);
  expect(drafts[0].noteKey).toBe(mergeDraftKey("pkg-1", "Alpha.md"));
  expect(drafts[0].content).toBe("hand written merge\n");

  chooseSyncPath("Alpha.md", "manual-merge");

  expect(harness.surface()?.getValue()).toBe("hand written merge\n");
  expect(syncMergeNotice().textContent).toBe("A merge kept in this browser was loaded.");
});

it("does not promise a merge back when this browser refused to keep it", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      {
        ok: false,
        code: "preview_stale",
        message: "this vault moved on while the decision was being made",
      },
      409
    );
  // Storage this page *has* and cannot write to: a full origin quota, which is the
  // state that answers "there is somewhere to keep one" and drops the text anyway.
  const storage = memoryDraftStorage();
  storage.maxBytes = 20;
  const harness = mountSyncConflicts(server, { storage });
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("a merge with nowhere to go\n");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  syncAction("apply").click();

  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "This vault moved on while the decision was being made, so nothing was applied. " +
        "Both versions are being read again, and a merge you composed has to be typed " +
        "again: this page could not keep a copy of it. " +
        "1 change is waiting for a decision."
    )
  );
  // The sentence is true: the teardown's flush had nowhere to put it.
  expect(readDrafts(storage)).toEqual([]);
});

it("reports a merge on another card lost to a refusal that was not about staleness", async () => {
  const server = createSyncServer();
  // Two holds, because the merge at risk is often on the card nobody clicked: any
  // failure here re-reads the channel and a re-read replaces *every* card.
  server.state.holds = [
    syncOperation(),
    syncOperation({
      package_id: "pkg-2",
      operation_id: "op-2",
      sequence: 5,
      paths: [syncContestedPath({ path: "Beta.md" })],
    }),
  ];
  server.state.resolve = () =>
    syncJSON(
      {
        ok: false,
        code: "safety_error",
        message: "the content history is blocked, so nothing was written",
      },
      500
    );
  const storage = memoryDraftStorage();
  storage.maxBytes = 20;
  const harness = mountSyncConflicts(server, { storage });
  await harness.panel.load();

  // The merge is composed on the second card…
  chooseSyncPath("Beta.md", "manual-merge");
  harness.surface()?.type("a merge on the other card\n");

  // …and the decision is applied on the first, which is the Apply these helpers find.
  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  syncAction("apply").click();

  // The engine's own sentence about the vault, then the one loss nothing else on the
  // page would ever report. Without the second half this reads as "nothing happened"
  // over text that has just been destroyed.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "the content history is blocked, so nothing was written. " +
        "A merge you composed has to be typed again: this page could not keep a copy of it. " +
        "2 changes are waiting for a decision."
    )
  );
  expect(readDrafts(storage)).toEqual([]);
});

it("keeps quiet about merges when a failure lost none of them", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      {
        ok: false,
        code: "safety_error",
        message: "the content history is blocked, so nothing was written",
      },
      500
    );
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("a merge this browser can keep\n");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  syncAction("apply").click();

  // The refusal is about the vault, and "your merge is still here" beside it is noise:
  // the re-read seeds the editor straight back from this browser's copy.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "the content history is blocked, so nothing was written. " +
        "1 change is waiting for a decision."
    )
  );
  chooseSyncPath("Alpha.md", "manual-merge");
  expect(harness.surface()?.getValue()).toBe("a merge this browser can keep\n");
});

it("stops keeping a merge the resolution decided against", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("a merge nobody applied\n");
  // Second thoughts: the merge stays on screen, and the decision is somebody else's
  // version.
  chooseSyncPath("Alpha.md", "accept-incoming");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  server.state.holds = [];
  syncAction("apply").click();

  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "Alpha.md now holds your version, and your decision was recorded. " +
        "Nothing is waiting for a decision."
    )
  );
  // A resolution covers every contested path of its package, so this conflict is gone
  // and no page will ever offer that merge again. A merge is ineligible for eviction,
  // so one left here would hold quota another note's draft needs until it expired.
  expect(readDrafts(harness.storage)).toEqual([]);
});

it("says nothing about merges on a stale plan that had none", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      {
        ok: false,
        code: "preview_stale",
        message: "this vault moved on while the decision was being made",
      },
      409
    );
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "accept-incoming");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  syncAction("apply").click();

  // No merge was composed, so both halves of the promise are noise — and the half
  // saying text has to be typed again is noise that reads as a loss.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "This vault moved on while the decision was being made, so nothing was applied. " +
        "Both versions are being read again. " +
        "1 change is waiting for a decision."
    )
  );
});

it("opens a merge kept by an earlier visit rather than starting it over", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  seedDraft(harness.storage, {
    vaultID,
    noteKey: mergeDraftKey("pkg-1", "Alpha.md"),
    relPath: "Alpha.md",
    baseRevision: "sha-mine",
    draftID: "tab-1",
    content: "yesterday's merge\n",
    updatedAt: Date.now(),
  });

  await harness.panel.load();
  chooseSyncPath("Alpha.md", "manual-merge");

  expect(harness.surface()?.getValue()).toBe("yesterday's merge\n");
  expect(syncMergeNotice().textContent).toBe("A merge kept in this browser was loaded.");

  // And it is the decision's text without a keystroke, so an Apply pressed straight
  // away submits the recovered merge rather than this vault's version.
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  expect(server.lastBody(PREVIEW)).toMatchObject({
    paths: [{ path: "Alpha.md", choice: "manual-merge", merged: "yesterday's merge\n" }],
  });
});

it("stops keeping the merge once it is in the vault", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("applied merge\n");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  server.state.holds = [];
  syncAction("apply").click();

  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "Alpha.md now holds your version, and your decision was recorded. " +
        "Nothing is waiting for a decision."
    )
  );
  expect(readDrafts(harness.storage)).toEqual([]);
});

it("keeps nothing at all when the page was served without a vault identity", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server, { vaultID: "" });
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "manual-merge");
  harness.surface()?.type("nowhere but here\n");

  expect(syncMergeNotice().textContent).toBe(
    "This browser will not keep a copy of the merge, so it exists only on this page."
  );
  // Guessing a key is how one vault's merge is offered over another's on the same
  // origin, so a page that cannot say which vault it is keeps nothing.
  expect(harness.storage.keys()).toEqual([]);

  // The merge still travels: it is the decision's text either way.
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  expect(server.lastBody(PREVIEW)).toMatchObject({
    paths: [{ path: "Alpha.md", choice: "manual-merge", merged: "nowhere but here\n" }],
  });
});
