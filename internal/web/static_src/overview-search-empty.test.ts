// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initOverviewSearchResults } from "./overview-search";
import { renderOverviewBulkFixture } from "./search-rendering.test-helpers";
import { searchResult, setURL } from "./search-test-helpers";

afterEach(() => {
  vi.useRealTimers();
  document.body.innerHTML = "";
  delete document.documentElement.dataset.readOnly;
  setURL("/");
});

const setup = async (query: string, response = new Response("[]")) => {
  vi.useFakeTimers();
  renderOverviewBulkFixture();
  setURL(`/library${query}`);
  const fetcher = vi.fn().mockResolvedValueOnce(response);
  initOverviewSearchResults({ fetch: fetcher });
  await vi.runAllTimersAsync();
  return { help: document.querySelector<HTMLElement>("[data-overview-empty-help]")!, fetcher };
};

it("explains active constraints and removes only the chosen kind, preserving repeated facets and ordering", async () => {
  const params = new URLSearchParams({ q: "雪 & rain", expr: "status:wip", sort: "relevance", relative_to: "hub", limit: "20" });
  params.append("tag", "one");
  params.append("tag", "two");
  params.set("folder", "Projects");
  const { help } = await setup(`?${params}`);
  expect(help.hidden).toBe(false);
  expect(help.textContent).toContain("Searching with: text, filters, expression.");
  const links = Array.from(help.querySelectorAll<HTMLAnchorElement>("a:not([data-empty-create-note])"));
  expect(links.map((link) => link.textContent)).toEqual(["Remove search text", "Remove filters", "Remove expression"]);
  const withoutText = new URLSearchParams(params);
  withoutText.delete("q");
  expect(new URL(links[0].href).searchParams.toString()).toBe(withoutText.toString());
  const withoutFilters = new URLSearchParams(params);
  withoutFilters.delete("tag");
  withoutFilters.delete("folder");
  expect(new URL(links[1].href).searchParams.toString()).toBe(withoutFilters.toString());
  const withoutExpression = new URLSearchParams(params);
  withoutExpression.delete("expr");
  expect(new URL(links[2].href).searchParams.toString()).toBe(withoutExpression.toString());
  expect(links.every((link) => new URL(link.href).pathname === "/library")).toBe(true);
});

it.each([
  ["?q=missing", "text", ["Remove search text"]],
  ["?tag=missing", "filters", ["Remove filters"]],
  ["?expr=status:missing", "expression", ["Remove expression"]],
] as const)("offers only applicable actions for %s", async (query, summary, labels) => {
  const { help } = await setup(query);
  expect(help.hidden).toBe(false);
  expect(help.textContent).toContain(`Searching with: ${summary}.`);
  expect(Array.from(help.querySelectorAll("a:not([data-empty-create-note])"), (link) => link.textContent)).toEqual(labels);
  if (labels.length) expect(help.querySelector("a")?.getAttribute("href")).toBe("/library");
});

it("does not offer to remove whitespace-only search text", async () => {
  const { help } = await setup("?q=%20%20&tag=missing");
  expect(help.textContent).toContain("Searching with: filters.");
  expect(Array.from(help.querySelectorAll("a:not([data-empty-create-note])"), (link) => link.textContent)).toEqual(["Remove filters"]);
});

it.each(["blank", "error", "matches"])("does not offer empty-result help for %s", async (state) => {
  const response = state === "error" ? new Response("{}", { status: 503 })
    : new Response(JSON.stringify([searchResult()]));
  const { help, fetcher } = await setup(state === "blank" ? "" : "?q=note&tag=work", response);
  expect(help.hidden).toBe(true);
  if (state === "blank") expect(fetcher).not.toHaveBeenCalled();
});

it("hides old help immediately during a new search and keeps it hidden after a failed request", async () => {
  const { help, fetcher } = await setup("?q=missing&tag=work");
  expect(help.hidden).toBe(false);
  fetcher.mockRejectedValueOnce(new Error("offline"));
  const input = document.querySelector<HTMLInputElement>("#search-input")!;
  input.value = "changed";
  input.dispatchEvent(new Event("input"));
  expect(help.hidden).toBe(true);
  await vi.runAllTimersAsync();
  expect(help.hidden).toBe(true);
});

it("offers a reviewable title without carrying filters, and hides creation on replicas", async () => {
  const title = '雪 & "new idea"';
  const { help } = await setup(`?${new URLSearchParams({ q: title, folder: "Notes" })}`);
  const create = help.querySelector<HTMLAnchorElement>("[data-empty-create-note]")!;
  expect(create.textContent).toBe(`Create a note titled “${title}”…`);
  expect(new URL(create.href).pathname).toBe("/new");
  expect(new URL(create.href).searchParams.toString()).toBe(new URLSearchParams({ title }).toString());
  document.documentElement.dataset.readOnly = "true";
  const replica = await setup("?q=missing");
  expect(replica.help.querySelector("[data-empty-create-note]")).toBeNull();
});
