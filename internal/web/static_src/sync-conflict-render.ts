import { mergeRefusal, offered, versionNote, manualMerge } from "./sync-decisions";
import type {
  SyncBlocked,
  SyncChoice,
  SyncConflictOperation,
  SyncContestedPath,
  SyncVersion,
} from "./api";

// The review page's markup, and nothing that decides anything.
//
// Every contested path renders both versions in full, because the one thing
// somebody resolving a conflict has to do is read them, and a page that showed
// digests and a pair of buttons would be asking for a decision nobody can make.
// A version that cannot be shown — a deletion, an attachment, a note over the
// server's bound — says which of those it is rather than rendering as empty.
//
// A choice the engine refuses is rendered disabled *with its reason* rather than
// dropped. "Keep both is not on offer here because a configuration conflict has
// nowhere to put a second copy" is the sentence that stops somebody hunting for a
// button that was never there.

export type SyncPathElements = {
  path: SyncContestedPath;
  root: HTMLElement;
  radios: HTMLInputElement[];
  merge: HTMLElement | null;
  mergeHost: HTMLElement | null;
  mergeNotice: HTMLElement | null;
  mergeReset: HTMLButtonElement | null;
};

export type SyncOperationElements = {
  operation: SyncConflictOperation;
  root: HTMLElement;
  paths: SyncPathElements[];
  plan: HTMLElement;
  status: HTMLElement;
  review: HTMLButtonElement | null;
  apply: HTMLButtonElement | null;
  cancel: HTMLButtonElement | null;
};

const element = <K extends keyof HTMLElementTagNameMap>(
  doc: Document,
  tag: K,
  className: string,
  text = ""
): HTMLElementTagNameMap[K] => {
  const node = doc.createElement(tag);
  node.className = className;
  if (text !== "") {
    node.textContent = text;
  }
  return node;
};

const versionTitles: Record<string, string> = {
  local: "This vault's version",
  remote: "The other replica's version",
  baseline: "The version both started from",
};

const renderVersion = (
  doc: Document,
  branch: "local" | "remote" | "baseline",
  version: SyncVersion
): HTMLElement => {
  const section = element(doc, "section", "sync-version");
  section.setAttribute("data-sync-version", branch);
  section.append(element(doc, "h5", "sync-version-title", versionTitles[branch]));
  const note = versionNote(version);
  if (note !== "") {
    section.append(element(doc, "p", "sync-version-note", note));
    return section;
  }
  const text = element(doc, "pre", "sync-version-text");
  text.setAttribute("data-sync-version-text", branch);
  text.textContent = version.text;
  section.append(text);
  return section;
};

const renderVersions = (doc: Document, path: SyncContestedPath): HTMLElement => {
  const host = element(doc, "div", "sync-versions");
  host.append(renderVersion(doc, "local", path.local), renderVersion(doc, "remote", path.remote));
  const baseline = doc.createElement("details");
  baseline.className = "sync-baseline";
  const summary = doc.createElement("summary");
  summary.textContent = versionTitles.baseline;
  baseline.append(summary, renderVersion(doc, "baseline", path.baseline));
  host.append(baseline);
  return host;
};

// renderChoice builds one radio. A refused choice is disabled and carries its
// reason; the radio itself is still rendered so the set of decisions a person can
// see is the same set on every conflict, with the unavailable ones visibly out.
const renderChoice = (
  doc: Document,
  name: string,
  choice: SyncChoice,
  refusal: string
): HTMLInputElement => {
  const label = element(doc, "label", refusal === "" ? "sync-choice" : "sync-choice is-refused");
  const input = doc.createElement("input");
  input.type = "radio";
  input.name = name;
  input.value = choice.choice;
  input.disabled = refusal !== "";
  input.setAttribute("data-sync-choice", choice.choice);
  const copy = element(doc, "span", "sync-choice-copy");
  copy.append(element(doc, "span", "sync-choice-label", choice.label));
  if (choice.effect !== "") {
    copy.append(element(doc, "span", "sync-choice-effect", choice.effect));
  }
  if (choice.discards !== "") {
    copy.append(
      element(
        doc,
        "span",
        "sync-choice-discards",
        choice.discards === "local"
          ? "Discards this vault's version."
          : "Discards the other replica's version."
      )
    );
  }
  if (refusal !== "") {
    copy.append(element(doc, "span", "sync-choice-reason", refusal));
  }
  label.append(input, copy);
  return input;
};

const renderChoices = (
  doc: Document,
  packageID: string,
  path: SyncContestedPath
): { host: HTMLElement; radios: HTMLInputElement[] } => {
  const host = doc.createElement("fieldset");
  host.className = "sync-choices";
  host.setAttribute("data-sync-choices", path.path);
  const legend = doc.createElement("legend");
  legend.textContent = "Decision";
  host.append(legend);
  const name = `sync-choice|${packageID}|${path.path}`;
  const radios: HTMLInputElement[] = [];
  const merge = mergeRefusal(path);
  for (const choice of path.available) {
    const refusal = choice.choice === manualMerge ? merge : "";
    const radio = renderChoice(doc, name, choice, refusal);
    radios.push(radio);
    host.append(radio.parentElement as HTMLElement);
  }
  for (const choice of path.refused) {
    host.append(
      renderChoice(doc, name, choice, choice.reason || "Not available for this conflict.")
        .parentElement as HTMLElement
    );
  }
  return { host, radios };
};

// renderMerge builds the merge pane, hidden until the choice is taken. The editor
// itself is mounted into mergeHost by the controller, so this module stays free of
// the editor and of the draft store.
const renderMerge = (
  doc: Document,
  path: SyncContestedPath
): Pick<SyncPathElements, "merge" | "mergeHost" | "mergeNotice" | "mergeReset"> => {
  if (!offered(path, manualMerge) || !path.mergeable) {
    return { merge: null, mergeHost: null, mergeNotice: null, mergeReset: null };
  }
  const merge = element(doc, "div", "sync-merge");
  merge.setAttribute("data-sync-merge", path.path);
  merge.hidden = true;
  merge.append(
    element(
      doc,
      "p",
      "sync-merge-hint",
      "Compose the version this vault should keep. It starts from this vault's version; " +
        "the other replica's is above. Unsent text is kept in this browser."
    )
  );
  const host = element(doc, "div", "sync-merge-host");
  host.setAttribute("data-sync-merge-host", path.path);
  const notice = element(doc, "p", "sync-merge-notice");
  notice.setAttribute("data-sync-merge-notice", path.path);
  notice.hidden = true;
  const reset = element(doc, "button", "sync-button sync-merge-reset", "Start again");
  reset.type = "button";
  reset.setAttribute("data-sync-merge-reset", path.path);
  merge.append(host, notice, reset);
  return { merge, mergeHost: host, mergeNotice: notice, mergeReset: reset };
};

const pathHeading = (path: SyncContestedPath): string => {
  if (path.renamed_to !== "") {
    return `${path.path} → ${path.renamed_to}`;
  }
  if (path.collides !== "") {
    return `${path.path} (collides with ${path.collides})`;
  }
  return path.path;
};

const renderPath = (
  doc: Document,
  packageID: string,
  path: SyncContestedPath
): SyncPathElements => {
  const root = element(doc, "section", "sync-path");
  root.setAttribute("data-sync-path", path.path);
  root.append(element(doc, "h4", "sync-path-name", pathHeading(path)));
  if (path.summary !== "") {
    root.append(element(doc, "p", "sync-path-summary", path.summary));
  }
  if (path.local_changed) {
    root.append(
      element(
        doc,
        "p",
        "sync-path-warning",
        "This vault's version changed after the conflict was recorded. The version shown " +
          "here is the one the decision applies to."
      )
    );
  }
  root.append(renderVersions(doc, path));
  const choices = renderChoices(doc, packageID, path);
  root.append(choices.host);
  const merge = renderMerge(doc, path);
  if (merge.merge) {
    root.append(merge.merge);
  }
  if (path.available.length === 0) {
    root.append(
      element(
        doc,
        "p",
        "sync-path-remedy",
        path.remedy || "No decision can be made here. Resolve it from the command line."
      )
    );
  }
  return { path, root, radios: choices.radios, ...merge };
};

// renderBlocked names what else in the channel is waiting behind this conflict.
// It is not a detail: somebody deciding whether to deal with this now is deciding
// about those changes too, and nothing else on the page would say so.
const renderBlocked = (doc: Document, blocked: SyncBlocked[]): HTMLElement => {
  const host = element(doc, "div", "sync-blocked");
  host.setAttribute("data-sync-blocked", "");
  host.append(
    element(
      doc,
      "p",
      "sync-blocked-summary",
      blocked.length === 1
        ? "1 later change is waiting behind this one and will be applied once it is resolved."
        : `${blocked.length} later changes are waiting behind this one and will be applied once it is resolved.`
    )
  );
  const list = doc.createElement("ul");
  list.className = "sync-blocked-list";
  for (const waiting of blocked) {
    const item = doc.createElement("li");
    item.className = "sync-blocked-item";
    const behind = waiting.waiting_on === "" ? "" : ` behind ${waiting.waiting_on}`;
    const detail = waiting.detail === "" ? "" : ` — ${waiting.detail}`;
    item.textContent = `${waiting.package_id} from ${waiting.origin_device} (#${waiting.sequence}): ${waiting.reason}${behind}${detail}`;
    list.append(item);
  }
  host.append(list);
  return host;
};

const renderActions = (doc: Document, packageID: string): HTMLElement => {
  const host = element(doc, "div", "sync-operation-actions");
  const review = element(doc, "button", "sync-button", "Review this decision");
  review.type = "button";
  review.setAttribute("data-sync-review", packageID);
  const apply = element(doc, "button", "sync-button is-primary", "Apply the decision");
  apply.type = "button";
  apply.hidden = true;
  apply.setAttribute("data-sync-apply", packageID);
  const cancel = element(doc, "button", "sync-button", "Cancel");
  cancel.type = "button";
  cancel.hidden = true;
  cancel.setAttribute("data-sync-cancel", packageID);
  host.append(review, apply, cancel);
  return host;
};

const renderHeader = (doc: Document, operation: SyncConflictOperation): HTMLElement => {
  const header = doc.createElement("header");
  header.className = "sync-operation-header";
  header.append(
    element(
      doc,
      "h3",
      "sync-operation-title",
      `Change from ${operation.origin_device} (#${operation.sequence})`
    )
  );
  const detected = operation.detected_at === "" ? "" : ` Recorded ${operation.detected_at}.`;
  header.append(
    element(
      doc,
      "p",
      "sync-operation-meta",
      `Package ${operation.package_id}. Operation ${operation.operation_id}.${detected}`
    )
  );
  return header;
};

const renderOperation = (doc: Document, operation: SyncConflictOperation): SyncOperationElements => {
  const root = element(doc, "article", "sync-operation");
  root.setAttribute("data-sync-operation", operation.package_id);
  root.append(renderHeader(doc, operation));
  const status = element(doc, "p", "sync-status");
  status.setAttribute("data-sync-operation-status", operation.package_id);
  status.setAttribute("role", "status");
  const plan = element(doc, "div", "sync-plan");
  plan.setAttribute("data-sync-plan", operation.package_id);
  plan.hidden = true;

  if (operation.problem !== "") {
    root.append(
      element(
        doc,
        "p",
        "sync-operation-problem",
        `This change cannot be described here: ${operation.problem}`
      ),
      status
    );
    return { operation, root, paths: [], plan, status, review: null, apply: null, cancel: null };
  }

  if (operation.blocked.length > 0) {
    root.append(renderBlocked(doc, operation.blocked));
  }
  const paths = operation.paths.map((path) => renderPath(doc, operation.package_id, path));
  for (const path of paths) {
    root.append(path.root);
  }
  const actions = renderActions(doc, operation.package_id);
  root.append(actions, plan, status);
  return {
    operation,
    root,
    paths,
    plan,
    status,
    review: actions.querySelector<HTMLButtonElement>("[data-sync-review]"),
    apply: actions.querySelector<HTMLButtonElement>("[data-sync-apply]"),
    cancel: actions.querySelector<HTMLButtonElement>("[data-sync-cancel]"),
  };
};

export const renderSyncOperations = (
  host: HTMLElement,
  operations: SyncConflictOperation[],
  doc: Document
): SyncOperationElements[] => {
  host.textContent = "";
  const rendered = operations.map((operation) => renderOperation(doc, operation));
  for (const operation of rendered) {
    host.append(operation.root);
  }
  return rendered;
};

/** renderSyncPlan lists what the resolution will do, step by step. */
export const renderSyncPlan = (
  host: HTMLElement,
  paths: { path: string; kind: string; choice: string; steps: { path: string; action: string }[] }[],
  doc: Document
): void => {
  host.textContent = "";
  host.hidden = false;
  host.append(element(doc, "h4", "sync-plan-title", "What applying this will do"));
  const list = doc.createElement("ul");
  list.className = "sync-plan-list";
  for (const path of paths) {
    const item = doc.createElement("li");
    item.className = "sync-plan-item";
    // A choice the applied change already satisfies revises nothing, and saying so
    // is better than an empty bullet: "take the other replica's version" is a
    // decision whose whole content is that this vault writes nothing further.
    const steps =
      path.steps.length === 0
        ? "the other replica's change is applied and nothing further is written"
        : path.steps.map((step) => `${step.action} ${step.path}`).join(", ");
    item.textContent = `${path.path} — ${path.choice}: ${steps}`;
    list.append(item);
  }
  host.append(list);
};
