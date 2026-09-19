import { queryElement } from "./dom";
import { setStatusText, type StatusKind } from "./status";
import { fetchSyncStatus, type FetchLike } from "./sync-requests";
import {
  formatSyncTimestamp,
  syncFacts,
  syncNeedsPerson,
  syncProblems,
  type SyncFact,
} from "./sync-status-view";
import type { SyncStatusResult } from "./api";

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
};

type SyncStatusElements = {
  summary: HTMLElement;
  facts: HTMLElement;
  problems: HTMLElement;
  refresh: HTMLButtonElement | null;
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

// summaryOf appends when the status was observed, because everything else on the
// panel is a fact about a moment and a person re-reading the page needs to know
// which moment they are looking at.
const summaryOf = (status: SyncStatusResult): [string, StatusKind] => {
  const at = formatSyncTimestamp(status.observed_at);
  const observed = at === "" ? "" : ` Read ${at}.`;
  return [`${status.summary}${observed}`, syncNeedsPerson(status) ? "is-warning" : "is-success"];
};

export const createSyncStatusPanel = (deps: SyncStatusPanelDeps): SyncStatusPanel | null => {
  const elements = readElements(deps.root);
  if (!elements) {
    return null;
  }
  const doc = deps.root.ownerDocument;
  const endpoint = deps.root.getAttribute("data-sync-status-endpoint") ?? "";

  const load = async (): Promise<void> => {
    setStatusText(elements.summary, "Reading synchronization status...", "is-loading");
    const outcome = await fetchSyncStatus(deps.fetch, endpoint);
    if (!outcome.ok) {
      setStatusText(elements.summary, outcome.message, "is-error");
      renderFacts(elements.facts, [], doc);
      renderProblems(elements.problems, [], doc);
      return;
    }
    setStatusText(elements.summary, ...summaryOf(outcome.result));
    renderFacts(elements.facts, syncFacts(outcome.result), doc);
    renderProblems(elements.problems, syncProblems(outcome.result), doc);
  };

  elements.refresh?.addEventListener("click", () => void load());
  return { load };
};
