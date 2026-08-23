import { relatedModeDefinitions } from "./related-model";
import { setStatusText, type StatusKind } from "./status";
import {
  formatSynthesisBytes,
  formatSynthesisExpiry,
  synthesisDestinationPath,
  synthesisPurposeLabel,
  type SynthesisConfig,
  type SynthesisModel,
} from "./synthesize-model";
import { element } from "./synthesize-view-elements";
import type { SynthesisView } from "./synthesize-view";
import type { RelatedMode } from "./related-types";
import type { SynthesisCandidate } from "./synthesize-sources";

export const selectSynthesisSuggestionMode = (
  view: SynthesisView,
  activeMode: RelatedMode
): void => {
  for (const definition of relatedModeDefinitions) {
    const selected = definition.mode === activeMode;
    view.suggestionTabs[definition.mode].setAttribute(
      "aria-selected",
      selected ? "true" : "false"
    );
    view.suggestionTabs[definition.mode].tabIndex = selected ? 0 : -1;
    view.suggestionPanels[definition.mode].hidden = !selected;
  }
};

const sourceRow = (
  doc: Document,
  position: number,
  total: number,
  source: { key: string; title: string; path: string; route: string },
  anchor: boolean
): HTMLLIElement => {
  const row = element(doc, "li", "synthesize-source", {
    "data-synthesize-source": source.key,
    "data-synthesize-position": String(position),
  });
  if (anchor) {
    row.classList.add("is-anchor");
    row.setAttribute("data-synthesize-anchor", "");
  }
  const badge = element(doc, "span", "synthesize-source-position");
  badge.textContent = String(position);
  const body = element(doc, "div", "synthesize-source-body");
  const link = element(doc, "a", "synthesize-source-title");
  link.href = source.route;
  link.textContent = source.title;
  const path = element(doc, "code", "synthesize-source-path");
  path.textContent = source.path;
  body.append(link, path);
  row.append(badge, body);

  if (anchor) {
    const note = element(doc, "span", "synthesize-source-role");
    note.textContent = "Anchor";
    row.appendChild(note);
    return row;
  }

  const controls = element(doc, "div", "synthesize-source-controls");
  const handle = element(doc, "button", "synthesize-source-handle", {
    "data-synthesize-handle": source.key,
    "aria-label": `Reorder ${source.title}, position ${position} of ${total}. Press Up or Down to move it.`,
  });
  handle.type = "button";
  handle.textContent = "Drag";
  const remove = element(doc, "button", "synthesize-source-remove", {
    "data-synthesize-remove": source.key,
    "aria-label": `Remove ${source.title}`,
  });
  remove.type = "button";
  remove.textContent = "Remove";
  controls.append(handle, remove);
  row.appendChild(controls);
  return row;
};

export const renderSynthesisSources = (
  view: SynthesisView,
  model: SynthesisModel,
  config: SynthesisConfig
): void => {
  const doc = view.sourceList.ownerDocument;
  const total = model.sources.length + 1;
  const rows: HTMLLIElement[] = [sourceRow(doc, 1, total, config.anchor, true)];
  model.sources.forEach((source, index) => {
    rows.push(sourceRow(doc, index + 2, total, source, false));
  });
  view.sourceList.replaceChildren(...rows);
  const remaining = config.maxSupporters - model.sources.length;
  view.selectionStatus.textContent =
    remaining > 0
      ? `${total} of ${config.maxSupporters + 1} notes selected. ${remaining} more can be added.`
      : `${total} of ${config.maxSupporters + 1} notes selected. Remove one to add another.`;
};

export const renderSynthesisCandidates = (
  container: HTMLElement,
  candidates: SynthesisCandidate[],
  selectedKeys: string[],
  anchorKey: string,
  detailIsHTML: boolean,
  emptyMessage: string
): void => {
  const doc = container.ownerDocument;
  const selected = new Set(selectedKeys);
  const rows = candidates
    .filter((candidate) => candidate.key !== anchorKey && candidate.key !== "")
    .map((candidate) => {
      const card = element(doc, "article", "synthesize-candidate", {
        "data-synthesize-candidate": candidate.key,
      });
      const heading = element(doc, "h4", "synthesize-candidate-title");
      const link = element(doc, "a", "");
      link.href = candidate.route;
      link.textContent = candidate.title;
      heading.appendChild(link);
      const path = element(doc, "p", "synthesize-candidate-path");
      const pathCode = doc.createElement("code");
      pathCode.textContent = candidate.path;
      path.appendChild(pathCode);
      card.append(heading, path);
      if (candidate.detail !== "") {
        const detail = element(doc, "p", "synthesize-candidate-detail");
        if (detailIsHTML) {
          detail.innerHTML = candidate.detail;
        } else {
          detail.textContent = candidate.detail;
        }
        card.appendChild(detail);
      }
      const add = element(doc, "button", "synthesize-candidate-add", {
        "data-synthesize-add": candidate.key,
      });
      add.type = "button";
      if (selected.has(candidate.key)) {
        add.disabled = true;
        add.textContent = "Selected";
      } else {
        add.textContent = "Add source";
        add.setAttribute("aria-label", `Add ${candidate.title} as a source`);
      }
      card.appendChild(add);
      return card;
    });
  if (rows.length === 0) {
    const empty = element(doc, "p", "synthesize-candidates-empty");
    empty.textContent = emptyMessage;
    container.replaceChildren(empty);
    return;
  }
  container.replaceChildren(...rows);
};

export type SynthesisWarningLink = { href: string; label: string };

export const setSynthesisWarning = (
  view: SynthesisView,
  message: string,
  link?: SynthesisWarningLink
): void => {
  view.warning.textContent = message;
  if (message !== "" && link) {
    const anchor = element(view.warning.ownerDocument, "a", "synthesize-warning-link", {
      "data-synthesize-warning-link": "",
      href: link.href,
    });
    anchor.textContent = link.label;
    view.warning.append(" ", anchor);
  }
  view.warning.hidden = message === "";
};

export const setSynthesisStatus = (
  target: HTMLElement,
  message: string,
  kind: StatusKind = ""
): void => {
  setStatusText(target, message, kind);
};

const excerptLabel = (path: string, start: number, end: number): string =>
  start === end ? `${path}:${start}` : `${path}:${start}-${end}`;

export const renderSynthesisContext = (view: SynthesisView, model: SynthesisModel): void => {
  const doc = view.manifest.ownerDocument;
  view.prepareButton.textContent =
    model.context === null && !model.contextStale ? "Prepare context" : "Rebuild context";
  if (model.context === null) {
    view.copyButton.disabled = true;
    view.contextSummary.textContent = "";
    view.manifest.replaceChildren();
    view.packet.textContent = "";
    return;
  }
  const context = model.context;
  view.copyButton.disabled = false;
  view.contextSummary.textContent = `${context.manifest.notes.length} notes, ${formatSynthesisBytes(context.manifest.byte_count)} of a ${formatSynthesisBytes(context.manifest.max_bytes)} budget, purpose ${synthesisPurposeLabel(context.purpose)}.`;

  const notesHeading = element(doc, "h4", "synthesize-manifest-heading");
  notesHeading.textContent = "Selected notes";
  const notes = element(doc, "ol", "synthesize-manifest-notes");
  for (const note of context.manifest.notes) {
    const item = doc.createElement("li");
    const link = doc.createElement("a");
    link.href = note.route;
    link.textContent = note.title || note.path;
    const path = doc.createElement("code");
    path.textContent = note.path;
    item.append(link, " ", path);
    notes.appendChild(item);
  }
  const excerptsHeading = element(doc, "h4", "synthesize-manifest-heading");
  excerptsHeading.textContent = "Excerpt locations";
  const excerpts = element(doc, "ul", "synthesize-manifest-excerpts");
  if (context.manifest.excerpts.length === 0) {
    const item = doc.createElement("li");
    item.textContent = "None.";
    excerpts.appendChild(item);
  } else {
    for (const excerpt of context.manifest.excerpts) {
      const item = doc.createElement("li");
      const code = doc.createElement("code");
      code.textContent = excerptLabel(excerpt.path, excerpt.start_line, excerpt.end_line);
      item.appendChild(code);
      excerpts.appendChild(item);
    }
  }
  view.manifest.replaceChildren(notesHeading, notes, excerptsHeading, excerpts);
  view.packet.textContent = context.markdown;
};

export const setSynthesisFieldError = (target: HTMLElement, message: string): void => {
  target.textContent = message;
  target.hidden = message === "";
};

// A restored candidate carries no rendered HTML, because storing server markup
// and writing it back into the page is not worth the convenience. The Markdown
// is shown as text instead, and the actions still work.
export const renderSynthesisCandidate = (view: SynthesisView, model: SynthesisModel): void => {
  const candidate = model.candidate;
  if (candidate === null) {
    view.candidatePanel.hidden = true;
    view.candidateRendered.replaceChildren();
    view.candidateMarkdown.textContent = "";
    return;
  }
  view.candidatePanel.hidden = false;
  view.candidateMarkdown.textContent = candidate.markdown;
  if (candidate.html !== "") {
    view.candidateRendered.innerHTML = candidate.html;
    return;
  }
  const doc = view.candidateRendered.ownerDocument;
  const note = element(doc, "p", "synthesize-candidate-plain");
  note.textContent = "Restored candidate — shown as Markdown.";
  view.candidateRendered.replaceChildren(note);
};

export const renderSynthesisDestination = (view: SynthesisView, model: SynthesisModel): void => {
  const destination = synthesisDestinationPath(model);
  view.destination.textContent =
    destination === ""
      ? "Enter a title to choose where the note is created."
      : `Creates ${destination}`;
};

export const renderSynthesisPreview = (view: SynthesisView, model: SynthesisModel): void => {
  const preview = model.preview;
  view.applyButton.disabled = preview === null;
  if (preview === null) {
    view.previewPanel.hidden = true;
    view.previewDestination.textContent = "";
    view.previewExpiry.textContent = "";
    view.previewRendered.replaceChildren();
    view.previewMarkdown.textContent = "";
    view.previewSources.replaceChildren();
    return;
  }
  const doc = view.previewSources.ownerDocument;
  view.previewPanel.hidden = false;
  view.previewDestination.textContent = `Creates ${preview.destination}`;
  view.previewExpiry.textContent = `This preview can be applied until ${formatSynthesisExpiry(preview.expires_at)}.`;
  view.previewRendered.innerHTML = preview.rendered_html;
  view.previewMarkdown.textContent = preview.markdown;

  const notes = model.context ? model.context.manifest.notes : [];
  const rows = notes.map((note) => {
    const item = doc.createElement("li");
    const link = doc.createElement("a");
    link.href = note.route;
    link.textContent = note.title || note.path;
    const path = doc.createElement("code");
    path.textContent = note.path;
    item.append(link, " ", path);
    return item;
  });
  if (rows.length === 0) {
    const item = doc.createElement("li");
    item.textContent = "Listed in the note Markdown above.";
    view.previewSources.replaceChildren(item);
    return;
  }
  view.previewSources.replaceChildren(...rows);
};
