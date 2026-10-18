import { type BriefSlotKind } from "./api";
import { notePathForKey } from "./filters";

export type FetchLike = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;
export type ConfirmLike = (message: string, returnFocus?: Element | null) => boolean | Promise<boolean>;
export type ClipboardWriter = (text: string) => Promise<void>;

export type DailyBriefDeps = {
  fetch?: FetchLike;
  confirm?: ConfirmLike;
  writeClipboard?: ClipboardWriter;
};

export type DailyBriefConfig = {
  viewEndpoint: string;
  itemEndpoint: string;
  insightEndpoint: string;
  csrfToken: string;
  insightEnabled: boolean;
};

export const readDailyBriefConfig = (root: HTMLElement): DailyBriefConfig => {
  const insightValue = (root.getAttribute("data-brief-insight-enabled") || "").toLowerCase();
  return {
    viewEndpoint: root.getAttribute("data-brief-view-endpoint") || "/api/brief/view",
    itemEndpoint: root.getAttribute("data-brief-item-endpoint") || "/api/brief/item",
    insightEndpoint: root.getAttribute("data-brief-insight-endpoint") || "/api/brief/insight",
    csrfToken: root.getAttribute("data-brief-csrf") || "",
    insightEnabled: insightValue === "true" || insightValue === "1",
  };
};

export const slotOrder: BriefSlotKind[] = ["continue", "rediscover", "connect"];
export const statusClasses = ["is-success", "is-error", "is-loading", "is-empty"];
export const insightPromptMaxLength = 2000;

export const normalizeSlot = (value: string): BriefSlotKind | "" => {
  switch ((value || "").toLowerCase()) {
    case "continue":
    case "rediscover":
    case "connect":
      return value.toLowerCase() as BriefSlotKind;
    default:
      return "";
  }
};

export const slotLabel = (slot: BriefSlotKind): string => {
  switch (slot) {
    case "continue":
      return "Continue";
    case "rediscover":
      return "Rediscover";
    case "connect":
      return "Connect";
  }
};

export const unavailableTitle = (slot: BriefSlotKind): string => {
  switch (slot) {
    case "continue":
      return "No substantive recent note";
    case "rediscover":
      return "No rediscover candidate";
    case "connect":
      return "No valid connection";
  }
};

export const relationshipLabel = (relationship: string): string => {
  switch ((relationship || "").toLowerCase()) {
    case "inbound":
      return "Inbound link";
    case "outbound":
      return "Outbound link";
    case "shared-neighbor":
      return "Shared neighbor";
    default:
      return relationship;
  }
};

// briefNoteKeyFromRelPath mirrors server route-key generation: trim, normalize
// slashes, strip one trailing .md, reject traversal/empty segments, lowercase.
export const briefNoteKeyFromRelPath = (relPath: string): string => {
  const trimmed = (relPath || "").trim().replace(/\\/g, "/");
  if (trimmed === "") {
    return "";
  }
  const withoutExt = trimmed.replace(/\.md$/i, "");
  const segments: string[] = [];
  for (const segment of withoutExt.split("/")) {
    if (segment === "" || segment === "." || segment === "..") {
      return "";
    }
    segments.push(segment.toLowerCase());
  }
  return segments.join("/");
};

export const briefNotePathForRelPath = (relPath: string): string => {
  const key = briefNoteKeyFromRelPath(relPath);
  return key ? notePathForKey(key) : "";
};

export const insightDisclosureMessage = [
  "Ask AI sends this card's context and your optional prompt to your configured [agent].command.",
  "BrainNotes does not store the prompt or AI output. Continue?",
].join("\n");
