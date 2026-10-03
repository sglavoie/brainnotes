// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initLibrarySort } from "./library-sort";
import { filterChangeEventName } from "./filters";

afterEach(() => { document.body.innerHTML = ""; window.history.replaceState(null, "", "/"); });

it("preserves filters and limits, and restores a saved relevance anchor when reselected", () => {
  window.history.replaceState(null, "", "/library?q=notes&tag=work&expr=status:wip&sort=relevance&relative_to=Hub&limit=10");
  document.body.innerHTML = '<select data-library-sort><option value="">Default</option><option value="-modified">Recent</option></select>';
  const select = document.querySelector("select")!;
  const changed = vi.fn();
  window.addEventListener(filterChangeEventName, changed);
  initLibrarySort();
  expect(select.value).toBe("relevance");
  expect(new URLSearchParams(location.search).get("relative_to")).toBe("Hub");
  select.value = "-modified";
  select.dispatchEvent(new Event("change"));
  const params = new URLSearchParams(location.search);
  expect(Object.fromEntries(params)).toEqual({ q: "notes", tag: "work", expr: "status:wip", sort: "-modified", limit: "10" });
  expect(changed).toHaveBeenCalledOnce();
  select.value = "relevance";
  select.dispatchEvent(new Event("change"));
  expect(new URLSearchParams(location.search).get("relative_to")).toBe("Hub");
  select.value = "";
  select.dispatchEvent(new Event("change"));
  expect(new URLSearchParams(location.search).has("sort")).toBe(false);
  expect(new URLSearchParams(location.search).has("relative_to")).toBe(false);
  window.removeEventListener(filterChangeEventName, changed);
});
