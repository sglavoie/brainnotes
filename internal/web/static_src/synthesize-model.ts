import type {
  SynthesisContextResponse,
  SynthesisPreviewResponse,
} from "./api";

export const SYNTHESIS_MAX_SUPPORTERS = 10;

// SYNTHESIS_MAX_DRAFT_BYTES mirrors noteops.MaxSynthesisDraftBytes. The server
// is authoritative; checking here only puts the refusal next to the field.
export const SYNTHESIS_MAX_DRAFT_BYTES = 1 << 20;

export type SynthesisSource = {
  key: string;
  title: string;
  path: string;
  route: string;
};

export type SynthesisConfig = {
  csrfToken: string;
  vaultID: string;
  anchor: SynthesisSource;
  defaultFolder: string;
  defaultPurpose: string;
  purposes: string[];
  maxSupporters: number;
  agentEnabled: boolean;
  contextEndpoint: string;
  generateEndpoint: string;
  previewEndpoint: string;
  applyEndpoint: string;
  relatedEndpoint: string;
  searchEndpoint: string;
};

// SynthesisGeneratedDraft is agent output held beside the draft, never inside
// it. The user chooses to replace or append; nothing the agent produces reaches
// the editable text on its own. html is empty for a restored candidate, because
// the rendered form is not persisted.
export type SynthesisGeneratedDraft = {
  markdown: string;
  html: string;
};

// context holds the packet the server last built for exactly the sources, order,
// purpose, and prompt the model carries now. contextStale records that a context
// existed and was invalidated, which is what tells the user to rebuild rather
// than that they never prepared one. preview holds the receipt that names the
// exact bytes apply would write; previewStale is the same distinction one step
// later.
export type SynthesisModel = {
  sources: SynthesisSource[];
  title: string;
  folder: string;
  purpose: string;
  prompt: string;
  draft: string;
  candidate: SynthesisGeneratedDraft | null;
  context: SynthesisContextResponse | null;
  contextStale: boolean;
  preview: SynthesisPreviewResponse | null;
  previewStale: boolean;
};

export const synthesisHasSavedWork = (model: SynthesisModel, config: SynthesisConfig): boolean =>
  [
    model.sources.length > 0,
    model.title.trim() !== "",
    model.folder !== config.defaultFolder,
    model.purpose !== config.defaultPurpose,
    model.prompt.trim() !== "",
    model.draft.trim() !== "",
    model.candidate !== null,
  ].some(Boolean);

const attribute = (root: HTMLElement, name: string): string =>
  (root.getAttribute(name) || "").trim();

const parseMaxSupporters = (value: string): number => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) {
    return SYNTHESIS_MAX_SUPPORTERS;
  }
  const truncated = Math.trunc(parsed);
  return truncated > 0 ? Math.min(truncated, SYNTHESIS_MAX_SUPPORTERS) : SYNTHESIS_MAX_SUPPORTERS;
};

const firstNonempty = (...values: (string | undefined)[]): string => values.find(Boolean) || "";
const synthesisEndpoint = (root: HTMLElement, name: string, fallback: string): string =>
  firstNonempty(attribute(root, name), fallback);

export const readSynthesisConfig = (root: HTMLElement): SynthesisConfig => {
  const anchorKey = attribute(root, "data-synthesize-anchor-key");
  const anchorPath = attribute(root, "data-synthesize-anchor-path");
  const purposes = attribute(root, "data-synthesize-purposes")
    .split(",")
    .map((purpose) => purpose.trim())
    .filter((purpose) => purpose !== "");
  const defaultPurpose = attribute(root, "data-synthesize-default-purpose");
  return {
    csrfToken: attribute(root, "data-synthesize-csrf"),
    vaultID: attribute(root, "data-synthesize-vault"),
    anchor: {
      key: anchorKey,
      title: firstNonempty(attribute(root, "data-synthesize-anchor-title"), anchorPath, anchorKey),
      path: anchorPath,
      route: attribute(root, "data-synthesize-anchor-route"),
    },
    defaultFolder: attribute(root, "data-synthesize-default-folder"),
    defaultPurpose: firstNonempty(defaultPurpose, purposes[0], "synthesis"),
    purposes: purposes.length > 0 ? purposes : [firstNonempty(defaultPurpose, "synthesis")],
    maxSupporters: parseMaxSupporters(attribute(root, "data-synthesize-max-supporters")),
    agentEnabled: attribute(root, "data-synthesize-agent") === "enabled",
    contextEndpoint: synthesisEndpoint(root, "data-synthesize-context-endpoint", "/api/synthesis/context"),
    generateEndpoint: synthesisEndpoint(root, "data-synthesize-generate-endpoint", "/api/synthesis/generate"),
    previewEndpoint: synthesisEndpoint(root, "data-synthesize-preview-endpoint", "/api/synthesis/preview"),
    applyEndpoint: synthesisEndpoint(root, "data-synthesize-apply-endpoint", "/api/synthesis/apply"),
    relatedEndpoint: synthesisEndpoint(root, "data-synthesize-related-endpoint", "/api/related"),
    searchEndpoint: synthesisEndpoint(root, "data-synthesize-search-endpoint", "/api/search"),
  };
};

export const createSynthesisModel = (config: SynthesisConfig): SynthesisModel => ({
  sources: [],
  title: "",
  folder: config.defaultFolder,
  purpose: config.defaultPurpose,
  prompt: "",
  draft: "",
  candidate: null,
  context: null,
  contextStale: false,
  preview: null,
  previewStale: false,
});

// A receipt names one exact set of bytes, so anything that would change those
// bytes invalidates it: the draft, the destination, and the packet the sources
// section is built from.
export const invalidateSynthesisPreview = (model: SynthesisModel): void => {
  if (model.preview === null) {
    return;
  }
  model.preview = null;
  model.previewStale = true;
};

// A prepared packet describes one exact selection, order, purpose, and prompt,
// so every change to those invalidates it. Draft, title, and folder text is not
// part of the packet and deliberately leaves it alone — but it is part of the
// planned note, so a receipt built from the packet falls with it.
export const invalidateSynthesisContext = (model: SynthesisModel): void => {
  invalidateSynthesisPreview(model);
  if (model.context === null) {
    return;
  }
  model.context = null;
  model.contextStale = true;
};

export const synthesisSelectedKeys = (model: SynthesisModel): string[] =>
  model.sources.map((source) => source.key);

// Every input a context or a preview request describes stays editable while that
// request is in flight. A signature taken before the request and compared against
// the model when the response lands is what stops a late success from installing
// a packet or a receipt for values the user has since replaced.
export const synthesisContextSignature = (model: SynthesisModel): string =>
  JSON.stringify([synthesisSelectedKeys(model), model.purpose, model.prompt]);

export const synthesisPreviewSignature = (model: SynthesisModel): string =>
  JSON.stringify([
    model.context === null ? "" : model.context.context_id,
    model.title.trim(),
    model.folder.trim(),
    model.draft,
  ]);

export const synthesisSourcePosition = (model: SynthesisModel, key: string): number => {
  const index = model.sources.findIndex((source) => source.key === key);
  return index < 0 ? 0 : index + 2;
};

export const formatSynthesisBytes = (bytes: number): string => {
  if (!Number.isFinite(bytes) || bytes < 0) {
    return "0 bytes";
  }
  const whole = Math.trunc(bytes);
  if (whole < 1024) {
    return `${whole} ${whole === 1 ? "byte" : "bytes"}`;
  }
  return `${whole} bytes (${(whole / 1024).toFixed(1)} KiB)`;
};

// synthesisDraftContainsSources mirrors the fence-aware scan noteops uses to
// reject a draft that writes its own Sources section. Doing it here puts the
// message beside the draft field; the server still refuses independently.
export const synthesisDraftContainsSources = (draft: string): boolean => {
  let fenced = false;
  for (const raw of draft.split("\n")) {
    const line = raw.replace(/\r$/, "");
    if (/^\s{0,3}(```+|~~~+)/.test(line)) {
      fenced = !fenced;
      continue;
    }
    if (fenced) {
      continue;
    }
    const match = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/.exec(line);
    if (match && match[1].length === 2 && match[2].trim().normalize("NFC").toLowerCase() === "sources") {
      return true;
    }
  }
  return false;
};

export const synthesisTitleError = (title: string): string => {
  const trimmed = title.trim();
  if (trimmed === "") {
    return "A note title is required.";
  }
  if (trimmed.includes("/")) {
    return "A note title may not contain '/'.";
  }
  if (trimmed.startsWith(".")) {
    return "A note title may not start with '.'.";
  }
  if (/[[\]|#^]/.test(trimmed)) {
    return "A note title may not contain '[', ']', '|', '#', or '^'.";
  }
  return "";
};

export const synthesisDraftError = (draft: string): string => {
  if (new TextEncoder().encode(draft).length > SYNTHESIS_MAX_DRAFT_BYTES) {
    return `The draft is larger than the ${SYNTHESIS_MAX_DRAFT_BYTES} byte maximum.`;
  }
  if (draft.trim() === "") {
    return "A draft is required.";
  }
  if (synthesisDraftContainsSources(draft)) {
    return "The draft may not contain a '## Sources' heading; the note adds one from the selected sources.";
  }
  return "";
};

// The folder is vault-relative and may be empty, meaning the vault root.
export const synthesisFolderError = (folder: string): string => {
  const trimmed = folder.trim();
  if (trimmed === "") {
    return "";
  }
  if (trimmed.startsWith("/")) {
    return "A folder must be vault-relative.";
  }
  if (trimmed.split("/").some((segment) => segment === ".." || segment === ".")) {
    return "A folder may not contain '.' or '..' segments.";
  }
  return "";
};

// The destination is the note the plan would write, shown before a preview
// exists so a title collision is visible while it is still cheap to change.
export const synthesisDestinationPath = (model: SynthesisModel): string => {
  const title = model.title.trim();
  if (title === "") {
    return "";
  }
  const folder = model.folder.trim().replace(/^\/+|\/+$/g, "");
  return folder === "" ? `${title}.md` : `${folder}/${title}.md`;
};

export const formatSynthesisExpiry = (value: string): string => {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }
  return parsed.toLocaleString();
};

export const synthesisPurposeLabel = (purpose: string): string => {
  switch (purpose) {
    case "synthesis":
      return "Synthesis";
    case "contradictions":
      return "Contradictions";
    case "questions":
      return "Questions";
    default:
      return purpose;
  }
};
