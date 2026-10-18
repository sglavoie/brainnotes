import type { SyncStatusResult } from "./api";

// What the synchronization status says, as sentences and nothing more.
//
// Every function here is pure and every one of them is a claim about
// synchronization rather than about layout, which is why they are tested rather
// than eyeballed. Three claims are load-bearing and are the reason this file is
// separate from the panel that renders it.
//
// **Published is not arrived.** The transport fact names what the other replica
// has acknowledged, and counts what is published and unacknowledged as waiting.
// A copied package is not an applied one, and the only evidence of application
// this device can ever have is a receipt — so "3 of 4 acknowledged" is the truth
// and "4 sent" is the comfortable lie. A published package is a third thing as
// well as those two: one whose manifest cannot be read is counted as neither, so
// "acknowledged" and "awaiting" do not have to add up to "published" and a fact
// that assumed they did would claim everything is acknowledged over a count that
// says otherwise.
//
// **Arrived is not applied either.** A delivered package sitting in the incoming
// channel is somebody else's change that this vault has not made its own, and it
// is reported as waiting until it is applied.
//
// **A failing vault never reads as idle.** A worker with a failure streak is a
// problem sentence carrying the companion's own message, because that message is
// the only place a person can read why an export has been failing all afternoon.

export type SyncFact = {
  term: string;
  detail: string;
  // kind marks a fact that is not merely informational, so the panel can style it
  // without re-deriving which ones those are.
  kind: "" | "waiting" | "problem";
};

export const formatSyncTimestamp = (value: string): string => {
  if (value === "") {
    return "";
  }
  const at = new Date(value);
  if (Number.isNaN(at.getTime())) {
    return value;
  }
  return at.toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" });
};

const count = (n: number, one: string, many: string): string =>
  n === 1 ? `1 ${one}` : `${n} ${many}`;

/** The line the navigation shows on every page. */
export const syncLineSummary = (status: SyncStatusResult): string => status.summary
  .replace(/^(\d+) changes are waiting for a decision\.$/, "$1 changes need review.")
  .replace(/^1 change is waiting for a decision\.$/, "1 change needs review.");

/**
 * Whether the status is worth a person's attention.
 *
 * It is the server's own answer — vaultsyncd.NeedsPerson, which is what
 * `bn sync -check` exits non-zero on — rather than a second opinion derived from
 * the state tokens here. Two surfaces deciding this separately is how a browser
 * ends up quietly calmer than the command line about the same vault.
 */
export const syncNeedsPerson = (status: SyncStatusResult): boolean => status.needs_person;

const saveFact = (status: SyncStatusResult): SyncFact => {
  if (status.save.blocked) {
    return {
      term: "Saved here",
      detail: `Recording is blocked: ${status.save.block_reason || "the content history is blocked"}.`,
      kind: "problem",
    };
  }
  if (!status.save.managed) {
    return {
      term: "Saved here",
      detail: "This vault has no content history, so nothing here is recorded or exported.",
      kind: "waiting",
    };
  }
  const at = formatSyncTimestamp(status.save.last_commit_at || status.save.saved_at);
  return {
    term: "Saved here",
    detail: at === "" ? "Nothing has been recorded yet." : `Last recorded ${at}.`,
    kind: "",
  };
};

const exportFact = (status: SyncStatusResult): SyncFact => {
  const pending = status.export.pending_commits;
  if (status.export.baseline_pending) {
    return {
      term: "Packaged for the other replica",
      detail: "The first package has not been built yet, so nothing has been offered.",
      kind: "waiting",
    };
  }
  if (pending === 0) {
    return {
      term: "Packaged for the other replica",
      detail: "Everything recorded here has been packaged.",
      kind: "",
    };
  }
  return {
    term: "Packaged for the other replica",
    detail: `${count(pending, "recorded change", "recorded changes")} not packaged yet.`,
    kind: "waiting",
  };
};

// interruptedClause names published packages this device cannot read back.
//
// A directory in *this* device's own outgoing channel with no readable manifest is
// a publication interrupted before the manifest landed (see
// vaultsyncd.TransportStatus.Incomplete). It is counted in neither the
// acknowledged nor the awaiting total, so leaving it unsaid is what makes
// "every published change is acknowledged (3 of 4)" possible: a sentence that is
// false and a count that visibly contradicts it, with no explanation on offer.
const interruptedClause = (incomplete: number): string => {
  if (incomplete === 0) {
    return "";
  }
  return (
    " " +
    count(
      incomplete,
      "publication was interrupted before it finished, so it will never be acknowledged",
      "publications were interrupted before they finished, so they will never be acknowledged"
    ) +
    "."
  );
};

// transportFact reports acknowledgment and never delivery. The oldest
// unacknowledged package is named with its age because "one package is waiting"
// and "one package has been waiting since Tuesday" are the same count and
// different situations.
const transportFact = (status: SyncStatusResult): SyncFact => {
  const { published, acknowledged, awaiting_receipt: awaiting, incomplete } = status.transport;
  if (published === 0) {
    return {
      term: "Acknowledged by the other replica",
      detail: "Nothing has been published to the other replica yet.",
      kind: "",
    };
  }
  if (awaiting === 0 && incomplete === 0) {
    return {
      term: "Acknowledged by the other replica",
      detail: `Every published change is acknowledged (${acknowledged} of ${published}).`,
      kind: "",
    };
  }
  if (awaiting === 0) {
    return {
      term: "Acknowledged by the other replica",
      detail: `${acknowledged} of ${published} acknowledged.` + interruptedClause(incomplete),
      kind: "waiting",
    };
  }
  const since = formatSyncTimestamp(status.transport.oldest_awaiting);
  const oldest = since === "" ? "" : ` The oldest has been waiting since ${since}.`;
  return {
    term: "Acknowledged by the other replica",
    detail:
      `${acknowledged} of ${published} acknowledged. ` +
      `${count(awaiting, "change is published and not acknowledged", "changes are published and not acknowledged")}.` +
      oldest +
      interruptedClause(incomplete),
    kind: "waiting",
  };
};

// arrivingClause separates a change that is here and undecided from one that is
// still coming. Both are counted as awaiting application — the incomplete ones are
// a subset of that total (see vaultsyncd.PeerStatus.Incomplete) — and only the
// first is anything a person can act on: a package the transport has not finished
// copying is nobody's decision yet, and reading "not applied yet" as "stuck" is
// what sends somebody looking for the change that has not landed.
const arrivingClause = (incomplete: number): string => {
  if (incomplete === 0) {
    return "";
  }
  return (
    " " +
    count(
      incomplete,
      "of those cannot be read yet, so it is still arriving or arrived damaged",
      "of those cannot be read yet, so they are still arriving or arrived damaged"
    ) +
    "."
  );
};

const peerFact = (status: SyncStatusResult): SyncFact => {
  const { delivered, awaiting_application: waiting, incomplete, conflicts } = status.peer;
  if (conflicts > 0) {
    return {
      term: "Arrived from the other replica",
      detail: `${count(conflicts, "change is waiting for a decision", "changes are waiting for a decision")} below.`,
      kind: "problem",
    };
  }
  if (waiting > 0) {
    return {
      term: "Arrived from the other replica",
      detail:
        `${count(waiting, "change has arrived and is not applied yet", "changes have arrived and are not applied yet")}.` +
        arrivingClause(incomplete),
      kind: "waiting",
    };
  }
  const at = formatSyncTimestamp(status.peer.last_delivery_at);
  if (delivered === 0 && at === "") {
    return {
      term: "Arrived from the other replica",
      detail: "Nothing is waiting to be applied.",
      kind: "",
    };
  }
  return {
    term: "Arrived from the other replica",
    detail:
      at === ""
        ? "Everything that arrived has been applied."
        : `Everything that arrived has been applied. Last delivery ${at}.`,
    kind: "",
  };
};

const replicaFact = (status: SyncStatusResult): SyncFact[] => {
  const replica = status.replica;
  if (!replica) {
    return [];
  }
  const at = formatSyncTimestamp(replica.last_successful_sync);
  if (!replica.recorded) {
    return [
      {
        term: "This copy",
        detail: "No verified copy has been recorded, so its age is unknown.",
        kind: "problem",
      },
    ];
  }
  return [
    {
      term: "This copy",
      detail: replica.stale
        ? `Last verified copy ${at}, which is over 15 minutes ago. The machine making the copy is where that is fixed.`
        : `Last verified copy ${at}.`,
      kind: replica.stale ? "problem" : "",
    },
  ];
};

/**
 * The facts panel: four for a vault that synchronizes, one for a published copy.
 *
 * A read-only replica gets the replica fact alone, because the other four would be
 * answered from state files describing the machine that made the copy — see
 * replicaSyncStatus in internal/web/sync_status.go — and a page that showed them
 * would be reporting somebody else's vault as this one.
 */
export const syncFacts = (status: SyncStatusResult): SyncFact[] => {
  if (status.replica) {
    return replicaFact(status);
  }
  if (!status.enrolled) {
    return [
      {
        term: "Other replica",
        detail: "This vault is not enrolled with another replica, so nothing is exported or delivered.",
        kind: "",
      },
      saveFact(status),
    ];
  }
  return [saveFact(status), exportFact(status), transportFact(status), peerFact(status)];
};

const workerProblem = (worker: SyncStatusResult["workers"][number]): string[] => {
  if (worker.failures === 0) {
    return [];
  }
  const next = formatSyncTimestamp(worker.next_attempt_after);
  const retry = next === "" ? "" : ` Next attempt after ${next}.`;
  const because = worker.last_error === "" ? "" : ` ${worker.last_error}`;
  return [
    `${worker.name}: ${count(worker.failures, "failure in a row", "failures in a row")}.${because}${retry}`,
  ];
};

/**
 * Everything standing in the way, as one list.
 *
 * The companion's own failure messages are carried verbatim (the server has
 * already taken the vault's absolute path out of them). A page that summarized
 * them would be the only place a person could look and still not learn why.
 */
export const syncProblems = (status: SyncStatusResult): string[] => {
  const problems = status.problems.map((problem) =>
    problem.subject === "" ? problem.detail : `${problem.subject}: ${problem.detail}`
  );
  const rejected = status.transport.rejected.map(
    (receipt) => `Acknowledgment for ${receipt.package_id} was refused: ${receipt.detail}`
  );
  const workers = status.workers.flatMap(workerProblem);
  const recovery = status.save.blocked ? status.save.block_recovery : [];
  return [...problems, ...rejected, ...workers, ...recovery];
};
