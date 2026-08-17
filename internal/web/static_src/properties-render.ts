import type {
  PropertyPagination,
  PropertyProposal,
  PropertyRow,
  PropertySummary,
  PropertyTotals,
  PropertyValueCount,
} from "./api";
import {
  coverageLabel,
  definitionLabel,
  issueLabel,
  issueSummaryLabel,
  paginationLabel,
  propertyKindLabel,
  valueDisplayText,
} from "./properties-model";

const appendDefinitionPair = (
  doc: Document,
  list: HTMLElement,
  term: string,
  detail: string
): void => {
  const stat = doc.createElement("div");
  stat.className = "properties-stat";
  const dt = doc.createElement("dt");
  dt.textContent = term;
  const dd = doc.createElement("dd");
  dd.textContent = detail;
  stat.appendChild(dt);
  stat.appendChild(dd);
  list.appendChild(stat);
};

const appendBadge = (
  doc: Document,
  parent: HTMLElement,
  text: string,
  className: string
): void => {
  const badge = doc.createElement("span");
  badge.className = className;
  badge.textContent = text;
  parent.appendChild(badge);
};

export const renderTotals = (
  list: HTMLElement | null,
  totals: PropertyTotals,
  totalNotes: number
): void => {
  if (!list) {
    return;
  }
  const doc = list.ownerDocument;
  list.textContent = "";
  appendDefinitionPair(doc, list, "Properties", String(totals.properties));
  appendDefinitionPair(doc, list, "Configured", String(totals.configured));
  appendDefinitionPair(doc, list, "Unconfigured", String(totals.unconfigured));
  appendDefinitionPair(doc, list, "Notes", String(totalNotes));
  appendDefinitionPair(doc, list, "Violations", String(totals.violations));
  appendDefinitionPair(doc, list, "Missing required", String(totals.missing_required));
  appendDefinitionPair(doc, list, "Unsupported values", String(totals.unsupported_values));
};

export const renderCatalog = (list: HTMLElement, summaries: PropertySummary[]): void => {
  const doc = list.ownerDocument;
  list.textContent = "";
  for (const summary of summaries) {
    const item = doc.createElement("li");
    item.className = "properties-item";
    item.setAttribute("data-properties-item", "");
    item.setAttribute("data-properties-name", summary.name);
    if (summary.issues > 0) {
      item.setAttribute("data-properties-issues", String(summary.issues));
    }

    const header = doc.createElement("div");
    header.className = "properties-item-header";

    const link = doc.createElement("a");
    link.className = "properties-item-name";
    link.href = summary.path;
    link.textContent = summary.name;
    header.appendChild(link);

    appendBadge(
      doc,
      header,
      summary.configured ? "Configured" : "Unconfigured",
      summary.configured
        ? "properties-badge properties-badge-configured"
        : "properties-badge properties-badge-unconfigured"
    );
    if (summary.issues > 0) {
      appendBadge(
        doc,
        header,
        `${summary.issues} ${summary.issues === 1 ? "issue" : "issues"}`,
        "properties-badge properties-badge-issues"
      );
    }
    item.appendChild(header);

    const definition = doc.createElement("p");
    definition.className = "properties-item-definition";
    definition.textContent = definitionLabel(summary);
    item.appendChild(definition);

    const meta = doc.createElement("p");
    meta.className = "properties-item-meta";
    meta.textContent = [
      coverageLabel(summary),
      `${summary.distinct_values} distinct`,
      summary.observed_kinds.map((kind) => propertyKindLabel(kind)).join(", ") || "no values",
    ].join(" - ");
    item.appendChild(meta);

    const issues = doc.createElement("p");
    issues.className = "properties-item-issues";
    issues.textContent = issueSummaryLabel(summary);
    item.appendChild(issues);

    list.appendChild(item);
  }
};

export const renderProposals = (
  section: HTMLElement | null,
  list: HTMLElement | null,
  proposals: PropertyProposal[],
  onAdd?: (proposal: PropertyProposal) => void
): void => {
  if (!section || !list) {
    return;
  }
  const doc = list.ownerDocument;
  list.textContent = "";
  section.hidden = proposals.length === 0;
  for (const proposal of proposals) {
    const item = doc.createElement("li");
    item.className = "properties-proposal-item";
    item.setAttribute("data-properties-proposal", proposal.property);

    const title = doc.createElement("h4");
    title.className = "properties-proposal-title";
    title.textContent = proposal.items
      ? `${proposal.property}: list of ${proposal.items}`
      : `${proposal.property}: ${proposal.type}`;
    item.appendChild(title);

    const rationale = doc.createElement("p");
    rationale.className = "properties-proposal-rationale";
    rationale.textContent = proposal.rationale;
    item.appendChild(rationale);

    const toml = doc.createElement("pre");
    toml.className = "properties-proposal-toml";
    toml.textContent = proposal.toml;
    item.appendChild(toml);

    if (onAdd) {
      const actions = doc.createElement("div");
      actions.className = "properties-proposal-actions";
      const add = doc.createElement("button");
      add.type = "button";
      add.className = "properties-button properties-button-primary";
      add.setAttribute("data-properties-proposal-add", proposal.property);
      add.textContent = "Add to brainnotes.toml";
      add.addEventListener("click", () => {
        onAdd(proposal);
      });
      actions.appendChild(add);
      item.appendChild(actions);
    }

    list.appendChild(item);
  }
};

export const renderSummary = (list: HTMLElement | null, summary: PropertySummary): void => {
  if (!list) {
    return;
  }
  const doc = list.ownerDocument;
  list.textContent = "";
  appendDefinitionPair(doc, list, "Coverage", coverageLabel(summary));
  appendDefinitionPair(doc, list, "Distinct values", String(summary.distinct_values));
  appendDefinitionPair(
    doc,
    list,
    "Observed kinds",
    summary.observed_kinds.map((kind) => propertyKindLabel(kind)).join(", ") || "None"
  );
  appendDefinitionPair(doc, list, "Notes with violations", String(summary.violation_notes));
  appendDefinitionPair(doc, list, "Missing required", String(summary.missing_required));
  appendDefinitionPair(doc, list, "Unsupported values", String(summary.unsupported_values));
};

export const renderDefinition = (
  element: HTMLElement | null,
  summary: PropertySummary
): void => {
  if (!element) {
    return;
  }
  element.textContent = definitionLabel(summary);
  element.hidden = false;
};

export const renderProposal = (
  element: HTMLElement | null,
  summary: PropertySummary
): void => {
  if (!element) {
    return;
  }
  const doc = element.ownerDocument;
  element.textContent = "";
  const proposal = summary.proposal;
  element.hidden = !proposal;
  if (!proposal) {
    return;
  }

  const heading = doc.createElement("h3");
  heading.textContent = "Inferred definition";
  element.appendChild(heading);

  const rationale = doc.createElement("p");
  rationale.className = "properties-proposal-rationale";
  rationale.textContent = proposal.rationale;
  element.appendChild(rationale);

  const toml = doc.createElement("pre");
  toml.className = "properties-proposal-toml";
  toml.textContent = proposal.toml;
  element.appendChild(toml);
};

export const renderValues = (
  list: HTMLElement | null,
  values: PropertyValueCount[],
  activeValue: string
): void => {
  if (!list) {
    return;
  }
  const doc = list.ownerDocument;
  list.textContent = "";
  for (const entry of values) {
    const item = doc.createElement("li");
    item.className = "properties-value-item";

    const button = doc.createElement("button");
    button.type = "button";
    button.className = "properties-value-button";
    button.setAttribute("data-property-value", entry.value.text);
    button.setAttribute("data-property-value-kind", entry.value.kind);
    if (entry.value.text === activeValue) {
      button.setAttribute("aria-pressed", "true");
      button.classList.add("is-active");
    } else {
      button.setAttribute("aria-pressed", "false");
    }
    if (!entry.value.supported) {
      button.disabled = true;
      button.classList.add("is-unsupported");
      button.title = "This value has no supported spelling and cannot be filtered on.";
    }

    const text = doc.createElement("span");
    text.className = "properties-value-text";
    text.textContent = entry.value.text;
    button.appendChild(text);

    const kind = doc.createElement("span");
    kind.className = "properties-value-kind";
    kind.textContent = propertyKindLabel(entry.value.kind);
    button.appendChild(kind);

    const count = doc.createElement("span");
    count.className = "properties-value-count";
    count.textContent = `${entry.notes} ${entry.notes === 1 ? "note" : "notes"}`;
    button.appendChild(count);

    item.appendChild(button);
    list.appendChild(item);
  }
};

// RowRenderOptions turns the read-only note list into the editable one. It is
// absent whenever the page has no CSRF token, so a read-only page cannot show
// a control that would be refused.
export type RowRenderOptions = {
  selected: ReadonlySet<string>;
  listKind: boolean;
};

const appendRowControls = (
  doc: Document,
  item: HTMLElement,
  header: HTMLElement,
  row: PropertyRow,
  options: RowRenderOptions
): void => {
  const select = doc.createElement("input");
  select.type = "checkbox";
  select.className = "properties-row-select";
  select.setAttribute("data-property-row-select", row.rel_path);
  select.setAttribute("aria-label", `Select ${row.title || row.rel_path}`);
  select.checked = options.selected.has(row.rel_path);
  header.insertBefore(select, header.firstChild);

  const actions = doc.createElement("div");
  actions.className = "properties-row-actions";

  const editable = !row.unsupported && row.hash !== "";
  const edit = doc.createElement("button");
  edit.type = "button";
  edit.className = "properties-button properties-row-edit";
  edit.setAttribute("data-property-row-edit", row.rel_path);
  edit.textContent = "Edit";
  edit.disabled = !editable || !row.present;
  actions.appendChild(edit);

  if (options.listKind) {
    const add = doc.createElement("button");
    add.type = "button";
    add.className = "properties-button properties-row-add";
    add.setAttribute("data-property-row-add", row.rel_path);
    add.textContent = "Add item";
    add.disabled = !editable;
    actions.appendChild(add);
  }

  const remove = doc.createElement("button");
  remove.type = "button";
  remove.className = "properties-button properties-row-unset";
  remove.setAttribute("data-property-row-unset", row.rel_path);
  remove.textContent = "Remove";
  remove.disabled = !row.present;
  actions.appendChild(remove);

  item.appendChild(actions);
};

export const renderRows = (
  list: HTMLElement | null,
  rows: PropertyRow[],
  options?: RowRenderOptions
): void => {
  if (!list) {
    return;
  }
  const doc = list.ownerDocument;
  list.textContent = "";
  for (const row of rows) {
    const item = doc.createElement("li");
    item.className = "properties-row";
    item.setAttribute("data-property-row", row.rel_path);
    if (row.unsupported) {
      item.setAttribute("data-property-row-unsupported", "");
    }
    if (options) {
      item.tabIndex = -1;
      if (options.selected.has(row.rel_path)) {
        item.classList.add("is-selected");
      }
    }

    const header = doc.createElement("div");
    header.className = "properties-row-header";

    const link = doc.createElement("a");
    link.className = "properties-row-title";
    link.href = row.path;
    link.textContent = row.title || row.rel_path;
    header.appendChild(link);

    if (row.folder !== "") {
      appendBadge(doc, header, row.folder, "properties-row-folder");
    }
    item.appendChild(header);

    const value = doc.createElement("p");
    value.className = "properties-row-value";
    if (!row.present) {
      value.classList.add("is-missing");
      value.textContent = "No value";
    } else if (row.unsupported) {
      value.classList.add("is-unsupported");
      value.textContent = valueDisplayText(row.value);
    } else {
      value.textContent = valueDisplayText(row.value);
    }
    item.appendChild(value);

    if (row.issues.length > 0) {
      const issues = doc.createElement("ul");
      issues.className = "properties-row-issues";
      for (const issue of row.issues) {
        const issueItem = doc.createElement("li");
        issueItem.className = "properties-row-issue";
        issueItem.setAttribute("data-property-issue", issue.kind);
        issueItem.textContent = issueLabel(issue);
        issues.appendChild(issueItem);
      }
      item.appendChild(issues);
    }

    if (options) {
      appendRowControls(doc, item, header, row, options);
    }
    list.appendChild(item);
  }
};

export const renderPagination = (
  element: HTMLElement | null,
  pagination: PropertyPagination
): void => {
  if (!element) {
    return;
  }
  element.textContent = paginationLabel(pagination);
};
