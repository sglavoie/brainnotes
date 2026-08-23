
import { element } from "./synthesize-view-elements";
import type { SynthesisConfig } from "./synthesize-model";

export const buildGenerateSection = (
  doc: Document,
  config: SynthesisConfig
): {
  section: HTMLElement;
  generate: HTMLButtonElement;
  status: HTMLElement;
  panel: HTMLElement;
  rendered: HTMLElement;
  markdown: HTMLElement;
  replace: HTMLButtonElement;
  append: HTMLButtonElement;
  copy: HTMLButtonElement;
  dismiss: HTMLButtonElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-generate", {
    "aria-labelledby": "synthesize-generate-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-generate-heading";
  heading.textContent = "Generated candidate";
  const intro = element(doc, "p", "synthesize-section-intro");
  intro.textContent = config.agentEnabled
    ? "Generation sends the prepared context packet to your configured [agent].command. The candidate is shown here and never replaces your draft on its own."
    : "Generation is unavailable until [agent].command is configured. Everything else on this page works without an agent.";

  const actions = element(doc, "div", "synthesize-generate-actions");
  const generate = element(doc, "button", "synthesize-action", {
    "data-synthesize-generate": "",
  });
  generate.type = "button";
  generate.textContent = "Generate draft";
  generate.disabled = true;
  actions.appendChild(generate);

  const status = element(doc, "p", "synthesize-generate-status", {
    "data-synthesize-generate-status": "",
    role: "status",
    "aria-live": "polite",
  });

  const panel = element(doc, "article", "synthesize-candidate-panel", {
    "data-synthesize-candidate-panel": "",
    "aria-labelledby": "synthesize-generate-heading",
  });
  panel.hidden = true;
  panel.tabIndex = -1;
  const rendered = element(doc, "div", "synthesize-candidate-rendered", {
    "data-synthesize-candidate-rendered": "",
  });
  const markdownWrapper = element(doc, "details", "synthesize-candidate-markdown");
  const markdownSummary = doc.createElement("summary");
  markdownSummary.textContent = "Candidate Markdown";
  const markdown = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-candidate-markdown": "",
    tabindex: "0",
  });
  markdownWrapper.append(markdownSummary, markdown);

  const candidateActions = element(doc, "div", "synthesize-candidate-actions");
  const replace = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-replace": "",
  });
  replace.type = "button";
  replace.textContent = "Replace draft";
  const append = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-append": "",
  });
  append.type = "button";
  append.textContent = "Append to draft";
  const copy = element(doc, "button", "synthesize-action", {
    "data-synthesize-candidate-copy": "",
  });
  copy.type = "button";
  copy.textContent = "Copy candidate";
  const dismiss = element(doc, "button", "synthesize-action is-quiet", {
    "data-synthesize-candidate-dismiss": "",
  });
  dismiss.type = "button";
  dismiss.textContent = "Dismiss candidate";
  candidateActions.append(replace, append, copy, dismiss);
  panel.append(rendered, markdownWrapper, candidateActions);

  section.append(heading, intro, actions, status, panel);
  return { section, generate, status, panel, rendered, markdown, replace, append, copy, dismiss };
};

export const buildCreateSection = (
  doc: Document
): {
  section: HTMLElement;
  destination: HTMLElement;
  preview: HTMLButtonElement;
  apply: HTMLButtonElement;
  status: HTMLElement;
  panel: HTMLElement;
  previewDestination: HTMLElement;
  expiry: HTMLElement;
  rendered: HTMLElement;
  markdown: HTMLElement;
  sources: HTMLOListElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-create", {
    "aria-labelledby": "synthesize-create-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-create-heading";
  heading.textContent = "Create note";
  const destination = element(doc, "p", "synthesize-destination", {
    "data-synthesize-destination": "",
  });

  const actions = element(doc, "div", "synthesize-create-actions");
  const preview = element(doc, "button", "synthesize-action is-primary", {
    "data-synthesize-preview": "",
  });
  preview.type = "button";
  preview.textContent = "Preview note";
  const apply = element(doc, "button", "synthesize-action", { "data-synthesize-apply": "" });
  apply.type = "button";
  apply.textContent = "Create note";
  apply.disabled = true;
  actions.append(preview, apply);

  const status = element(doc, "p", "synthesize-preview-status", {
    "data-synthesize-preview-status": "",
    role: "status",
    "aria-live": "polite",
  });

  const panel = element(doc, "div", "synthesize-preview-panel", {
    "data-synthesize-preview-panel": "",
  });
  panel.hidden = true;
  panel.tabIndex = -1;
  const previewDestination = element(doc, "p", "synthesize-preview-destination", {
    "data-synthesize-preview-destination": "",
  });
  const expiry = element(doc, "p", "synthesize-preview-expiry", {
    "data-synthesize-preview-expiry": "",
  });
  const rendered = element(doc, "div", "synthesize-preview-rendered", {
    "data-synthesize-preview-rendered": "",
  });
  const markdownWrapper = element(doc, "details", "synthesize-preview-markdown");
  const markdownSummary = doc.createElement("summary");
  markdownSummary.textContent = "Note Markdown";
  const markdown = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-preview-markdown": "",
    tabindex: "0",
  });
  markdownWrapper.append(markdownSummary, markdown);
  const sourcesHeading = element(doc, "h4", "synthesize-manifest-heading");
  sourcesHeading.textContent = "Sources";
  const sources = element(doc, "ol", "synthesize-preview-sources", {
    "data-synthesize-preview-sources": "",
  });
  panel.append(previewDestination, expiry, rendered, markdownWrapper, sourcesHeading, sources);

  section.append(heading, destination, actions, status, panel);
  return {
    section,
    destination,
    preview,
    apply,
    status,
    panel,
    previewDestination,
    expiry,
    rendered,
    markdown,
    sources,
  };
};

export const buildContextSection = (
  doc: Document
): {
  section: HTMLElement;
  prepare: HTMLButtonElement;
  copy: HTMLButtonElement;
  discard: HTMLButtonElement;
  status: HTMLElement;
  summary: HTMLElement;
  manifest: HTMLElement;
  packet: HTMLElement;
} => {
  const section = element(doc, "section", "synthesize-section synthesize-context", {
    "aria-labelledby": "synthesize-context-heading",
  });
  const heading = element(doc, "h3", "synthesize-section-heading");
  heading.id = "synthesize-context-heading";
  heading.textContent = "Context packet";
  const actions = element(doc, "div", "synthesize-context-actions");
  const prepare = element(doc, "button", "synthesize-action is-primary", {
    "data-synthesize-prepare": "",
  });
  prepare.type = "button";
  prepare.textContent = "Prepare context";
  const copy = element(doc, "button", "synthesize-action", { "data-synthesize-copy": "" });
  copy.type = "button";
  copy.textContent = "Copy context";
  copy.disabled = true;
  const discard = element(doc, "button", "synthesize-action is-quiet", {
    "data-synthesize-discard": "",
  });
  discard.type = "button";
  discard.textContent = "Discard saved work";
  actions.append(prepare, copy, discard);
  const status = element(doc, "p", "synthesize-context-status", {
    "data-synthesize-context-status": "",
    role: "status",
    "aria-live": "polite",
  });
  const summary = element(doc, "p", "synthesize-context-summary", {
    "data-synthesize-context-summary": "",
  });
  const manifest = element(doc, "div", "synthesize-manifest", {
    "data-synthesize-manifest": "",
  });
  const packetWrapper = element(doc, "details", "synthesize-packet");
  const packetSummary = doc.createElement("summary");
  packetSummary.textContent = "Packet Markdown";
  const packet = element(doc, "pre", "synthesize-packet-body", {
    "data-synthesize-packet": "",
    tabindex: "0",
  });
  packetWrapper.append(packetSummary, packet);
  section.append(heading, actions, status, summary, manifest, packetWrapper);
  return { section, prepare, copy, discard, status, summary, manifest, packet };
};
