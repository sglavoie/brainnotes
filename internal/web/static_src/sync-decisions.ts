import type {
  SyncChoice,
  SyncConflictOperation,
  SyncContestedPath,
  SyncPathDecision,
  SyncVersion,
} from "./api";

// What a person has decided about one held package, and what is still missing.
//
// Nothing here talks to the server or to the DOM. It exists because the rules it
// holds are the difference between a review page and a pile of radio buttons:
//
// * Every contested path must be decided. The server refuses a partial decision
//   and so does this, before the request — the message names the path nobody
//   decided, which a 400 from the engine could not.
// * A manual merge is only a decision once its text exists. `null` is "no merge
//   supplied"; the empty string is a merge that empties the note, which is a real
//   thing to decide and must stay distinguishable from the first.
// * A choice that discards a version says so, in the words of which version stops
//   being in the vault. It never promises the discarded one is recoverable —
//   whether it is depends on whether this vault had recorded it, which this layer
//   cannot know.

export type SyncDecisionState = {
  choice: string;
  // merged is the composed text for a manual merge, and null for every other
  // choice and for a merge nobody has opened yet.
  merged: string | null;
};

export type SyncDecisions = {
  choose(path: string, choice: string): void;
  merge(path: string, text: string): void;
  stateOf(path: string): SyncDecisionState;
  chosen(path: string): string;
  /** decisions is the request body's paths, in the order the server listed them. */
  decisions(): SyncPathDecision[];
  /** missing names the paths this decision set cannot be submitted without. */
  missing(): string[];
  /** discards is one sentence per path whose decision drops a version. */
  discards(): string[];
};

export const manualMerge = "manual-merge";

const choiceIn = (choices: SyncChoice[], choice: string): SyncChoice | null =>
  choices.find((candidate) => candidate.choice === choice) ?? null;

/** offered reports whether the engine will accept this choice for this path. */
export const offered = (path: SyncContestedPath, choice: string): boolean =>
  choiceIn(path.available, choice) !== null;

/**
 * mergeRefusal is why the merge editor is not on offer, or the empty string.
 *
 * The engine's answer and this page's are two different facts and both matter: a
 * conflict the engine would not merge at all is refused with its own reason, and
 * one it would merge whose versions are an attachment or over the response bound
 * is refused here — a merge editor seeded with nothing would ask somebody to
 * retype a PNG.
 */
export const mergeRefusal = (path: SyncContestedPath): string => {
  const refused = choiceIn(path.refused, manualMerge);
  if (refused) {
    return refused.reason;
  }
  if (!offered(path, manualMerge)) {
    return "This conflict cannot be merged by hand.";
  }
  if (!path.mergeable) {
    return "Both versions have to be text shown on this page before they can be merged here.";
  }
  return "";
};

/**
 * mergeSeed is the text a fresh merge editor opens with: this vault's version when
 * there is one to open, and the other replica's when there is not.
 *
 * The second case is one conflict — this vault deleted the note and the other
 * replica changed it — and it is the only kind offering a merge where a version is
 * missing rather than unshowable, since a merge is refused outright unless both
 * sides are either shown or absent. An empty editor there would be asking somebody
 * to retype the note they are deciding about; the peer's text is the only text the
 * merge can start from, and both versions are on the page above it either way.
 */
export const mergeSeed = (path: SyncContestedPath): string =>
  path.local.shown ? path.local.text : path.remote.text;

// absentNote says why there is nothing at this path on this side, and it is one
// sentence per branch rather than one sentence.
//
// "The change is a deletion" is true of a delete-edit and false of the other kind
// whose contested path exists on neither side: a divergent rename holds nothing
// locally because *this* vault filed the note somewhere else, and nothing remotely
// because the package moved it — so a single sentence has to be wrong on one of
// them, and the one it was wrong about is the one where a person is deciding
// between two arrangements and is told they are deciding about a deletion.
const absentNote: Record<string, string> = {
  local: "Not here: this vault does not hold this file.",
  remote: "Not there: the other replica's change removes this file from this path.",
  baseline: "There was nothing at this path to begin with.",
};

/** versionNote says why a version is not shown, or the empty string when it is. */
export const versionNote = (
  version: SyncVersion,
  branch: "local" | "remote" | "baseline"
): string => {
  if (!version.exists) {
    return absentNote[branch];
  }
  if (version.shown) {
    return "";
  }
  if (version.binary) {
    return `Not text (${version.size} bytes), so it is not shown here.`;
  }
  if (version.too_large) {
    return `Too large to show here (${version.size} bytes).`;
  }
  return "This version could not be read.";
};

const discardSentence = (path: string, choice: SyncChoice): string => {
  if (choice.discards === "local") {
    return `${path}: this vault's version stops being in the vault.`;
  }
  if (choice.discards === "remote") {
    return `${path}: the other replica's version stops being in the vault.`;
  }
  return "";
};

/**
 * confirmationText is what a person is asked before a destructive resolution.
 *
 * It names every version that stops being in the vault and stops there. It does
 * not say the discarded version can be recovered, because whether it can depends
 * on whether this vault had recorded it — and a dialog that promised recovery
 * would be the one sentence on this page nobody could rely on.
 */
export const confirmationText = (discards: string[]): string =>
  [
    "Resolving this discards a version:",
    "",
    ...discards.map((sentence) => `- ${sentence}`),
    "",
    "The resolution itself is recorded in this vault's history. A version that was " +
      "never recorded here is not in any history.",
    "",
    "Continue?",
  ].join("\n");

export const createSyncDecisions = (operation: SyncConflictOperation): SyncDecisions => {
  const state = new Map<string, SyncDecisionState>();
  const pathsByName = new Map(operation.paths.map((path) => [path.path, path]));

  const stateOf = (path: string): SyncDecisionState =>
    state.get(path) ?? { choice: "", merged: null };

  return {
    choose(path: string, choice: string): void {
      state.set(path, { ...stateOf(path), choice });
    },

    merge(path: string, text: string): void {
      state.set(path, { ...stateOf(path), merged: text });
    },

    stateOf,
    chosen: (path: string): string => stateOf(path).choice,

    decisions(): SyncPathDecision[] {
      return operation.paths.map((path) => {
        const decided = stateOf(path.path);
        return {
          path: path.path,
          choice: decided.choice,
          merged: decided.choice === manualMerge ? decided.merged : null,
        };
      });
    },

    missing(): string[] {
      return operation.paths.flatMap((path) => {
        const decided = stateOf(path.path);
        if (decided.choice === "") {
          return [`${path.path} has not been decided.`];
        }
        if (decided.choice === manualMerge && decided.merged === null) {
          return [`${path.path} is set to a manual merge with no merged text.`];
        }
        return [];
      });
    },

    discards(): string[] {
      return operation.paths.flatMap((path) => {
        const contested = pathsByName.get(path.path);
        const chosen = contested ? choiceIn(contested.available, stateOf(path.path).choice) : null;
        const sentence = chosen ? discardSentence(path.path, chosen) : "";
        return sentence === "" ? [] : [sentence];
      });
    },
  };
};
