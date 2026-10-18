// Shared readers and drivers for sync.spec.js: the manifest lookups, the dialog
// witness, and the git readings a resolution is checked against.

import { join } from "node:path";

import { expect, gitRead, gitReadQuiet, gitStatus, manifest } from "./fixture.js";

export const SYNC = manifest.sync;

// Where a resolution's acknowledgment lands. `outgoing/receipts/` is this
// device's side — internal/vaultsync/layout.go calls it "acknowledgments this
// device owes the peer" — and a receipt there means durable application, never a
// completed transfer, which is exactly the distinction the status panel reports.
export const RECEIPTS = join(manifest.localVault, ".bnotes", "sync", "outgoing", "receipts");

export function held(label) {
  const conflict = SYNC.conflicts[label];
  if (!conflict) {
    throw new Error(`the fixture seeded no ${label} conflict: ${Object.keys(SYNC.conflicts)}`);
  }
  return conflict;
}

export function vaultFile(rel) {
  return join(manifest.localVault, rel);
}

// openSync loads the page and waits for both panels to have fetched their own
// state. The server renders no status and no conflict server-side, so every
// assertion below is against what the shipped bundle put there.
export async function openSync(page) {
  await page.goto("/sync");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-sync-facts]")).not.toBeEmpty();
  await expect(page.locator("[data-sync-conflicts-status]")).not.toHaveClass(/is-loading/);
}

export function operationFor(page, label) {
  return page.locator(`[data-sync-operation="${held(label).packageId}"]`);
}

export function contestedFor(page, label) {
  return operationFor(page, label).locator(`[data-sync-path="${held(label).path}"]`);
}

const confirmations = new WeakMap();
export function watchDialogs(page) {
  const asked = [];
  confirmations.set(page, asked);
  return asked;
}

const acceptDecision = async (page) => {
  const dialog = page.getByRole("dialog");
  if (!await dialog.isVisible()) return;
  confirmations.get(page)?.push(await dialog.locator(".history-confirm-description").innerText());
  await dialog.getByRole("button", { name: "Apply and discard these versions" }).click();
};

// planDecision takes the choice and reads the plan, which is the step Apply is
// not offered without.
export async function planDecision(page, label, choice) {
  const operation = operationFor(page, label);
  const radio = contestedFor(page, label).locator(`[data-sync-choice="${choice}"]`);
  await expect(radio).toBeEnabled();
  await radio.check();
  await operation.locator("[data-sync-review]").click();
  await expect(operation.locator("[data-sync-plan]")).toBeVisible();
  await expect(operation.locator("[data-sync-operation-status]")).toHaveText(
    "Read what this will do, then apply it.",
  );
  await expect(operation.locator("[data-sync-apply]")).toBeVisible();
}

// The two sentences a finished resolution can say, and which one it is depends on
// whether there was a second commit to make: taking the other replica's version
// wholesale revises nothing on top of the application, so there is no undo entry to
// record and claiming one would send somebody to the history to find it.
export const RESOLVED = {
  revised: "The change was applied and your decision was recorded.",
  accepted:
    "The change was applied, and it needed nothing revised on top of it: what it " +
    "carries is what you chose.",
};

// applyDecision presses Apply and answers with what the server said it did.
//
// The server's own answer is read from the response rather than from the status
// sentence because a successful resolution re-reads the whole channel and throws
// the element that sentence was written into away — and the re-read is itself the
// assertion that follows: the operation is gone from the page because the engine
// no longer holds it, not because this page crossed it off.
//
// `revised` is stated by the caller rather than derived from the answer. Reading
// `journal_id` and then checking the sentence against it would let either answer
// pass, and which decisions revise is a fact about the choice a person made.
export async function applyDecision(page, label, { revised }) {
  const operation = operationFor(page, label);
  const answered = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/sync/conflicts/resolve") &&
      response.request().method() === "POST",
  );
  await operation.locator("[data-sync-apply]").click();
  await acceptDecision(page);
  const resolved = await (await answered).json();
  expect(resolved.ok, `the resolution was refused: ${resolved.message ?? ""}`).toBe(true);
  expect(resolved.package_id).toBe(held(label).packageId);
  // A receipt is the only evidence this device can have that the change is
  // durably applied, and the peer keeps offering the package until one exists.
  expect(resolved.acknowledged, "the peer was never acknowledged").toBe(true);
  expect(Boolean(resolved.journal_id), `journal_id = ${resolved.journal_id ?? ""}`).toBe(revised);
  expect(resolved.summary).toBe(revised ? RESOLVED.revised : RESOLVED.accepted);
  await expect(operation).toHaveCount(0);
  return resolved;
}

// commitSubjects is what the vault's last commits are called, newest first.
//
// `brainnotes: <token> [<entry>]` is internal/vaultgit's own message, so the
// tokens below are the engine's names for the two halves of a resolution rather
// than this file's labels for them.
export function commitSubjects(vault, depth) {
  return gitRead(vault, "log", `-${depth}`, "--format=%s").trim().split("\n");
}

// contentAt reads a path out of a commit, or answers null when that commit does not
// hold it at all — which is the reading that matters for a rejected deletion, and
// is why git's own diagnosis of the missing path is not passed through.
export function contentAt(vault, ref, rel) {
  try {
    return gitReadQuiet(vault, "show", `${ref}:${rel}`);
  } catch {
    return null;
  }
}

// revisedApplicationOf checks that a resolution made its two commits and returns
// what the vault held between them, which is the load-bearing reading.
//
// A decision that keeps this vault's version leaves the file on disk exactly as it
// was, so every on-disk assertion about it is also satisfied by a resolution that
// wrote nothing at all. The intermediate commit is the only place the difference is
// visible: the other replica's change really landed and was then revised, which is
// also what puts the rejected version in the history where an undo can reach it.
export function revisedApplicationOf(vault, journalID, rel) {
  const [revision, application] = commitSubjects(vault, 2);
  expect(revision, "the last commit is not this resolution's recorded decision").toBe(
    `brainnotes: sync-resolve [${journalID}]`,
  );
  expect(application, "the decision does not sit on top of an applied import").toContain(
    "brainnotes: sync-import [",
  );
  return contentAt(vault, "HEAD~1", rel);
}

// settled is the pair of questions every resolution has afterwards: did the
// snapshot happen here, and did the other vault move. A resolution is two commits
// in this vault and none at all in the peer's.
export function settled(what) {
  expect(gitStatus(manifest.localVault), `${what} was applied but never snapshotted`).toBe("");
  expect(gitStatus(manifest.peerVault), `${what} reached the peer vault`).toBe("");
}
