import { readableExcerpt } from "./note-excerpt";
import { readNoteResume } from "./note-resume";

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
    if (slot.dataset.briefSlot === "continue") enhanceResume(card);
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
  if (context) context.textContent = `Last passage in this tab · ${resume.label}`;
  const excerpt = card.querySelector<HTMLElement>(".brief-card-excerpt");
  if (excerpt && resume.excerpt) excerpt.textContent = readableExcerpt(resume.excerpt);
};

const enhanceCardCopy = (card: HTMLElement, slot: string): void => {
  enhanceBridgeTitle(card);
  const excerpt = card.querySelector<HTMLElement>(".brief-card-excerpt");
  if (excerpt) excerpt.textContent = readableExcerpt(excerpt.textContent || "");
  if (slot === "connect" || card.querySelector(".brief-card-context")) return;
  const context = document.createElement("p");
  context.className = "brief-card-context";
  context.textContent = slot === "continue" ? "Your latest writing" : "Something to revisit";
  card.querySelector(".brief-card-title")?.after(context);
};

const enhanceBridgeTitle = (card: HTMLElement): void => {
  const bridge = card.dataset.connectionBridge;
  const connection = card.querySelector(".brief-connection-source");
  if (bridge && connection?.lastChild?.nodeType === Node.TEXT_NODE) {
    connection.lastChild.textContent = ` · through ${bridge.replace(/\.md$/i, "").split("/").pop()}`;
  }
};
