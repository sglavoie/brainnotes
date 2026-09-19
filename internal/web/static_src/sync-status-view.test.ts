import { expect, it } from "vitest";
import {
  formatSyncTimestamp,
  syncFacts,
  syncNeedsPerson,
  syncProblems,
  type SyncFact,
} from "./sync-status-view";
import { syncStatus, syncWorker } from "./sync.test-helpers";

// What the status sentences claim, which is the half of this page that can be
// wrong in a way nobody notices. A panel that renders four facts is obviously
// working; a panel that reports a copied package as an applied one looks exactly
// the same and is the reason this vault exists.

const factFor = (facts: SyncFact[], term: string): SyncFact => {
  const found = facts.find((fact) => fact.term === term);
  if (!found) {
    throw new Error(`no fact named ${term} in ${facts.map((fact) => fact.term).join(", ")}`);
  }
  return found;
};

it("reports what the other replica acknowledged and never what was sent", () => {
  const facts = syncFacts(
    syncStatus({
      transport: {
        published: 4,
        acknowledged: 3,
        awaiting_receipt: 1,
        oldest_awaiting: "2026-02-03T08:00:00Z",
      },
    })
  );

  const transport = factFor(facts, "Acknowledged by the other replica");
  expect(transport.detail).toContain("3 of 4 acknowledged");
  expect(transport.detail).toContain("1 change is published and not acknowledged");
  expect(transport.detail).toContain("The oldest has been waiting since");
  expect(transport.kind).toBe("waiting");
  // The comfortable lie, spelled out so a future edit that reaches for it fails.
  expect(facts.map((fact) => fact.detail).join(" ")).not.toContain("4 sent");
});

it("says every published change is acknowledged only when every one of them is", () => {
  const facts = syncFacts(
    syncStatus({
      transport: { published: 2, acknowledged: 2, awaiting_receipt: 0, incomplete: 0 },
    })
  );

  const transport = factFor(facts, "Acknowledged by the other replica");
  expect(transport.detail).toBe("Every published change is acknowledged (2 of 2).");
  expect(transport.kind).toBe("");
});

it("does not call a channel fully acknowledged over a publication that never finished", () => {
  const facts = syncFacts(
    syncStatus({
      // Neither acknowledged nor awaiting: a package of this device's own whose
      // manifest never landed is counted apart from both, so the two totals do not
      // add up to what was published.
      transport: { published: 4, acknowledged: 3, awaiting_receipt: 0, incomplete: 1 },
    })
  );

  const transport = factFor(facts, "Acknowledged by the other replica");
  expect(transport.detail).toBe(
    "3 of 4 acknowledged. 1 publication was interrupted before it finished, " +
      "so it will never be acknowledged."
  );
  // The lie this case exists for: a sentence claiming everything is acknowledged
  // beside a count saying three of four are.
  expect(transport.detail).not.toContain("Every published change is acknowledged");
  expect(transport.kind).toBe("waiting");
});

it("explains the interrupted publications behind a count that is also awaiting receipts", () => {
  const facts = syncFacts(
    syncStatus({
      transport: { published: 5, acknowledged: 2, awaiting_receipt: 1, incomplete: 2 },
    })
  );

  const transport = factFor(facts, "Acknowledged by the other replica");
  expect(transport.detail).toBe(
    "2 of 5 acknowledged. 1 change is published and not acknowledged. " +
      "2 publications were interrupted before they finished, so they will never be acknowledged."
  );
});

it("counts what arrived and is not applied as waiting rather than as done", () => {
  const facts = syncFacts(
    syncStatus({
      peer: { delivered: 5, awaiting_application: 5, applied: 0, last_delivery_at: "2026-02-03T09:30:00Z" },
    })
  );

  const peer = factFor(facts, "Arrived from the other replica");
  expect(peer.detail).toBe("5 changes have arrived and are not applied yet.");
  expect(peer.kind).toBe("waiting");
});

it("separates a change that is still arriving from one waiting to be applied", () => {
  const facts = syncFacts(
    syncStatus({ peer: { delivered: 3, awaiting_application: 3, incomplete: 1 } })
  );

  const peer = factFor(facts, "Arrived from the other replica");
  expect(peer.detail).toBe(
    "3 changes have arrived and are not applied yet. " +
      "1 of those cannot be read yet, so it is still arriving or arrived damaged."
  );
  expect(peer.kind).toBe("waiting");
});

it("reports a conflict as a decision waiting for a person, ahead of the count of arrivals", () => {
  const facts = syncFacts(
    syncStatus({ peer: { delivered: 3, awaiting_application: 3, conflicts: 1 } })
  );

  const peer = factFor(facts, "Arrived from the other replica");
  expect(peer.detail).toBe("1 change is waiting for a decision below.");
  expect(peer.kind).toBe("problem");
});

it("names the delivery time on a channel that is fully applied", () => {
  const facts = syncFacts(
    syncStatus({ peer: { delivered: 2, applied: 2, last_delivery_at: "2026-02-03T09:30:00Z" } })
  );

  const peer = factFor(facts, "Arrived from the other replica");
  expect(peer.detail).toContain("Everything that arrived has been applied. Last delivery ");
  expect(peer.kind).toBe("");
});

it("answers a replica about its own copy and about nothing else", () => {
  const stale = syncFacts(
    syncStatus({
      mode: "read-only",
      enrolled: true,
      peer: { delivered: 9, conflicts: 2 },
      replica: { recorded: true, stale: true, last_successful_sync: "2026-02-03T07:00:00Z" },
    })
  );

  expect(stale.map((fact) => fact.term)).toEqual(["This copy"]);
  expect(stale[0].detail).toContain("over 15 minutes ago");
  expect(stale[0].detail).toContain("The machine making the copy is where that is fixed.");
  expect(stale[0].kind).toBe("problem");
});

it("tells a replica with no recorded copy from one whose copy is merely old", () => {
  const facts = syncFacts(
    syncStatus({
      mode: "read-only",
      replica: { recorded: false, stale: true, last_successful_sync: "" },
    })
  );

  expect(facts[0].detail).toBe("No verified copy has been recorded, so its age is unknown.");
  expect(facts[0].kind).toBe("problem");
});

it("offers an unenrolled vault the two facts that are true of it", () => {
  const facts = syncFacts(syncStatus({ enrolled: false }));

  expect(facts.map((fact) => fact.term)).toEqual(["Other replica", "Saved here"]);
  expect(facts[0].detail).toContain("not enrolled with another replica");
});

it("names an unmanaged or blocked content history where the saving is reported", () => {
  const unmanaged = factFor(syncFacts(syncStatus({ save: { managed: false } })), "Saved here");
  expect(unmanaged.detail).toBe(
    "This vault has no content history, so nothing here is recorded or exported."
  );
  expect(unmanaged.kind).toBe("waiting");

  const blocked = factFor(
    syncFacts(syncStatus({ save: { blocked: true, block_reason: "an interrupted snapshot" } })),
    "Saved here"
  );
  expect(blocked.detail).toBe("Recording is blocked: an interrupted snapshot.");
  expect(blocked.kind).toBe("problem");
});

it("reports the first package and the unpackaged ones as two different waits", () => {
  const baseline = factFor(
    syncFacts(syncStatus({ export: { baseline_pending: true, pending_commits: 3 } })),
    "Packaged for the other replica"
  );
  expect(baseline.detail).toContain("The first package has not been built yet");

  const pending = factFor(
    syncFacts(syncStatus({ export: { pending_commits: 1 } })),
    "Packaged for the other replica"
  );
  expect(pending.detail).toBe("1 recorded change not packaged yet.");
  expect(pending.kind).toBe("waiting");
});

it("carries a failing worker's own message so a person can read why", () => {
  const problems = syncProblems(
    syncStatus({
      workers: [
        syncWorker({
          name: "export",
          failures: 3,
          last_error: "vaultexport: <vault>/.bnotes/sync/export.json could not be written",
          next_attempt_after: "2026-02-03T10:10:00Z",
        }),
        syncWorker({ name: "import" }),
      ],
    })
  );

  expect(problems).toHaveLength(1);
  expect(problems[0]).toContain("export: 3 failures in a row.");
  expect(problems[0]).toContain("vaultexport: <vault>/.bnotes/sync/export.json could not be written");
  expect(problems[0]).toContain("Next attempt after ");
});

it("lists refused acknowledgments, reported problems and the recovery steps as one list", () => {
  const problems = syncProblems(
    syncStatus({
      problems: [
        { subject: "export", detail: "the cursor is from an earlier enrollment epoch" },
        { subject: "", detail: "the companion is not running" },
      ],
      transport: { rejected: [{ package_id: "pkg-9", detail: "the payload digest did not match" }] },
      save: {
        blocked: true,
        block_reason: "an interrupted snapshot",
        block_recovery: ["run bn doctor --git", "then bn sync"],
      },
    })
  );

  expect(problems).toEqual([
    "export: the cursor is from an earlier enrollment epoch",
    "the companion is not running",
    "Acknowledgment for pkg-9 was refused: the payload digest did not match",
    "run bn doctor --git",
    "then bn sync",
  ]);
});

it("keeps the recovery steps out of a vault whose saving is not blocked", () => {
  expect(syncProblems(syncStatus({ save: { block_recovery: ["run bn doctor --git"] } }))).toEqual([]);
});

it("takes needs_person from the server rather than deriving a second opinion", () => {
  // A vault holding a conflict whose status says nobody is needed is a
  // disagreement to report, not one to quietly correct: the command line and the
  // browser must answer this question the same way.
  expect(syncNeedsPerson(syncStatus({ needs_person: false, peer: { conflicts: 2 } }))).toBe(false);
  expect(syncNeedsPerson(syncStatus({ needs_person: true }))).toBe(true);
});

it("shows a timestamp it cannot parse rather than dropping it", () => {
  expect(formatSyncTimestamp("")).toBe("");
  expect(formatSyncTimestamp("whenever")).toBe("whenever");
  expect(formatSyncTimestamp("2026-02-03T10:05:00Z")).not.toBe("");
});
