// @vitest-environment jsdom

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { filterChangeEventName } from "./filters";
import { initQueryBar } from "./query-bar";
import { createQueryRequester } from "./query-bar-requests";
import {
  completionsPayload,
  invalidValidatePayload,
  jsonResponse,
  manualScheduler,
  mountQueryBar,
  pressKey,
  routedFetch,
  typeQuery,
  validatePayload,
  waitForExpect,
  type ManualScheduler,
  type MountedQueryBar,
  type QueryRoutes,
} from "./query-bar.test-helpers";

const setLocation = (search: string): void => {
  window.history.replaceState(null, "", `/library${search}`);
};

const exprParam = (): string | null =>
  new URLSearchParams(window.location.search).get("expr");

const optionValues = (bar: { options: () => HTMLElement[] }): string[] =>
  bar
    .options()
    .map((option) => option.querySelector(".query-bar-completion-value")?.textContent || "");

type Harness = MountedQueryBar & {
  scheduler: ManualScheduler;
  calls: string[];
};

const startQueryBar = (routes: QueryRoutes): Harness => {
  const els = mountQueryBar();
  const scheduler = manualScheduler();
  const { fetcher, calls } = routedFetch(routes);
  initQueryBar({ fetch: fetcher, scheduler });
  return { ...els, scheduler, calls };
};

beforeEach(() => {
  setLocation("");
});

afterEach(() => {
  document.body.innerHTML = "";
  vi.unstubAllGlobals();
});

describe("query bar validation", () => {
  it("publishes a confirmed expression to the URL and announces the change", async () => {
    const seen: string[] = [];
    const listener = (): void => {
      seen.push(exprParam() || "");
    };
    window.addEventListener(filterChangeEventName, listener);

    const bar = startQueryBar({
      validate: (url) =>
        jsonResponse(validatePayload({ query: url.searchParams.get("expr") || "" })),
    });

    typeQuery(bar.input, "tag:focus");
    expect(exprParam()).toBeNull();
    bar.scheduler.flush();

    await waitForExpect(() => {
      expect(bar.statusEl.textContent).toBe("Expression is valid.");
    });
    expect(exprParam()).toBe("tag:focus");
    expect(seen).toEqual(["tag:focus"]);
    expect(bar.root.getAttribute("data-query-bar-phase")).toBe("valid");
    expect(bar.input.getAttribute("aria-invalid")).toBe("false");

    window.removeEventListener(filterChangeEventName, listener);
  });

  it("never publishes an expression the server rejected", async () => {
    const bar = startQueryBar({
      validate: () => jsonResponse(invalidValidatePayload("statuz:wip")),
    });

    typeQuery(bar.input, "statuz:wip");
    bar.scheduler.flush();

    await waitForExpect(() => {
      expect(bar.errorEl.hidden).toBe(false);
    });
    expect(exprParam()).toBeNull();
    expect(bar.input.getAttribute("aria-invalid")).toBe("true");
  });

  it("renders the error strip with a caret under the offending column", async () => {
    const query = "tag:focus and statuz:wip";
    const bar = startQueryBar({
      validate: () =>
        jsonResponse(
          invalidValidatePayload(query, {
            message: "unknown field \"statuz\"",
            start: 14,
            end: 20,
            column: 15,
            suggestions: ["status"],
          })
        ),
    });

    typeQuery(bar.input, query);
    bar.scheduler.flush();

    await waitForExpect(() => {
      expect(bar.errorEl.hidden).toBe(false);
    });
    const message = bar.errorEl.querySelector("[data-query-bar-error-message]");
    const echo = bar.errorEl.querySelector("[data-query-bar-error-query]");
    const caret = bar.errorEl.querySelector("[data-query-bar-error-caret]");
    const suggestions = bar.errorEl.querySelector(
      "[data-query-bar-error-suggestions]"
    ) as HTMLElement;

    expect(message?.textContent).toBe('Column 15: unknown field "statuz"');
    expect(echo?.textContent).toBe(query);
    expect(caret?.textContent).toBe(`${" ".repeat(14)}^^^^^^`);
    expect(suggestions.hidden).toBe(false);
    expect(suggestions.textContent).toBe("Did you mean: status?");
  });

  it("reports a failed check as unavailable and keeps the URL alone", async () => {
    const bar = startQueryBar({
      validate: () => jsonResponse({}, 500),
    });

    typeQuery(bar.input, "tag:focus");
    bar.scheduler.flush();

    await waitForExpect(() => {
      expect(bar.statusEl.textContent).toBe("Expression check unavailable.");
    });
    expect(bar.errorEl.hidden).toBe(true);
    expect(exprParam()).toBeNull();
  });

  it("re-validates an expression the URL arrived with", async () => {
    setLocation("?expr=tag%3Afocus");
    const bar = startQueryBar({
      validate: () => jsonResponse(validatePayload()),
    });

    expect(bar.input.value).toBe("tag:focus");
    await waitForExpect(() => {
      expect(bar.statusEl.textContent).toBe("Expression is valid.");
    });
    expect(bar.calls.some((call) => call.includes("expr=tag%3Afocus"))).toBe(true);
  });

  it("clears the expression and drops it from the URL", async () => {
    setLocation("?expr=tag%3Afocus&tag=focus");
    const bar = startQueryBar({
      validate: () => jsonResponse(validatePayload()),
    });
    await waitForExpect(() => {
      expect(bar.statusEl.textContent).toBe("Expression is valid.");
    });

    const clear = document.querySelector("[data-query-bar-clear]") as HTMLButtonElement;
    clear.click();

    expect(bar.input.value).toBe("");
    expect(exprParam()).toBeNull();
    // An unrelated filter parameter is not this bar's to remove.
    expect(new URLSearchParams(window.location.search).get("tag")).toBe("focus");
    expect(bar.statusEl.textContent).toBe("Type an expression to filter the vault.");
  });
});

describe("query bar completions", () => {
  const tagRoutes: QueryRoutes = {
    validate: () => jsonResponse(invalidValidatePayload("tag:")),
    completions: (url) =>
      jsonResponse(
        completionsPayload(url.searchParams.get("expr") || "", [
          { value: "focus", insert: "focus", count: 12 },
          { value: "reading", insert: "reading", count: 4 },
          { value: "deep-focus", insert: "deep-focus", count: 2 },
        ])
      ),
  };

  const openList = async (bar: Harness, text: string): Promise<void> => {
    typeQuery(bar.input, text);
    bar.scheduler.flush();
    await waitForExpect(() => {
      expect(bar.list.hidden).toBe(false);
    });
  };

  it("renders a listbox of options and activates the first one", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    expect(bar.list.getAttribute("role")).toBe("listbox");
    const options = bar.options();
    expect(options).toHaveLength(3);
    expect(options[0]?.getAttribute("aria-selected")).toBe("true");
    expect(options[1]?.getAttribute("aria-selected")).toBe("false");
    expect(bar.input.getAttribute("aria-expanded")).toBe("true");
    expect(bar.input.getAttribute("aria-activedescendant")).toBe(options[0]?.id);
  });

  it("moves aria-activedescendant with the arrow keys and wraps", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    pressKey(bar.input, "ArrowDown");
    expect(bar.input.getAttribute("aria-activedescendant")).toBe(bar.options()[1]?.id);

    pressKey(bar.input, "ArrowUp");
    pressKey(bar.input, "ArrowUp");
    expect(bar.input.getAttribute("aria-activedescendant")).toBe(bar.options()[2]?.id);
    expect(bar.options()[2]?.getAttribute("aria-selected")).toBe("true");
  });

  it("accepts the active option with Enter and moves the caret past it", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    pressKey(bar.input, "ArrowDown");
    const accepted = pressKey(bar.input, "Enter");

    expect(accepted.defaultPrevented).toBe(true);
    expect(bar.input.value).toBe("tag:reading");
    expect(bar.input.selectionStart).toBe(11);
    expect(bar.list.hidden).toBe(true);
    expect(bar.input.getAttribute("aria-expanded")).toBe("false");
    expect(bar.input.hasAttribute("aria-activedescendant")).toBe(false);
  });

  it("accepts the active option with Tab", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    const accepted = pressKey(bar.input, "Tab");
    expect(accepted.defaultPrevented).toBe(true);
    expect(bar.input.value).toBe("tag:focus");
  });

  it("closes the list on Escape without touching the text", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    const escape = pressKey(bar.input, "Escape");
    expect(escape.defaultPrevented).toBe(true);
    expect(bar.list.hidden).toBe(true);
    expect(bar.input.value).toBe("tag:");
    expect(bar.options()).toHaveLength(0);
  });

  it("narrows the open list to the new prefix before the next reply lands", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    // No flush: the debounced request has not gone out yet, so what the list
    // shows here is the local narrowing alone.
    typeQuery(bar.input, "tag:foc");
    expect(optionValues(bar)).toEqual(["focus", "deep-focus"]);
  });

  it("does not offer the field list right after a space", async () => {
    const bar = startQueryBar(tagRoutes);
    await openList(bar, "tag:");

    typeQuery(bar.input, "tag:focus and ");
    expect(bar.list.hidden).toBe(true);
  });

  it("chains straight to the value list after a field is accepted", async () => {
    const bar = startQueryBar({
      validate: () => jsonResponse(invalidValidatePayload("tag:")),
      completions: (url) => {
        const expr = url.searchParams.get("expr") || "";
        if (expr === "tag:") {
          return jsonResponse(completionsPayload(expr, [{ value: "focus", insert: "focus" }]));
        }
        return jsonResponse(
          completionsPayload(expr, [{ value: "tag", insert: "tag:", kind: "field" }], {
            kind: "field",
          })
        );
      },
    });

    await openList(bar, "ta");
    pressKey(bar.input, "Enter");
    expect(bar.input.value).toBe("tag:");

    await waitForExpect(() => {
      expect(optionValues(bar)).toEqual(["focus"]);
    });
  });
});

describe("query request supersession", () => {
  it("drops a slow reply once a newer request has gone out", async () => {
    let releaseFirst: () => void = () => {};
    const firstGate = new Promise<void>((resolve) => {
      releaseFirst = resolve;
    });
    const seen: string[] = [];

    const requester = createQueryRequester(async (input) => {
      const raw = typeof input === "string" ? input : input.toString();
      seen.push(raw);
      if (raw.includes("tag%3Afoc&") || raw.endsWith("tag%3Afoc")) {
        await firstGate;
        return jsonResponse(validatePayload({ query: "tag:foc" }));
      }
      return jsonResponse(invalidValidatePayload("tag:focus"));
    });

    const slow = requester.validate("tag:foc");
    const fresh = requester.validate("tag:focus");

    expect(await fresh).toMatchObject({ ok: false, query: "tag:focus" });
    releaseFirst();
    // The superseded request reports null rather than a payload, so nothing
    // downstream can mistake it for the current answer.
    expect(await slow).toBeNull();
    expect(seen).toHaveLength(2);
  });

  it("aborts the in-flight request when cancelled", async () => {
    const signals: Array<AbortSignal | undefined> = [];
    const requester = createQueryRequester(async (_input, init) => {
      signals.push(init?.signal || undefined);
      return jsonResponse(validatePayload());
    });

    await requester.validate("tag:focus");
    expect(signals[0]).toBeInstanceOf(AbortSignal);
    expect(signals[0]?.aborted).toBe(false);

    const pending = requester.validate("tag:reading");
    requester.cancel();
    expect(signals[1]?.aborted).toBe(true);
    expect(await pending).toBeNull();
  });

  it("surfaces a genuine failure rather than swallowing it", async () => {
    const requester = createQueryRequester(async () => {
      throw new Error("offline");
    });
    await expect(requester.validate("tag:focus")).rejects.toThrow("offline");
  });
});
