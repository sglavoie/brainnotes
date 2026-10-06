// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { initQueryBar } from "./query-bar";
import {
  completionsPayload, jsonResponse, manualScheduler, mountQueryBar, pressKey,
  routedFetch, typeQuery, validatePayload, type QueryRoutes,
} from "./query-bar.test-helpers";

const setup = (routes: QueryRoutes) => {
  window.history.replaceState(null, "", "/library?q=notes&tag=work&sort=title&limit=2");
  const ui = mountQueryBar();
  const scheduler = manualScheduler();
  const { fetcher, calls } = routedFetch(routes);
  initQueryBar({ fetch: fetcher, scheduler });
  return { ...ui, scheduler, calls, retry: ui.root.querySelector<HTMLButtonElement>("[data-query-bar-retry]")! };
};
const deferred = <T,>() => {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
};
const phase = (ui: ReturnType<typeof setup>): string | undefined => ui.root.dataset.queryBarPhase;

afterEach(() => {
  document.body.innerHTML = "";
  window.history.replaceState(null, "", "/");
});

it("retries validation once without changing the applied search until confirmed", async () => {
  let fail = true;
  const pending = deferred<Response>();
  const ui = setup({ validate: () => fail ? jsonResponse({}, 503) : pending.promise });
  typeQuery(ui.input, "tag:focus");
  ui.scheduler.flush();
  await vi.waitFor(() => expect(phase(ui)).toBe("unavailable"));
  const originalURL = window.location.href;
  expect(ui.retry.hidden).toBe(false);
  fail = false;
  ui.retry.click();
  ui.retry.click();
  expect(phase(ui)).toBe("checking");
  expect(ui.retry.hidden).toBe(true);
  expect(ui.calls.filter((url) => url.includes("/validate"))).toHaveLength(2);
  expect(window.location.href).toBe(originalURL);
  expect(ui.input.value).toBe("tag:focus");
  pending.resolve(jsonResponse(validatePayload()));
  await vi.waitFor(() => expect(phase(ui)).toBe("valid"));
  expect(Object.fromEntries(new URL(window.location.href).searchParams)).toEqual({
    q: "notes", tag: "work", sort: "title", limit: "2", expr: "tag:focus",
  });
  expect(ui.retry.hidden).toBe(true);
});

it("does not apply a retry response after the expression has been edited", async () => {
  const pending = deferred<Response>();
  let fail = true;
  const ui = setup({ validate: () => fail ? jsonResponse({}, 503) : pending.promise });
  typeQuery(ui.input, "tag:focus");
  ui.scheduler.flush();
  await vi.waitFor(() => expect(phase(ui)).toBe("unavailable"));
  fail = false;
  ui.retry.click();
  typeQuery(ui.input, "tag:work");
  pending.resolve(jsonResponse(validatePayload()));
  await pending.promise;
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(phase(ui)).toBe("typing");
  expect(new URLSearchParams(window.location.search).has("expr")).toBe(false);
  expect(ui.input.value).toBe("tag:work");
});

it("dismisses suggestions and leaves composition keys to the input method", async () => {
  const ui = setup({
    validate: () => jsonResponse(validatePayload()),
    completions: () => jsonResponse(completionsPayload("tag:f", [{ value: "focus" }])),
  });
  typeQuery(ui.input, "tag:f");
  ui.scheduler.flush();
  await vi.waitFor(() => expect(ui.options()).toHaveLength(1));
  const before = window.location.href;
  const calls = ui.calls.length;
  ui.input.dispatchEvent(new CompositionEvent("compositionstart"));
  expect(ui.list.hidden).toBe(true);
  typeQuery(ui.input, "tag:日本");
  for (const key of ["Enter", "Tab", "ArrowDown", "ArrowUp", "Escape", " "]) {
    expect(pressKey(ui.input, key, { ctrlKey: key === " " }).defaultPrevented).toBe(false);
  }
  ui.scheduler.flush();
  expect(ui.calls).toHaveLength(calls);
  expect(ui.input.value).toBe("tag:日本");
  expect(window.location.href).toBe(before);
  ui.input.dispatchEvent(new CompositionEvent("compositionend"));
  // Browsers may send a final input event after compositionend; debounce it once.
  ui.input.dispatchEvent(new InputEvent("input", { isComposing: false }));
  ui.scheduler.flush();
  await vi.waitFor(() => expect(phase(ui)).toBe("valid"));
  expect(new URLSearchParams(window.location.search).get("expr")).toBe("tag:日本");
  expect(ui.calls.filter((url) => url.includes("/validate"))).toHaveLength(2);
});

it("cancels queued validation when composition begins", () => {
  const ui = setup({});
  typeQuery(ui.input, "tag:f");
  ui.input.dispatchEvent(new CompositionEvent("compositionstart"));
  ui.scheduler.flush();
  expect(ui.calls).toHaveLength(0);
});

it.each([false, true])("ignores responses from before composition, including failures (%s)", async (reject) => {
  const validation = deferred<Response>();
  const completion = deferred<Response>();
  const ui = setup({ validate: () => validation.promise, completions: () => completion.promise });
  typeQuery(ui.input, "tag:f");
  ui.scheduler.flush();
  ui.input.dispatchEvent(new CompositionEvent("compositionstart"));
  typeQuery(ui.input, "tag:日");
  if (reject) {
    validation.reject(new Error("offline"));
    completion.reject(new Error("offline"));
  } else {
    validation.resolve(jsonResponse(validatePayload()));
    completion.resolve(jsonResponse(completionsPayload("tag:f", [{ value: "focus" }])));
  }
  await Promise.allSettled([validation.promise, completion.promise]);
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(ui.options()).toHaveLength(0);
  expect(ui.retry.hidden).toBe(true);
  expect(new URLSearchParams(window.location.search).has("expr")).toBe(false);
});

it.each([{ isComposing: true }, { keyCode: 229 }])("ignores composition key flags without a preceding event (%j)", (flags) => {
  const ui = setup({});
  expect(pressKey(ui.input, "Enter", flags).defaultPrevented).toBe(false);
  ui.scheduler.flush();
  expect(ui.calls).toHaveLength(0);
});
