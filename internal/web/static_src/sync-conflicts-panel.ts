import { confirmAction, type ConfirmAction } from "./action-confirm";
import { queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";
import { resolveDraftStorage, resolveDraftTabID, type DraftStorage } from "./source-draft";
import { randomID } from "./source-save";
import {
  confirmationText,
  createSyncDecisions,
  manualMerge,
  mergeSeed,
  type SyncDecisions,
} from "./sync-decisions";
import {
  renderSyncOperations,
  renderSyncPlan,
  type SyncOperationElements,
  type SyncPathElements,
} from "./sync-conflict-render";
import { createSyncMergeEditor, type SyncMergeEditor } from "./sync-merge-editor";
import {
  applySyncResolution,
  fetchSyncConflicts,
  previewSyncResolution,
  stalePreview,
  type FetchLike,
} from "./sync-requests";
import type { SourceSurfaceFactory } from "./source-surface";
import { createPlanState, type PlanState } from "./sync-conflicts-plan-state";
import {
  emptyMessage,
  failureAnnouncement,
  join,
  mergeSafetyOf,
  resolvedSentence,
  staleAnnouncement,
  type MergeSafety,
  type SyncAnnouncement,
} from "./sync-conflicts-announce";
import type { SyncConflictOperation } from "./api";

// The conflict review controller: read the holds, take a decision, plan it,
// confirm what it discards, apply it, and re-read.
//
// Four rules here are the session's own, and each of them is a sentence somebody
// would otherwise have to guess at.
//
// **A plan is never applied after the decision changed.** Touching a radio or
// typing in a merge drops the receipt this page is holding, so Apply can only ever
// redeem a plan built from what is on screen. The server would have caught a
// resolution planned against a *vault* that moved on; nothing but this catches one
// planned against choices that did.
//
// **A stale rejection re-reads and keeps the merge.** `preview_stale` means a late
// local edit, a competing resolution, or a hold that is gone — the versions have to
// be read again, and the composed merge is the one text on the page that exists
// nowhere else, so the reload seeds every merge editor from the copy kept in this
// browser rather than starting it over. `preview_required` takes the same remedy for
// a different reason (a receipt spent or past its five minutes) and says so in its
// own words: see reReadOpening.
//
// **A discarding decision is confirmed by naming what it discards**, from the
// engine's own per-choice answer rather than from a guess about which choices are
// destructive.
//
// **An applied resolution is reported by what durably happened.** A resolution
// that applied and could not acknowledge says so, and the status panel is re-read
// rather than adjusted — the package is still waiting for the peer until a receipt
// exists, and this page says exactly that.
//
// The last of those has a mechanical half worth stating, because it is the one a
// reader cannot see from here: a re-read *replaces* the operations, so every node
// inside one is gone afterwards, including the status line the operation's own
// sentences are written to. Anything about what durably happened is therefore
// announced through the re-read and lands on the panel's status line, which is
// outside that host and survives. A sentence said before a reload is a sentence
// shown for one network round trip.

export type SyncConflictsPanelDeps = {
  root: HTMLElement;
  fetch: FetchLike;
  // surface builds a *fresh* factory per merge, because a formatting keystroke has
  // to reach the surface it was typed in: one factory shared between three merge
  // editors could only ever remember the last of them.
  surface: () => SourceSurfaceFactory;
  storage?: DraftStorage | null;
  draftID?: string;
  confirm?: ConfirmAction;
  // changed is called after anything durable happened, so the status panel re-reads
  // instead of this page deciding what the new status is.
  changed?: () => void;
  view?: Window | null;
};

export type SyncConflictsPanel = {
  load: () => Promise<void>;
};

type PanelElements = {
  status: HTMLElement;
  operations: HTMLElement;
};

type PanelContext = {
  conflictsEndpoint: string;
  previewEndpoint: string;
  resolveEndpoint: string;
  token: string;
  vaultID: string;
  fetch: FetchLike;
  surface: () => SourceSurfaceFactory;
  storage: DraftStorage | null;
  draftID: string;
  confirm: ConfirmAction;
  changed: () => void;
  // merges asks what a re-read can honestly promise about every merge on the page,
  // not just the ones on the card whose request failed. One reload replaces all of
  // the cards, so a sentence scoped to one of them is a sentence about a fraction of
  // what is about to be thrown away. It lives on the context because only the panel
  // can see every controller.
  merges: () => MergeSafety;
  view: Window | null;
  doc: Document;
};

type OperationController = {
  // editors is what this card has mounted, for the panel-wide merge question above.
  editors: () => SyncMergeEditor[];
  destroy: () => void;
};

const readElements = (root: HTMLElement): PanelElements | null => {
  const status = queryElement(root, "[data-sync-conflicts-status]", HTMLElement);
  const operations = queryElement(root, "[data-sync-operations]", HTMLElement);
  return status && operations ? { status, operations } : null;
};

// mergeEditorFor mounts the editor for one contested path, or answers null when
// this conflict has no merge on offer. A merge the page could not seed is not
// rendered at all (see renderMerge), so a null here is that same decision.
const mergeEditorFor = (
  ctx: PanelContext,
  operation: SyncConflictOperation,
  element: SyncPathElements,
  decisions: SyncDecisions,
  invalidate: () => void
): SyncMergeEditor | null => {
  if (!element.mergeHost || !element.mergeNotice) {
    return null;
  }
  return createSyncMergeEditor({
    host: element.mergeHost,
    notice: element.mergeNotice,
    reset: element.mergeReset,
    factory: ctx.surface(),
    vaultID: ctx.vaultID,
    packageID: operation.package_id,
    path: element.path.path,
    seed: mergeSeed(element.path),
    // This vault's digest, whichever version the seed came from: it is the side that
    // can move under a merge being composed, and a load that found it changed is the
    // one a person has to be told about. See baseRevision.
    baseRevision: element.path.local.sha256,
    storage: ctx.storage,
    draftID: ctx.draftID,
    onText: (text: string): void => {
      decisions.merge(element.path.path, text);
      invalidate();
    },
    view: ctx.view,
  });
};

type ResolutionScope = {
  ctx: PanelContext;
  elements: SyncOperationElements;
  decisions: SyncDecisions;
  plan: PlanState;
  editors: Map<string, SyncMergeEditor>;
  reload: (announce?: SyncAnnouncement) => void;
  say: (message: string, kind?: StatusKind) => void;
};

const reviewResolution = async (scope: ResolutionScope): Promise<void> => {
  const { decisions, say } = scope;
  const missing = decisions.missing();
  if (missing.length > 0) {
    say(`Every contested file has to be decided first. ${missing.join(" ")}`, "is-error");
    return;
  }
  say("Working out what this will do…", "is-loading");
  const outcome = await previewSyncResolution(
    scope.ctx.fetch,
    scope.ctx.previewEndpoint,
    scope.ctx.token,
    scope.elements.operation.package_id,
    decisions.decisions()
  );
  if (!outcome.ok) {
    // A preview writes nothing, so a refusal that is not about staleness leaves
    // this operation exactly as it is and is said here, where the merge on screen
    // survives to be corrected. Staleness is the one that has to re-read.
    if (!stalePreview(outcome)) {
      say(outcome.message, "is-error");
      return;
    }
    scope.reload(staleAnnouncement(outcome, scope.ctx.merges()));
    return;
  }
  renderSyncPlan(scope.elements.plan, outcome.result.paths, scope.ctx.doc);
  scope.plan.planned(outcome.result.preview_id);
  say("Read what this will do, then apply it.", "is-warning");
};

const applyResolution = async (scope: ResolutionScope): Promise<void> => {
  const { ctx, decisions, plan, say } = scope;
  const previewID = plan.id();
  if (previewID === "") {
    return;
  }
  const discards = decisions.discards();
  if (discards.length > 0 && !await ctx.confirm(confirmationText(discards), scope.elements.apply)) {
    say("Nothing was applied.");
    return;
  }
  say("Applying the decision…", "is-loading");
  const outcome = await applySyncResolution(ctx.fetch, ctx.resolveEndpoint, ctx.token, previewID);
  // The receipt is one-use whatever the answer was, so it is spent either way.
  plan.spend();
  // Both asked for either way, and the reason is the same one: a refused resolution
  // may still have applied the package — the engine has a partial outcome where the
  // change lands and the decision does not — so neither this page nor the status
  // panel is in a position to decide that nothing happened.
  ctx.changed();
  if (!outcome.ok) {
    // Every failure here reloads, and every reload replaces the editors — so the
    // merge question is asked on both branches rather than only the stale one. The
    // refusal that is not staleness used to reload in silence about a merge this
    // browser could not keep, which is the one loss nothing else on the page reports.
    const merges = ctx.merges();
    scope.reload(
      stalePreview(outcome)
        ? staleAnnouncement(outcome, merges)
        : failureAnnouncement(outcome.message, merges)
    );
    return;
  }
  // Every editor on this card, including one whose path was decided some other way
  // in the end. A resolution covers *all* of the package's contested paths, so the
  // card is gone after this and nothing will ever offer those merges back — and a
  // merge is deliberately ineligible for eviction (see mergeNoteKeyPrefix), so one
  // kept here would hold quota another note's draft needs until it expires.
  for (const editor of scope.editors.values()) {
    editor.applied();
  }
  scope.reload({ message: resolvedSentence(outcome.result), kind: "is-success" });
};

// bindDecisions wires the radios, and mounts a merge editor the first time the
// merge is chosen rather than at render: a surface per contested path on a page
// holding several conflicts is a lot of editor nobody asked for.
const bindDecisions = (scope: ResolutionScope): void => {
  const { ctx, elements, decisions, plan, editors } = scope;
  const openMerge = (element: SyncPathElements, chosen: boolean): void => {
    if (!element.merge) {
      return;
    }
    element.merge.hidden = !chosen;
    if (!chosen || editors.has(element.path.path)) {
      return;
    }
    const editor = mergeEditorFor(ctx, elements.operation, element, decisions, () =>
      plan.dropped("The merge changed, so it has to be reviewed again.")
    );
    if (!editor) {
      return;
    }
    editors.set(element.path.path, editor);
    // The editor's own text is the decision's text from the moment it exists,
    // including an empty document: "" is a merge and null is no merge at all, which
    // is the distinction the request is built on.
    decisions.merge(element.path.path, editor.text());
  };

  for (const element of elements.paths) {
    for (const radio of element.radios) {
      radio.addEventListener("change", () => {
        if (!radio.checked) {
          return;
        }
        decisions.choose(element.path.path, radio.value);
        plan.dropped("The decision changed, so it has to be reviewed again.");
        openMerge(element, radio.value === manualMerge);
      });
    }
  }
};

// createRequestGate serializes this card's requests and puts its buttons out of
// reach while one is in flight.
//
// The one that matters is a second Apply on the same receipt. The receipt is
// one-use, so the resolution the first click *landed* comes back to the second as
// "that decision is not here any more" — a page reporting that nothing was applied
// over a change that was, which is the one wrong answer this surface exists to
// prevent. Disabling the button is the convention (see note-trash.ts), and the flag
// behind it is what actually holds: a click dispatched in code does not consult
// `disabled`, and neither does a second Enter on a focused button in every engine.
const createRequestGate = (elements: SyncOperationElements): ((run: () => Promise<void>) => void) => {
  const buttons = [elements.review, elements.apply, elements.cancel];
  const enable = (usable: boolean): void => {
    for (const button of buttons) {
      if (button) {
        button.disabled = !usable;
      }
    }
  };
  let running = false;
  return (run: () => Promise<void>): void => {
    if (running) {
      return;
    }
    running = true;
    enable(false);
    void run().finally(() => {
      running = false;
      // A reload has replaced these nodes by now, so this re-enables buttons nobody
      // can see. That is the harmless half: the half that matters is a *refusal*,
      // which leaves the card standing and has to leave it usable.
      enable(true);
    });
  };
};

const createOperationController = (
  ctx: PanelContext,
  elements: SyncOperationElements,
  reload: () => void
): OperationController => {
  const say = (message: string, kind: StatusKind = ""): void => {
    setStatusText(elements.status, message, kind);
  };
  const scope: ResolutionScope = {
    ctx,
    elements,
    decisions: createSyncDecisions(elements.operation),
    plan: createPlanState(elements, say),
    editors: new Map<string, SyncMergeEditor>(),
    reload,
    say,
  };
  bindDecisions(scope);

  const gate = createRequestGate(elements);
  elements.review?.addEventListener("click", () => gate(() => reviewResolution(scope)));
  elements.apply?.addEventListener("click", () => gate(() => applyResolution(scope)));
  elements.cancel?.addEventListener("click", () => {
    scope.plan.dropped("");
    say("");
  });

  return {
    editors: (): SyncMergeEditor[] => [...scope.editors.values()],

    destroy(): void {
      for (const editor of scope.editors.values()) {
        editor.destroy();
      }
      scope.editors.clear();
    },
  };
};

export const createSyncConflictsPanel = (
  deps: SyncConflictsPanelDeps
): SyncConflictsPanel | null => {
  const elements = readElements(deps.root);
  if (!elements) {
    return null;
  }
  const attr = (name: string): string => deps.root.getAttribute(name) ?? "";
  // Declared before the context that closes over it: the question is about every card
  // mounted at the moment it is asked, which is not the set that existed when any one
  // of them was built.
  let controllers: OperationController[] = [];
  const ctx: PanelContext = {
    conflictsEndpoint: attr("data-sync-conflicts-endpoint"),
    previewEndpoint: attr("data-sync-preview-endpoint"),
    resolveEndpoint: attr("data-sync-resolve-endpoint"),
    token: attr("data-sync-csrf"),
    vaultID: attr("data-sync-vault"),
    fetch: deps.fetch,
    surface: deps.surface,
    // A merge is kept under the vault's own identity or not at all. Guessing a key
    // is how one vault's draft is offered over another's on the same origin, so a
    // page served without an identity keeps nothing rather than keeping it wrongly.
    storage: mergeStorage(deps),
    // The same per-tab identity the editor uses, from the same sessionStorage slot:
    // one tab is one author, whichever of its pages the text was typed on.
    draftID: deps.draftID ?? resolveDraftTabID(() => randomID("tab"), deps.view ?? undefined),
    confirm: deps.confirm ?? ((message: string, returnFocus?: Element | null) => confirmAction({
      title: "Apply this sync decision?", description: message, confirmLabel: "Apply and discard these versions",
    }, deps.root.ownerDocument, returnFocus)),
    changed: deps.changed ?? ((): void => {}),
    merges: (): MergeSafety =>
      mergeSafetyOf(controllers.flatMap((controller) => controller.editors())),
    view: deps.view ?? null,
    doc: deps.root.ownerDocument,
  };

  const teardown = (): void => {
    for (const controller of controllers) {
      controller.destroy();
    }
    controllers = [];
  };

  // load re-reads the channel, and announce is what the re-read was for. It is said
  // after the operations are rendered and on the panel's own status line, so the
  // sentence outlives the operation it is about — see the header.
  const load = async (announce?: SyncAnnouncement): Promise<void> => {
    teardown();
    setStatusText(elements.status, "Reading the changes waiting for a decision…", "is-loading");
    const outcome = await fetchSyncConflicts(ctx.fetch, ctx.conflictsEndpoint);
    if (!outcome.ok) {
      // The announcement still goes first: what durably happened is not less true
      // because the channel could not be read afterwards.
      setStatusText(elements.status, join(announce?.message, outcome.message), "is-error");
      elements.operations.textContent = "";
      return;
    }
    const rendered = renderSyncOperations(elements.operations, outcome.result.operations, ctx.doc);
    controllers = rendered.map((operation) =>
      createOperationController(ctx, operation, (next?: SyncAnnouncement) => void load(next))
    );
    const waiting = emptyMessage(outcome.result.operations.length);
    setStatusText(
      elements.status,
      join(announce?.message, waiting),
      announce ? announce.kind : outcome.result.operations.length === 0 ? "is-empty" : "is-warning"
    );
  };

  return { load: (): Promise<void> => load() };
};

// mergeStorage answers what a merge may be kept in, and the identity check is made
// here rather than inside the store: one built with an empty vault id would write
// keys a second vault on this origin reads as its own.
const mergeStorage = (deps: SyncConflictsPanelDeps): DraftStorage | null => {
  const vaultID = deps.root.getAttribute("data-sync-vault") ?? "";
  if (vaultID === "") {
    return null;
  }
  return deps.storage === undefined ? resolveDraftStorage(deps.view ?? undefined) : deps.storage;
};
