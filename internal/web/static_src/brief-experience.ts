import { readableExcerpt } from "./note-excerpt";
import { readNoteResume } from "./note-resume";
import { enhanceBriefConnection } from "./brief-connection";
import { noteKeyFromPath, readNoteReference } from "./note-reference";
import { recentDateLabel } from "./library-recent";

export const briefOpenLabel = (slot: string): string => {
  if (slot === "continue" && document.documentElement.dataset.readOnly === "true") return "Read note";
  return slot === "continue" ? "Resume writing" : slot === "rediscover" ? "Revisit note" : "Explore connection";
};

export const enhanceBriefCards = (root: ParentNode): void => {
  for (const slot of root.querySelectorAll<HTMLElement>("[data-brief-slot]:not(button)")) {
    const card = slot.querySelector<HTMLElement>("[data-brief-card]");
    if (!card) continue;
    enhancePurpose(slot);
    enhanceCardCopy(card, slot.dataset.briefSlot || "");
    if (slot.dataset.briefSlot !== "connect") enhanceDate(card);
    if (slot.dataset.briefSlot === "continue") enhanceResume(card);
    if (slot.dataset.briefSlot === "connect") enhanceBriefConnection(card);
  }
};

const enhancePurpose = (slot: HTMLElement): void => {
  const header = slot.querySelector(".panel-header");
  if (!header || header.querySelector(".brief-slot-purpose")) return;
  const purpose = document.createElement("p");
  purpose.className = "brief-slot-purpose";
  const purposes: Record<string, string> = { continue: "Pick up your thread", rediscover: "An idea worth another look", connect: "See how these ideas meet" };
  purpose.textContent = purposes[slot.dataset.briefSlot || ""];
  header.append(purpose);
};

const enhanceResume = (card: HTMLElement): void => {
  const link = card.querySelector<HTMLAnchorElement>(".brief-card-title a");
  if (!link) return;
  const resume = readNoteResume(decodeURIComponent(new URL(link.href).pathname.slice(3)));
  if (!resume) return;
  const context = card.querySelector<HTMLElement>(".brief-card-context");
  if (context) context.textContent = resume.heading || !resume.label.startsWith("Your last ")
    ? `Continue at “${resume.label}”` : "Pick up at your last passage";
  card.dataset.rememberedPassage = "true";
  const excerpt = card.querySelector<HTMLElement>(".brief-card-excerpt");
  if (excerpt && resume.excerpt) excerpt.textContent = readableExcerpt(resume.excerpt);
  const action = card.querySelector<HTMLButtonElement>("[data-brief-action-open]");
  if (action && !action.classList.contains("is-opened")) action.textContent = "Continue here";
};

const enhanceCardCopy = (card: HTMLElement, slot: string): void => {
  enhanceBridgeTitle(card);
  const excerpt = card.querySelector<HTMLElement>(".brief-card-excerpt");
  if (excerpt) excerpt.textContent = readableExcerpt(excerpt.textContent || "");
  if (slot === "connect" || card.querySelector(".brief-card-context")) return;
  const context = document.createElement("p");
  context.className = "brief-card-context";
  context.textContent = card.querySelector(".brief-card-explanation")?.textContent?.trim() || "";
  card.querySelector(".brief-card-title")?.after(context);
};

const enhanceDate = (card: HTMLElement): void => {
  const link = card.querySelector<HTMLAnchorElement>(".brief-card-title a");
  if (!link || card.dataset.datePreview) return;
  card.dataset.datePreview = "true";
  const controller = new AbortController();
  const stop = (): void => controller.abort();
  window.addEventListener("pagehide", stop, { once: true });
  void readNoteReference(noteKeyFromPath(link.href), controller.signal).then((note) => {
    if (!card.isConnected || controller.signal.aborted || card.dataset.rememberedPassage || !note.modified) return;
    const date = new Date(note.modified);
    const label = recentDateLabel(date, new Date());
    if (!label) return;
    const time = document.createElement("time"); time.dateTime = note.modified;
    time.title = date.toLocaleString(); time.textContent = `Edited ${label.replace(/^(Today|Yesterday)$/, (value) => value.toLowerCase())}`;
    card.querySelector(".brief-card-context")?.replaceChildren(time);
  }).catch(() => { /* The existing recommendation reason remains available. */ })
    .finally(() => window.removeEventListener("pagehide", stop));
};

const enhanceBridgeTitle = (card: HTMLElement): void => {
  const bridge = card.dataset.connectionBridge;
  const connection = card.querySelector(".brief-connection-source");
  if (bridge && connection?.lastChild?.nodeType === Node.TEXT_NODE) {
    connection.lastChild.textContent = ` · through ${bridge.replace(/\.md$/i, "").split("/").pop()}`;
  }
};
