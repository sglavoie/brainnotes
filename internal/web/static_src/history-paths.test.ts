// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { findHistoryPanelElements, renderHistoryPanelEntries } from "./history-panel-view";
import type { HistoryEntry } from "./api";

afterEach(() => { document.body.innerHTML = ""; });

const render = (paths: string[]): ReturnType<typeof vi.fn> => {
  document.body.innerHTML = '<section data-history-panel><p data-history-status></p><ul data-history-list></ul></section>';
  const entry: HistoryEntry = {
    id: "entry-1", timestamp: "2026-10-03T12:00:00Z", command: "bn web move",
    origin: "browser", origin_label: "Browser", file_count: paths.length, classes: [],
    files: paths.map((path) => ({ path, before: "file", after: "file", class: "note", recorded: true, travels: true })),
  };
  const undo = vi.fn();
  renderHistoryPanelEntries(findHistoryPanelElements(document)!, [entry], undo);
  return undo;
};

it("shows the first three full paths and expands the remainder using native details", () => {
  render(["Folder/Old.md", "Folder/New.md", "Backlink.md", "Fourth.md", "Fifth.md"]);
  const main = document.querySelector(".history-item-main")!;
  expect([...main.querySelectorAll(":scope > .history-paths li > span")].map((el) => el.textContent))
    .toEqual(["Folder/Old.md", "Folder/New.md", "Backlink.md"]);
  expect(main.querySelector("summary")!.textContent).toBe("2 more paths");
  expect(main.querySelector("details")!.open).toBe(false);
  expect([...main.querySelectorAll("details li > span")].map((el) => el.textContent)).toEqual(["Fourth.md", "Fifth.md"]);
});

it.each([0, 1, 3])("needs no expander for %i paths and preserves undo identity", (count) => {
  const undo = render(Array.from({ length: count }, (_, i) => `Note ${i}.md`));
  expect(document.querySelectorAll(".history-paths li")).toHaveLength(count);
  expect(document.querySelector("details")).toBeNull();
  document.querySelector<HTMLButtonElement>("button")!.click();
  expect(undo).toHaveBeenCalledWith("entry-1");
});

it("renders filenames as literal text and labels a single remaining path", () => {
  render(['<img src=x onerror="alert(1)">.md', "Café.md", "Three.md", "Four.md"]);
  expect(document.querySelector("img")).toBeNull();
  expect(document.querySelector(".history-paths li > span")!.textContent).toBe('<img src=x onerror="alert(1)">.md');
  expect(document.querySelector("summary")!.textContent).toBe("1 more path");
});

it("links every recorded path to its complete exact history, including collapsed and missing paths", () => {
  const paths = ["Notes/Plan.md", "Notes/plan.md", "deleted.md", "attachments/Café #1 & 2?.png", " spaced.md "];
  render(paths);
  const links = [...document.querySelectorAll<HTMLAnchorElement>(".history-paths a")];
  expect(links).toHaveLength(paths.length);
  links.forEach((link, index) => {
    const url = new URL(link.href);
    expect(url.pathname).toBe("/history");
    expect([...url.searchParams]).toEqual([["path", paths[index]], ["path_match", "exact"]]);
    expect(link.getAttribute("aria-label")).toBe(`History for ${paths[index]}`);
    expect(link.textContent).toBe("History for this path");
  });
});
