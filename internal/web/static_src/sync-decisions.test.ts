import { expect, it } from "vitest";
import {
  confirmationText,
  createSyncDecisions,
  manualMerge,
  mergeRefusal,
  mergeSeed,
  offered,
  versionNote,
} from "./sync-decisions";
import {
  acceptIncoming,
  keepBoth,
  keepCurrent,
  syncBinaryVersion,
  syncChoice,
  syncContestedPath,
  syncMissingVersion,
  syncOperation,
  syncTextVersion,
} from "./sync.test-helpers";

// The rules that make this a review surface rather than a pile of radio buttons:
// every contested path is decided, a manual merge is only a decision once its text
// exists, and a choice that discards a version says which version in the words of
// what stops being in the vault.

it("names the path nobody decided, which a refusal from the engine could not", () => {
  const operation = syncOperation({
    paths: [syncContestedPath(), syncContestedPath({ path: "Beta.md" })],
  });
  const decisions = createSyncDecisions(operation);

  decisions.choose("Alpha.md", "keep-current");

  expect(decisions.missing()).toEqual(["Beta.md has not been decided."]);
});

it("holds a manual merge to its text, and counts an empty merge as one", () => {
  const decisions = createSyncDecisions(syncOperation());

  decisions.choose("Alpha.md", manualMerge);
  expect(decisions.missing()).toEqual(["Alpha.md is set to a manual merge with no merged text."]);

  // Emptying the note is a decision somebody can mean. It has to stay
  // distinguishable from never having supplied a merge at all.
  decisions.merge("Alpha.md", "");
  expect(decisions.missing()).toEqual([]);
  expect(decisions.decisions()).toEqual([{ path: "Alpha.md", choice: manualMerge, merged: "" }]);
});

it("submits merged text only for the choice that is a merge, in the server's order", () => {
  const decisions = createSyncDecisions(
    syncOperation({ paths: [syncContestedPath(), syncContestedPath({ path: "Beta.md" })] })
  );

  decisions.merge("Alpha.md", "composed\n");
  decisions.choose("Alpha.md", "keep-current");
  decisions.choose("Beta.md", manualMerge);
  decisions.merge("Beta.md", "also composed\n");

  expect(decisions.decisions()).toEqual([
    { path: "Alpha.md", choice: "keep-current", merged: null },
    { path: "Beta.md", choice: manualMerge, merged: "also composed\n" },
  ]);
  expect(decisions.chosen("Alpha.md")).toBe("keep-current");
  expect(decisions.stateOf("Alpha.md").merged).toBe("composed\n");
});

it("says which version stops being in the vault, from the engine's own answer", () => {
  const decisions = createSyncDecisions(
    syncOperation({ paths: [syncContestedPath(), syncContestedPath({ path: "Beta.md" })] })
  );

  decisions.choose("Alpha.md", "keep-current");
  decisions.choose("Beta.md", "accept-incoming");

  expect(decisions.discards()).toEqual([
    "Alpha.md: the other replica's version stops being in the vault.",
    "Beta.md: this vault's version stops being in the vault.",
  ]);
});

it("reports no discard for a choice that keeps both versions", () => {
  const decisions = createSyncDecisions(syncOperation());

  decisions.choose("Alpha.md", "keep-both");

  expect(decisions.discards()).toEqual([]);
});

it("reports no discard for a choice the engine never offered", () => {
  const decisions = createSyncDecisions(
    syncOperation({ paths: [syncContestedPath({ available: [keepBoth()] })] })
  );

  // The page cannot reach this, and a sentence invented for a choice the engine
  // does not have would be a claim about a decision nobody can take.
  decisions.choose("Alpha.md", "accept-incoming");

  expect(decisions.discards()).toEqual([]);
});

it("asks about a destructive resolution without promising the version back", () => {
  const text = confirmationText([
    "Alpha.md: the other replica's version stops being in the vault.",
  ]);

  expect(text).toContain("Resolving this discards a version:");
  expect(text).toContain("- Alpha.md: the other replica's version stops being in the vault.");
  expect(text).toContain("A version that was never recorded here is not in any history.");
  expect(text).toContain("Continue?");
  expect(text).not.toMatch(/can be recovered|can be restored|you can undo/i);
});

it("gives the engine's refusal for a merge rather than this page's", () => {
  const path = syncContestedPath({
    available: [keepCurrent(), acceptIncoming()],
    refused: [syncChoice("manual-merge", { reason: "a configuration conflict is merged by hand in the file." })],
  });

  expect(mergeRefusal(path)).toBe("a configuration conflict is merged by hand in the file.");
  expect(offered(path, manualMerge)).toBe(false);
});

it("refuses a merge the engine would take but the page could not seed", () => {
  const binary = syncContestedPath({
    local: syncBinaryVersion(),
    remote: syncBinaryVersion(8192),
    mergeable: false,
  });

  // Offered by the engine, and still not on offer here: a merge editor seeded with
  // nothing would ask somebody to retype a PNG.
  expect(offered(binary, manualMerge)).toBe(true);
  expect(mergeRefusal(binary)).toBe(
    "Both versions have to be text shown on this page before they can be merged here."
  );
});

it("says a conflict cannot be merged by hand when nothing says why", () => {
  const path = syncContestedPath({ available: [keepCurrent()], refused: [] });

  expect(mergeRefusal(path)).toBe("This conflict cannot be merged by hand.");
});

it("offers no refusal for a conflict the merge is genuinely available on", () => {
  expect(mergeRefusal(syncContestedPath())).toBe("");
  expect(offered(syncContestedPath(), manualMerge)).toBe(true);
  expect(offered(syncContestedPath(), "no-such-choice")).toBe(false);
});

it("seeds a merge from this vault's version, and from the peer's when there is none", () => {
  expect(mergeSeed(syncContestedPath())).toBe("mine\n");
  // A local deletion against a peer's edit: there is no local text to start from,
  // and an editor opening empty would look like a merge that empties the note.
  expect(
    mergeSeed(
      syncContestedPath({ local: syncMissingVersion(), remote: syncTextVersion("theirs\n") })
    )
  ).toBe("theirs\n");
});

it("says why a version is not shown rather than rendering it as empty", () => {
  expect(versionNote(syncTextVersion("mine\n"), "local")).toBe("");
  expect(versionNote(syncBinaryVersion(4096), "local")).toBe(
    "Not text (4096 bytes), so it is not shown here."
  );
  expect(
    versionNote(syncTextVersion("", { shown: false, too_large: true, size: 2_000_000 }), "remote")
  ).toBe("Too large to show here (2000000 bytes).");
  expect(versionNote(syncTextVersion("", { shown: false, size: 12 }), "baseline")).toBe(
    "This version could not be read."
  );
});

// An absent version is described by the side it is absent from, because the two
// kinds of conflict whose contested path exists on neither side — a delete-edit
// and a divergent rename — differ only in which side did what, and a person
// deciding between two arrangements must not be told they are deciding about a
// deletion that is not there.
it("says which side a missing version is missing from", () => {
  expect(versionNote(syncMissingVersion(), "local")).toBe(
    "Not here: this vault does not hold this file."
  );
  expect(versionNote(syncMissingVersion(), "remote")).toBe(
    "Not there: the other replica's change removes this file from this path."
  );
  expect(versionNote(syncMissingVersion(), "baseline")).toBe(
    "There was nothing at this path to begin with."
  );
});
