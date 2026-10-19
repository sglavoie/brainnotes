import { queryElement } from "./dom";
import { setStatusText } from "./status";
import { renderSyncChecked } from "./sync-checked";
import { fetchSyncStatus, type FetchLike } from "./sync-requests";
import {
  syncFacts,
  syncNeedsPerson,
  syncProblems,
  type SyncFact,
} from "./sync-status-view";

// The status panel on /sync: the same facts the nav line summarizes, itemized.
//
// It re-reads after a resolution rather than adjusting what it shows, and that is
// the whole point of "status follows durable acknowledgments": a resolution that
// applied the peer's change and could not acknowledge it is a vault whose status
// still says the package is waiting, and a panel that decremented a counter
// locally would have quietly claimed otherwise.

export type SyncStatusPanelDeps = {
  root: HTMLElement;
  fetch: FetchLike;
};

export type SyncStatusPanel = {
  load: () => Promise<void>;
  dispose: () => void;
};

type SyncStatusElements = {
  summary: HTMLElement;
  facts: HTMLElement;
  problems: HTMLElement;
  refresh: HTMLButtonElement | null;
  disclosure: HTMLDetailsElement | null;
  checked: HTMLTimeElement | null;
};

const readElements = (root: HTMLElement): SyncStatusElements | null => {
  const summary = queryElement(root, "[data-sync-summary]", HTMLElement);
  const facts = queryElement(root, "[data-sync-facts]", HTMLElement);
  const problems = queryElement(root, "[data-sync-problems]", HTMLElement);
  if (!summary || !facts || !problems) {
    return null;
  }
  return {
    summary,
    facts,
    problems,
    refresh: queryElement(root, "[data-sync-refresh]", HTMLButtonElement),
    disclosure: queryElement(root, "[data-sync-details]", HTMLDetailsElement),
    checked: queryElement(root, "[data-sync-checked]", HTMLTimeElement),
  };
};

const factClass = (fact: SyncFact): string =>
  fact.kind === "" ? "sync-fact" : `sync-fact is-${fact.kind}`;

const renderFacts = (host: HTMLElement, facts: SyncFact[], doc: Document): void => {
  host.textContent = "";
  for (const fact of facts) {
    const term = doc.createElement("dt");
    term.className = factClass(fact);
    term.textContent = fact.term;
    const detail = doc.createElement("dd");
    detail.className = factClass(fact);
    detail.textContent = fact.detail;
    host.append(term, detail);
  }
};

const renderProblems = (host: HTMLElement, problems: string[], doc: Document): void => {
  host.textContent = "";
  host.hidden = problems.length === 0;
  for (const problem of problems) {
    const item = doc.createElement("li");
    item.className = "sync-problem";
    item.textContent = problem;
    host.append(item);
  }
};

export const createSyncStatusPanel = (deps: SyncStatusPanelDeps): SyncStatusPanel | null => {
  const elements = readElements(deps.root);
  if (!elements) {
    return null;
  }
  const doc = deps.root.ownerDocument;
  const endpoint = deps.root.getAttribute("data-sync-status-endpoint") ?? "";
  let generation = 0;
  let observed = "";
  const clock = doc.defaultView;
  const timer = clock?.setInterval(() => renderSyncChecked(elements.checked, observed), 60_000);

  const load = async (): Promise<void> => {
    const request = ++generation;
    observed = "";
    renderSyncChecked(elements.checked, observed);
    setStatusText(elements.summary, "Reading synchronization status…", "is-loading");
    const outcome = await fetchSyncStatus(deps.fetch, endpoint);
    if (request !== generation) return;
    if (!outcome.ok) {
      setStatusText(elements.summary, outcome.message, "is-error");
      renderFacts(elements.facts, [], doc);
      renderProblems(elements.problems, [], doc);
      return;
    }
    const facts = syncFacts(outcome.result);
    const problems = syncProblems(outcome.result);
    setStatusText(elements.summary, outcome.result.summary, syncNeedsPerson(outcome.result) ? "is-warning" : "is-success");
    observed = outcome.result.observed_at;
    renderSyncChecked(elements.checked, observed);
    renderFacts(elements.facts, facts, doc);
    renderProblems(elements.problems, problems, doc);
    // Reveal actionable or pending facts; a healthy refresh leaves the user's
    // chosen disclosure state alone.
    if (elements.disclosure && (syncNeedsPerson(outcome.result) || problems.length || facts.some((fact) => fact.kind !== ""))) {
      elements.disclosure.open = true;
    }
  };

  elements.refresh?.addEventListener("click", () => void load());
  return { load, dispose: () => { generation += 1; clock?.clearInterval(timer); } };
};
