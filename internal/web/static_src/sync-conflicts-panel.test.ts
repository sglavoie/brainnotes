// @vitest-environment jsdom
import { expect, it, vi } from "vitest";
import {
  chooseSyncPath,
  createSyncServer,
  mountSyncConflicts,
  syncAction,
  syncOperationStatus,
  syncPanelStatus,
  syncPlanBox,
} from "./sync-conflicts.test-helpers";
import {
  syncContestedPath,
  syncJSON,
  syncOperation,
  syncResolvedPayload,
} from "./sync.test-helpers";

// The review controller, driven the way a person drives it. What is asserted here
// is mostly the *sequence*: which requests were made, in what order, and — as
// often — which were not.

const CONFLICTS = "/api/sync/conflicts";
const PREVIEW = "/api/sync/conflicts/preview";
const RESOLVE = "/api/sync/conflicts/resolve";

it("decides, plans, confirms and applies a text conflict, then reads again", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);

  await harness.panel.load();
  expect(syncPanelStatus().textContent).toBe("1 change is waiting for a decision.");

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  expect(syncPlanBox().hidden).toBe(false);
  expect(syncPlanBox().textContent).toContain("write Alpha.md");
  // In the words of the control that chose it, so the plan and the radio above it
  // are not two vocabularies for one decision.
  expect(syncPlanBox().textContent).toContain("Keep this vault's version");
  expect(syncAction("review").hidden).toBe(true);
  expect(server.lastBody(PREVIEW)).toEqual({
    token: "csrf-token",
    package_id: "pkg-1",
    paths: [{ path: "Alpha.md", choice: "keep-current", merged: null }],
  });

  server.state.holds = [];
  syncAction("apply").click();
  // What durably happened is announced through the re-read, onto the panel's own
  // status: the re-read replaces every card, so a sentence written onto this
  // decision's status line would have been thrown away with it.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "Alpha.md now holds your version, and your decision was recorded. " +
        "Nothing is waiting for a decision."
    )
  );

  expect(harness.confirmations[0]).toContain(
    "Alpha.md: the other replica's version stops being in the vault."
  );
  expect(syncPanelStatus().classList.contains("is-success")).toBe(true);
  expect(server.lastBody(RESOLVE)).toEqual({ preview_id: "preview-1" });
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW, RESOLVE, CONFLICTS]);
  expect(harness.changes()).toBe(1);
});

it("resolves once when Apply is pressed twice, so a landed change is never reported as none", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  server.state.holds = [];
  syncAction("apply").click();
  // The receipt is one-use, so a second request carrying it would be refused by a
  // server that had just applied the first — and this page would report that nothing
  // was applied over a change that was.
  expect(syncAction("apply").disabled).toBe(true);
  syncAction("apply").click();

  await vi.waitFor(() =>
    expect(syncPanelStatus().classList.contains("is-success")).toBe(true)
  );
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW, RESOLVE, CONFLICTS]);
  expect(harness.changes()).toBe(1);
});

it("drops the plan when the decision changes, so a receipt can never outlive it", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  chooseSyncPath("Alpha.md", "accept-incoming");

  expect(syncOperationStatus().textContent).toBe(
    "The decision changed, so it has to be reviewed again."
  );
  expect(syncPlanBox().hidden).toBe(true);
  expect(syncPlanBox().textContent).toBe("");
  expect(syncAction("apply").hidden).toBe(true);
  expect(syncAction("review").hidden).toBe(false);

  // The server would have caught a plan built against a vault that moved on;
  // nothing but this catches one built against choices that did.
  syncAction("apply").click();
  await Promise.resolve();
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW]);
});

it("cancels a reviewed plan without writing anything", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  syncAction("cancel").click();

  expect(syncPlanBox().hidden).toBe(true);
  expect(syncAction("apply").hidden).toBe(true);
  expect(syncAction("review").hidden).toBe(false);
  expect(syncOperationStatus().textContent).toBe("");
  // The receipt is left to expire: a resolve sent only to discard it is a write.
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW]);
});

it("names the path nobody decided instead of asking the server about it", async () => {
  const server = createSyncServer();
  server.state.holds = [
    syncOperation({ paths: [syncContestedPath(), syncContestedPath({ path: "Beta.md" })] }),
  ];
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();

  await vi.waitFor(() =>
    expect(syncOperationStatus().textContent).toContain("Beta.md has not been decided.")
  );
  expect(syncOperationStatus().textContent).toContain(
    "Every contested file has to be decided first."
  );
  expect(server.urls()).toEqual([CONFLICTS]);
});

it("applies nothing when the destructive confirmation is declined", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();
  harness.answerConfirm(false);

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  syncAction("apply").click();

  await vi.waitFor(() => expect(syncOperationStatus().textContent).toBe("Nothing was applied."));
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW]);
  // The plan that was read is still the plan: declining is not a change of mind
  // about the decision, so the Apply button stays where it was.
  expect(syncAction("apply").hidden).toBe(false);
  expect(harness.changes()).toBe(0);
});

it("asks for no confirmation when the decision discards nothing", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  server.state.holds = [];
  await harness.panel.load();
  server.state.holds = [syncOperation()];
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-both");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  server.state.holds = [];
  syncAction("apply").click();

  await vi.waitFor(() => expect(server.urls()).toContain(RESOLVE));
  expect(harness.confirmations).toEqual([]);
});

it("reports a refusal that is not staleness as itself, and still re-reads the status", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      { ok: false, code: "safety", message: "the content history is blocked, so nothing was written" },
      500
    );
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "keep-current");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));
  syncAction("apply").click();

  // The refusal is the announcement, and it lands on the panel's status ahead of
  // what the re-read found — with the engine's sentence finished, so it does not
  // run into the count behind it.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "the content history is blocked, so nothing was written. " +
        "1 change is waiting for a decision."
    )
  );
  expect(syncPanelStatus().classList.contains("is-error")).toBe(true);
  // Both the channel and the status panel are re-read, for the same reason: a
  // refusal that got as far as the engine may still have applied the package, and
  // neither this page nor the panel is the judge of that.
  expect(server.urls()).toEqual([CONFLICTS, PREVIEW, RESOLVE, CONFLICTS]);
  expect(harness.changes()).toBe(1);
  // The receipt is one-use whatever the answer was, and the re-read rebuilt the
  // card from the channel besides, so there is no plan left to redeem.
  expect(syncAction("apply").hidden).toBe(true);
});

it("says a resolution that could not be acknowledged is still waiting for the peer", async () => {
  const server = createSyncServer();
  server.state.resolve = () =>
    syncJSON(
      syncResolvedPayload({
        // The server's own summary, verbatim: syncResolvedSummary writes the
        // acknowledgment into it, so this is the sentence a real unacknowledged
        // resolution arrives with.
        summary:
          "The change was applied and your decision was recorded. " +
          "The other replica has not been told yet and will keep offering it until it is.",
        acknowledged: false,
        abandoned: [{ package_id: "pkg-7", reason: "a later local edit crossed it" }],
      })
    );
  const harness = mountSyncConflicts(server);
  await harness.panel.load();

  chooseSyncPath("Alpha.md", "accept-incoming");
  syncAction("review").click();
  await vi.waitFor(() => expect(syncAction("apply").hidden).toBe(false));

  server.state.holds = [];
  syncAction("apply").click();
  // One announcement, in the order it matters: what this vault now holds, what the
  // peer has not been told, and what an earlier attempt lost — then the count the
  // re-read found, which says nothing is waiting because a package the peer has
  // not acknowledged is no longer waiting for a *decision*.
  await vi.waitFor(() =>
    expect(syncPanelStatus().textContent).toBe(
      "The change was applied and your decision was recorded. " +
        "The other replica has not been told yet and will keep offering it until it is. " +
        "An earlier unfinished resolution of pkg-7 was dropped: a later local edit crossed it. " +
        "Nothing is waiting for a decision."
    )
  );
  // Said once. The summary is the server's and already carries the acknowledgment,
  // so a page that added its own sentence about it made the same claim twice.
  expect(syncPanelStatus().textContent?.match(/has not been told/g)).toHaveLength(1);
  // The panel re-reads rather than adjusting a count it was holding, which is what
  // makes the status follow the vault's durable facts instead of this page's hopes.
  expect(harness.changes()).toBe(1);
});

it("counts what is waiting, and says so when nothing is", async () => {
  const server = createSyncServer();
  server.state.holds = [syncOperation(), syncOperation({ package_id: "pkg-9" })];
  const harness = mountSyncConflicts(server);

  await harness.panel.load();
  expect(syncPanelStatus().textContent).toBe("2 changes are waiting for a decision.");
  expect(syncPanelStatus().classList.contains("is-warning")).toBe(true);
  expect(document.querySelectorAll("[data-sync-operation]")).toHaveLength(2);

  server.state.holds = [];
  await harness.panel.load();
  expect(syncPanelStatus().textContent).toBe("Nothing is waiting for a decision.");
  expect(syncPanelStatus().classList.contains("is-empty")).toBe(true);
  expect(document.querySelectorAll("[data-sync-operation]")).toHaveLength(0);
});

it("reports a channel it could not read and renders no stale holds", async () => {
  const server = createSyncServer();
  const harness = mountSyncConflicts(server);
  await harness.panel.load();
  expect(document.querySelectorAll("[data-sync-operation]")).toHaveLength(1);

  server.state.conflicts = () =>
    syncJSON({ ok: false, code: "forbidden", message: "this server is a read-only replica" }, 403);
  await harness.panel.load();

  expect(syncPanelStatus().textContent).toBe("this server is a read-only replica");
  expect(syncPanelStatus().classList.contains("is-error")).toBe(true);
  expect(document.querySelectorAll("[data-sync-operation]")).toHaveLength(0);
});
