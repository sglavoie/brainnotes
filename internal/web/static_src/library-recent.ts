import { readableExcerpt } from "./note-excerpt";

export const recentDateLabel = (date: Date, now: Date): string => {
  const midnight = (value: Date): number => Date.UTC(value.getFullYear(), value.getMonth(), value.getDate());
  const days = Math.round((midnight(now) - midnight(date)) / 86400000);
  if (date.getTime() > now.getTime() || !Number.isFinite(days)) return "";
  if (days === 0) return "Today";
  if (days === 1) return "Yesterday";
  if (days < 7) return `${days} days ago`;
  return date.toLocaleDateString(undefined, { month: "short", day: "numeric", ...(date.getFullYear() !== now.getFullYear() ? { year: "numeric" } : {}) });
};

export const initLibraryRecent = (): void => {
  const root = document.querySelector<HTMLElement>("[data-library-activity]");
  const button = root?.querySelector<HTMLButtonElement>("[data-library-density]");
  if (!root || !button) return;
  for (const excerpt of root.querySelectorAll(".recent-note-excerpt")) excerpt.textContent = readableExcerpt(excerpt.textContent || "");
  const updateDates = (): void => {
    const now = new Date();
    for (const time of root.querySelectorAll<HTMLTimeElement>("[data-recent-time]")) {
      time.textContent = recentDateLabel(new Date(time.dateTime), now) || time.title;
      time.setAttribute("aria-label", `${time.textContent}, ${time.title}`);
    }
  };
  updateDates();
  document.addEventListener("visibilitychange", () => { if (!document.hidden) updateDates(); });
  const apply = (compact: boolean): void => {
    root.dataset.density = compact ? "compact" : "comfortable";
    button.setAttribute("aria-pressed", String(compact));
  };
  try { apply(localStorage.getItem("bn.library-density") === "compact"); } catch { apply(false); }
  button.hidden = false;
  button.addEventListener("click", () => {
    apply(root.dataset.density !== "compact");
    try { localStorage.setItem("bn.library-density", root.dataset.density!); } catch { /* This visit still works. */ }
  });
};
