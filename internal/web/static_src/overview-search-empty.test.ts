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
  expect(document.querySelector("[data-overview-search-status]")?.textContent).toBe('No notes match "雪 & rain"; try changing your search text, filters, and expression.');
  expect(help.querySelector(".bn-primary")?.textContent).toBe("Reset search");
  const links = Array.from(help.querySelectorAll<HTMLAnchorElement>("a:not([data-empty-create-note], [data-empty-reset])"));
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
  ["?q=missing", "search text", ["Remove search text"]],
  ["?tag=missing", "filters", ["Remove filters"]],
  ["?expr=status:missing", "expression", ["Remove expression"]],
] as const)("offers only applicable actions for %s", async (query, _summary, labels) => {
  const { help } = await setup(query);
  expect(help.hidden).toBe(false);
  expect(help.querySelectorAll(".bn-primary")).toHaveLength(1);
  expect(Array.from(help.querySelectorAll("a:not([data-empty-create-note], [data-empty-reset])"), (link) => link.textContent)).toEqual(labels);
  if (labels.length) expect(help.querySelector("a")?.getAttribute("href")).toBe("/library");
});

it("does not offer to remove whitespace-only search text", async () => {
  const { help } = await setup("?q=%20%20&tag=missing");
  expect(help.querySelector(".bn-primary")?.getAttribute("href")).toBe("/library");
  expect(Array.from(help.querySelectorAll("a:not([data-empty-create-note], [data-empty-reset])"), (link) => link.textContent)).toEqual(["Remove filters"]);
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

it("offers a reviewable title with a return search, and hides creation on replicas", async () => {
  const title = '雪 & "new idea"';
  const { help } = await setup(`?${new URLSearchParams({ q: title, folder: "Notes" })}`);
  const create = help.querySelector<HTMLAnchorElement>("[data-empty-create-note]")!;
  expect(create.textContent).toBe(`Create a note titled “${title}”…`);
  expect(new URL(create.href).pathname).toBe("/new");
  expect(new URL(create.href).searchParams.get("title")).toBe(title);
  expect(new URL(create.href).searchParams.get("return_to")).toBe(window.location.pathname + window.location.search);
  document.documentElement.dataset.readOnly = "true";
  const replica = await setup("?q=missing");
  expect(replica.help.querySelector("[data-empty-create-note]")).toBeNull();
});

it("offers the closest titles ahead of the ways to loosen a search that found nothing", async () => {
  const suggest = vi.fn().mockResolvedValue(new Response(JSON.stringify([
    { key: "concepts/deliberate practice", path: "/n/concepts/deliberate%20practice", title: "Deliberate Practice", folder: "Concepts" },
    { key: "practice log", path: "/n/practice%20log", title: "Practice Log", folder: "" },
  ])));
  vi.stubGlobal("fetch", suggest);
  try {
    const { help } = await setup("?q=delibrate");
    await vi.waitFor(() => expect(help.querySelector("[data-empty-closest]")).not.toBeNull());
    const line = help.querySelector<HTMLElement>("[data-empty-closest]")!;
    expect(help.firstElementChild).toBe(line);
    expect(line.textContent).toBe("Did you mean Deliberate Practice or Practice Log?");
    expect(Array.from(line.querySelectorAll("a")).map((link) => link.getAttribute("href"))).toEqual(["/n/concepts/deliberate%20practice", "/n/practice%20log"]);
    expect(String(suggest.mock.calls[0][0])).toBe("/api/search/suggest?q=delibrate");
  } finally {
    vi.unstubAllGlobals();
  }
});

it("asks for no suggestions when the empty result is a filter's doing", async () => {
  const suggest = vi.fn();
  vi.stubGlobal("fetch", suggest);
  try {
    const { help } = await setup("?tag=missing");
    expect(help.querySelector("[data-empty-closest]")).toBeNull();
    expect(suggest).not.toHaveBeenCalled();
  } finally {
    vi.unstubAllGlobals();
  }
});
