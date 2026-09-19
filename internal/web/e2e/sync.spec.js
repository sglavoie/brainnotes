// The synchronization page, driven in a real browser against a real enrolled
// pair with real held packages in the incoming channel.
//
// What only a browser can answer here is the whole of the session's claim. The
// Vitest suite stops at the request the page issued and the Go tests stop at the
// response the server sent; neither can say that a person who read two versions,
// typed a merge into the shipped CodeMirror, confirmed a dialog and pressed Apply
// has changed the bytes in the vault and the sentences on the page. Five kinds of
// conflict are resolved below — text, attachment, rename, deletion and settings —
// and after each one the vault is read rather than the page believed.
//
// The holds are seeded by scripts/syncfixture (see prepare.mjs): a Go program,
// because a hold is only real once internal/vaultimport has refused to apply a
// real internal/vaultsync package, and the manifest carries the engine's own
// classification of each one rather than this file's guess at it.
//
// **This file is ordered and serial, and that is the subject rather than a
// shortcut.** A resolution is two commits in a vault; it cannot be undone between
// tests, and the five holds are a one-shot resource the suite's single worker
// shares. So the read-only review test runs first, each resolution consumes its
// own hold, and the status test last reads what five durable acknowledgments did
// to the counts. Serial mode is deliberate: a failed resolution invalidates every
// assertion after it, and reporting one broken write as one failure is the point.

import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";

import { expect, gitRead, gitReadQuiet, gitStatus, manifest, test } from "./fixture.js";

const SYNC = manifest.sync;

// Where a resolution's acknowledgment lands. `outgoing/receipts/` is this
// device's side — internal/vaultsync/layout.go calls it "acknowledgments this
// device owes the peer" — and a receipt there means durable application, never a
// completed transfer, which is exactly the distinction the status panel reports.
const RECEIPTS = join(manifest.localVault, ".bnotes", "sync", "outgoing", "receipts");

test.describe.configure({ mode: "serial" });

function held(label) {
  const conflict = SYNC.conflicts[label];
  if (!conflict) {
    throw new Error(`the fixture seeded no ${label} conflict: ${Object.keys(SYNC.conflicts)}`);
  }
  return conflict;
}

function vaultFile(rel) {
  return join(manifest.localVault, rel);
}

// openSync loads the page and waits for both panels to have fetched their own
// state. The server renders no status and no conflict server-side, so every
// assertion below is against what the shipped bundle put there.
async function openSync(page) {
  await page.goto("/sync");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("[data-sync-facts]")).not.toBeEmpty();
  await expect(page.locator("[data-sync-conflicts-status]")).not.toHaveClass(/is-loading/);
}

function operationFor(page, label) {
  return page.locator(`[data-sync-operation="${held(label).packageId}"]`);
}

function contestedFor(page, label) {
  return operationFor(page, label).locator(`[data-sync-path="${held(label).path}"]`);
}

// watchDialogs accepts every confirmation and records what it said.
//
// Recording is half the assertion: a destructive decision must ask, and a
// decision that destroys nothing must not — Playwright dismisses an unhandled
// dialog, so a page that asked when it should not would silently answer "no" and
// report that nothing was applied.
function watchDialogs(page) {
  const asked = [];
  page.on("dialog", (dialog) => {
    asked.push(dialog.message());
    void dialog.accept();
  });
  return asked;
}

// planDecision takes the choice and reads the plan, which is the step Apply is
// not offered without.
async function planDecision(page, label, choice) {
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
const RESOLVED = {
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
async function applyDecision(page, label, { revised }) {
  const operation = operationFor(page, label);
  const answered = page.waitForResponse(
    (response) =>
      response.url().endsWith("/api/sync/conflicts/resolve") &&
      response.request().method() === "POST",
  );
  await operation.locator("[data-sync-apply]").click();
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
function commitSubjects(vault, depth) {
  return gitRead(vault, "log", `-${depth}`, "--format=%s").trim().split("\n");
}

// contentAt reads a path out of a commit, or answers null when that commit does not
// hold it at all — which is the reading that matters for a rejected deletion, and
// is why git's own diagnosis of the missing path is not passed through.
function contentAt(vault, ref, rel) {
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
function revisedApplicationOf(vault, journalID, rel) {
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
function settled(what) {
  expect(gitStatus(manifest.localVault), `${what} was applied but never snapshotted`).toBe("");
  expect(gitStatus(manifest.peerVault), `${what} reached the peer vault`).toBe("");
}

test("the review page presents every held change, both versions, and what waits behind", async ({
  page,
}) => {
  await openSync(page);

  // The status panel says what this vault saved, packaged and had acknowledged,
  // and what arrived — not how old this copy is. A replica's freshness is a fact
  // about the machine that made the copy, and this vault is not one.
  const facts = page.locator("[data-sync-facts]");
  for (const term of [
    "Saved here",
    "Packaged for the other replica",
    "Acknowledged by the other replica",
    "Arrived from the other replica",
  ]) {
    await expect(facts).toContainText(term);
  }
  await expect(facts).not.toContainText("This copy");
  await expect(facts).toContainText("5 changes are waiting for a decision below.");
  await expect(page.locator("[data-sync-conflicts-status]")).toHaveText(
    "5 changes are waiting for a decision.",
  );

  // Every seeded hold is on the page, under the engine's own summary of what kind
  // of disagreement it is.
  await expect(page.locator("[data-sync-operation]")).toHaveCount(5);
  await expect(contestedFor(page, "text")).toContainText("Both replicas changed this note.");
  await expect(contestedFor(page, "deletion")).toContainText(
    "The other replica deleted this note and this one changed it.",
  );
  await expect(contestedFor(page, "settings")).toContainText(
    "Both replicas changed the declarations that govern every note in this vault.",
  );

  // Both versions of the text conflict, in full, because reading them is the
  // whole of what a person resolving one has to do.
  const text = contestedFor(page, "text");
  await expect(text.locator("[data-sync-version-text='local']")).toContainText(
    held("text").local,
  );
  await expect(text.locator("[data-sync-version-text='remote']")).toContainText(
    held("text").remote,
  );

  // The one change waiting behind a conflict. Nothing else on the page would say
  // that deciding this also decides when that arrives.
  const blocked = operationFor(page, "text").locator("[data-sync-blocked]");
  await expect(blocked).toContainText(
    "1 later change is waiting behind this one and will be applied once it is resolved.",
  );
  await expect(blocked).toContainText(SYNC.dependent.packageId);
  for (const label of ["attachment", "rename", "deletion", "settings"]) {
    await expect(operationFor(page, label).locator("[data-sync-blocked]")).toHaveCount(0);
  }
});

test("a version this page cannot show says which kind it is and refuses a merge", async ({
  page,
}) => {
  await openSync(page);

  // The attachment: neither side is text, so neither is rendered, the merge is
  // refused in so many words, and the two choices that remain are worded without
  // claiming anybody has read anything.
  const attachment = contestedFor(page, "attachment");
  for (const branch of ["local", "remote"]) {
    await expect(attachment.locator(`[data-sync-version='${branch}'] .sync-version-note`)).toHaveText(
      /^Not text \(\d+ bytes\), so it is not shown here\.$/,
    );
    await expect(attachment.locator(`[data-sync-version-text='${branch}']`)).toHaveCount(0);
  }
  await expect(attachment.locator("[data-sync-choice='manual-merge']")).toBeDisabled();
  await expect(attachment).toContainText(
    "Both versions have to be text shown on this page before they can be merged here.",
  );
  await expect(attachment.locator("[data-sync-merge]")).toHaveCount(0);
  // Each effect on its own, because the refusal above is the one sentence here
  // that may say "text" — it is about why the editor is not on offer, not about
  // what a decision does.
  const effects = await attachment.locator(".sync-choice-effect").allTextContents();
  expect(effects, "the four decisions are not all described").toHaveLength(4);
  for (const effect of effects) {
    expect(effect.toLowerCase(), `bytes nobody was shown, decided as text: ${effect}`).not.toContain(
      "text",
    );
  }

  // The deletion: this vault's version is there to read and the other replica's
  // is a tombstone, which is said rather than drawn as an empty box.
  const deletion = contestedFor(page, "deletion");
  await expect(deletion.locator("[data-sync-version-text='local']")).toContainText(
    held("deletion").local,
  );
  await expect(deletion.locator("[data-sync-version='remote'] .sync-version-note")).toHaveText(
    "Not there: the other replica's change removes this file from this path.",
  );

  // The divergent rename: the contested path exists on neither side, so each
  // branch says which side it is missing from — "the change is a deletion" would
  // be a sentence about the wrong conflict — and the only readable version is the
  // baseline, which travels inside the package.
  const rename = contestedFor(page, "rename");
  await expect(rename.locator(".sync-path-name")).toHaveText(
    `${held("rename").path} → ${held("rename").renamedTo}`,
  );
  await expect(rename.locator("[data-sync-version='local'] .sync-version-note")).toHaveText(
    "Not here: this vault does not hold this file.",
  );
  await expect(rename.locator("[data-sync-version='remote'] .sync-version-note")).toHaveText(
    "Not there: the other replica's change removes this file from this path.",
  );
  await expect(rename.locator("[data-sync-version-text='baseline']")).toContainText(
    held("rename").baseline,
  );

  // A refused choice is on the page with its reason rather than absent, so
  // nobody hunts for a button that was never there.
  await expect(contestedFor(page, "settings")).toContainText("would govern nothing");
  await expect(deletion).toContainText("rather than editing it");
  for (const label of ["settings", "deletion", "rename"]) {
    await expect(contestedFor(page, label).locator("[data-sync-choice='keep-both']")).toBeDisabled();
  }
});

test("a text conflict is resolved by a merge typed into the editor", async ({ page }) => {
  const asked = watchDialogs(page);
  const file = vaultFile(held("text").path);
  const phrase = `Merged in a browser ${manifest.fixtureRoot.split("/").pop()}.`;
  await openSync(page);

  const path = held("text").path;
  await contestedFor(page, "text").locator("[data-sync-choice='manual-merge']").check();
  // The editor is mounted on demand, and its presence is the mount: the server
  // sends an empty div, so .cm-content exists only because the shipped bundle
  // constructed an EditorView in it.
  const surface = page.locator(`[data-sync-merge-host="${path}"] .cm-content`);
  await expect(surface).toBeVisible();
  // It opens from this vault's version, which is what makes it a merge rather
  // than a rewrite: the other replica's is on the page above it.
  await expect(surface).toContainText(held("text").local);
  await surface.locator(".cm-line").last().click();
  await page.keyboard.press("End");
  await page.keyboard.type(phrase);

  await planDecision(page, "text", "manual-merge");
  await applyDecision(page, "text", { revised: true });
  // A merge discards neither version, so nothing is confirmed. A dialog here
  // would mean the page warns about every decision, which is the same as warning
  // about none.
  expect(asked, "a merge that discards nothing asked for a confirmation").toEqual([]);

  const merged = readFileSync(file, "utf8");
  expect(merged, "the typed merge never reached the vault").toContain(phrase);
  expect(merged, "the merge did not start from this vault's version").toContain(held("text").local);
  expect(merged, "the other replica's version survived a merge that replaced both").not.toContain(
    held("text").remote,
  );
  settled("the merge");
});

test("an attachment conflict takes the other replica's bytes behind a confirmation", async ({
  page,
}) => {
  const asked = watchDialogs(page);
  const file = vaultFile(held("attachment").path);
  const before = readFileSync(file);
  const incoming = readFileSync(SYNC.expected.attachmentRemote);
  expect(before.equals(incoming), "the fixture already holds the incoming bytes").toBe(false);
  await openSync(page);

  await planDecision(page, "attachment", "accept-incoming");
  // Taking the other replica's bytes revises nothing on top of the application, so
  // there is no second commit and the answer must not claim one.
  await applyDecision(page, "attachment", { revised: false });
  expect(asked, "bytes were replaced with no confirmation").toHaveLength(1);
  expect(asked[0]).toContain(`${held("attachment").path}: this vault's version stops being`);

  // Byte for byte against what the package carried, read from a directory
  // outside both vaults: invalid UTF-8 cannot survive a JSON manifest.
  expect(
    readFileSync(file).equals(incoming),
    "the page reported success and the bytes are not the peer's",
  ).toBe(true);
  settled("the attachment decision");
});

test("a divergent rename takes the other replica's name with nothing to discard", async ({
  page,
}) => {
  const asked = watchDialogs(page);
  const source = vaultFile(held("rename").path);
  const destination = vaultFile(held("rename").renamedTo);
  expect(existsSync(source), "the fixture holds the contested source path").toBe(false);
  expect(existsSync(destination), "the fixture already holds the incoming name").toBe(false);
  await openSync(page);

  await planDecision(page, "rename", "accept-incoming");
  await applyDecision(page, "rename", { revised: false });
  // Nothing stops being in the vault: the note is written under the name the
  // other replica chose and whatever this vault did with it stays. The engine
  // says so per choice, and this page asks only when it does.
  expect(asked, "a rename that discards nothing asked for a confirmation").toEqual([]);

  expect(existsSync(destination), "the incoming name was never written").toBe(true);
  expect(readFileSync(destination, "utf8")).toContain(held("rename").baseline);
  settled("the rename decision");
});

test("a deleted note is kept here, and the deletion is rejected, behind a confirmation", async ({
  page,
}) => {
  const asked = watchDialogs(page);
  const file = vaultFile(held("deletion").path);
  const before = readFileSync(file, "utf8");
  await openSync(page);

  await planDecision(page, "deletion", "keep-current");
  const resolved = await applyDecision(page, "deletion", { revised: true });
  // Rejecting an incoming deletion is destructive in the other direction: the
  // other replica's version of this change stops being in the vault, and the
  // note will come back there.
  expect(asked, "an incoming deletion was rejected with no confirmation").toHaveLength(1);
  expect(asked[0]).toContain(`${held("deletion").path}: the other replica's version stops being`);

  expect(existsSync(file), "the note was deleted by the decision that kept it").toBe(true);
  expect(readFileSync(file, "utf8"), "keeping this vault's version changed it").toBe(before);
  // The note on disk is unchanged, which a resolution that did nothing at all would
  // also leave behind. The deletion landing and being reverted is what makes this a
  // rejection rather than a no-op, and it is only readable in the history.
  expect(
    revisedApplicationOf(manifest.localVault, resolved.journal_id, held("deletion").path),
    "the incoming deletion was never applied, so nothing was rejected",
  ).toBeNull();
  settled("the deletion decision");
});

test("a configuration conflict keeps this vault's declarations and cannot keep both", async ({
  page,
}) => {
  const asked = watchDialogs(page);
  const config = vaultFile(held("settings").path);
  // Read now rather than trusted from the seeding: settings.spec.js writes and
  // resets a portable key through the browser before this file runs, so the
  // bytes this decision has to preserve are the ones on disk at this moment.
  const before = readFileSync(config, "utf8");
  expect(before).toContain(held("settings").local);
  expect(before).not.toContain(held("settings").remote);
  await openSync(page);

  // A second copy of the declarations would govern nothing, so the engine
  // refuses the choice and the page carries its reason.
  const settings = contestedFor(page, "settings");
  await expect(settings.locator("[data-sync-choice='keep-both']")).toBeDisabled();
  await expect(settings).toContainText("merge them instead");

  await planDecision(page, "settings", "keep-current");
  const resolved = await applyDecision(page, "settings", { revised: true });
  expect(asked, "the other replica's declarations were rejected with no confirmation").toHaveLength(
    1,
  );

  // Same reading as the deletion, for the same reason: this file ends up byte for
  // byte as it started, so only the intermediate commit can say that the other
  // replica's declarations governed this vault for one commit and were then revoked.
  const governed = revisedApplicationOf(
    manifest.localVault,
    resolved.journal_id,
    held("settings").path,
  );
  expect(governed, "the other replica's declarations were never applied").toContain(
    held("settings").remote,
  );

  const after = readFileSync(config, "utf8");
  expect(after, "this vault's declaration did not survive the decision that kept it").toContain(
    held("settings").local,
  );
  expect(after, "the rejected declaration is governing this vault").not.toContain(
    held("settings").remote,
  );
  expect(after, "keeping this vault's configuration rewrote it").toBe(before);
  settled("the configuration decision");
});

test("the status follows durable acknowledgments once every decision is made", async ({ page }) => {
  // One receipt per resolved package, and a receipt means this vault durably
  // applied the change — not that a file finished copying.
  const receipts = readdirSync(RECEIPTS);
  expect(
    receipts.length,
    `five resolutions should owe the peer five acknowledgments: ${receipts}`,
  ).toBe(5);
  for (const label of ["text", "attachment", "rename", "deletion", "settings"]) {
    expect(
      receipts.some((name) => name.includes(held(label).packageId)),
      `no acknowledgment names the ${label} package`,
    ).toBe(true);
  }

  await openSync(page);
  await expect(page.locator("[data-sync-conflicts-status]")).toHaveText(
    "Nothing is waiting for a decision.",
  );
  await expect(page.locator("[data-sync-operation]")).toHaveCount(0);

  // The dependent package was held behind the text conflict and is held by
  // nothing now, but nothing has applied it either: arrived is not applied, and
  // the panel says which of the two this is. Draining it is the companion's work
  // and not a browser's.
  const facts = page.locator("[data-sync-facts]");
  await expect(facts).toContainText("1 change has arrived and is not applied yet.");
  await expect(facts).not.toContainText("waiting for a decision");
  await expect(page.locator("[data-sync-summary]")).not.toContainText("waiting for a decision");
  expect(existsSync(vaultFile(SYNC.dependent.path)), "the blocked change was applied early").toBe(
    false,
  );

  settled("the last decision");
});
