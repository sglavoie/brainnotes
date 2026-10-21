// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import { initSynthesize } from "./synthesize";
import {
  addCandidate,
  dragRow,
  jsonResponse,
  memoryStorage,
  mount,
  query,
  relatedPayload,
  routedFetch,
  selectedKeys,
  start,
  statusText,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis workbench mounting", () => {
  it("renders the anchor first, hides the no-JavaScript fallback, and loads balanced suggestions", async () => {
    const fetcher = routedFetch({
      related: (url) =>
        jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", ["one", "two"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });

    expect(query<HTMLElement>("[data-synthesize-fallback]").hidden).toBe(true);
    expect(selectedKeys()).toEqual(["anchor"]);
    expect(query<HTMLElement>("[data-synthesize-source='anchor']").hasAttribute("data-synthesize-anchor")).toBe(
      true
    );
    expect(document.querySelector("[data-synthesize-handle]")).toBeNull();
    expect(
      Array.from(
        query<HTMLElement>("[data-synthesize-suggestions='balanced']").querySelectorAll(
          "[data-synthesize-candidate]"
        )
      ).map((card) => card.getAttribute("data-synthesize-candidate"))
    ).toEqual(["one", "two"]);
    expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toContain(
      "2 suggestions"
    );
  });

  it("never offers the anchor as a candidate", async () => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["anchor", "one"])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    expect(document.querySelector("[data-synthesize-add='anchor']")).toBeNull();
    expect(document.querySelector("[data-synthesize-add='one']")).not.toBeNull();
  });

  it("reports an empty mode and a failed mode without discarding the panel", async () => {
    const fetcher = routedFetch({
      related: (url) =>
        url.searchParams.get("mode") === "connect"
          ? new Response("boom", { status: 500 })
          : jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", [])),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toBe(
      "No suggestions are available in this mode."
    );

    query<HTMLButtonElement>("[data-synthesize-tab='connect']").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-suggestions-status]").textContent).toBe(
        "Related notes request failed (500)."
      );
    });
    expect(query<HTMLElement>("[data-synthesize-panel='connect']").hidden).toBe(false);
    expect(query<HTMLElement>("[data-synthesize-panel='balanced']").hidden).toBe(true);
  });

  it("ignores a suggestion response that a later request for the same mode superseded", async () => {
    let releaseFirst: (value: Response) => void = () => {};
    const first = new Promise<Response>((resolve) => {
      releaseFirst = resolve;
    });
    let balancedCalls = 0;
    const fetcher = routedFetch({
      related: (url) => {
        const mode = url.searchParams.get("mode") || "balanced";
        if (mode !== "balanced") {
          return jsonResponse(relatedPayload(mode, ["other"]));
        }
        balancedCalls += 1;
        return balancedCalls === 1 ? first : jsonResponse(relatedPayload("balanced", ["second"]));
      },
    });
    mount();
    initSynthesize({ fetch: fetcher, storage: null, clipboard: null });

    query<HTMLButtonElement>("[data-synthesize-tab='rediscover']").click();
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='other']")).not.toBeNull();
    });
    query<HTMLButtonElement>("[data-synthesize-tab='balanced']").click();
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='second']")).not.toBeNull();
    });

    expect(balancedCalls).toBe(2);
    releaseFirst(jsonResponse(relatedPayload("balanced", ["first"])));
    for (let flush = 0; flush < 10; flush += 1) {
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
    expect(document.querySelector("[data-synthesize-add='first']")).toBeNull();
    expect(document.querySelector("[data-synthesize-add='second']")).not.toBeNull();
  });
});

describe("synthesis source curation", () => {
  const withSuggestions = async (): Promise<ReturnType<typeof vi.fn>> => {
    const fetcher = routedFetch({
      related: (url) =>
        jsonResponse(
          relatedPayload(url.searchParams.get("mode") || "balanced", ["one", "two", "three", "four"])
        ),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    return fetcher;
  };

  it("adds, refuses a duplicate, and stops at the configured maximum", async () => {
    await withSuggestions();
    addCandidate("one");
    expect(selectedKeys()).toEqual(["anchor", "one"]);
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(true);
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").textContent).toBe("Selected");

    addCandidate("two");
    addCandidate("three");
    addCandidate("four");
    expect(selectedKeys()).toEqual(["anchor", "one", "two", "three"]);
    expect(statusText()).toBe("Only 3 supporting notes can be selected. Remove one first.");
    expect(query<HTMLElement>("[data-synthesize-selection-status]").textContent).toContain(
      "Remove one to add another."
    );
  });

  it("moves a source with the keyboard, announces the position, and keeps focus on the handle", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");

    const handle = query<HTMLButtonElement>("[data-synthesize-handle='two']");
    handle.focus();
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));

    expect(selectedKeys()).toEqual(["anchor", "two", "one"]);
    expect(statusText()).toBe("Moved two title to position 2 of 3.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-handle='two']")
    );
  });

  it("refuses to move the first supporter above the anchor", async () => {
    await withSuggestions();
    addCandidate("one");
    const handle = query<HTMLButtonElement>("[data-synthesize-handle='one']");
    handle.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    expect(selectedKeys()).toEqual(["anchor", "one"]);
    expect(statusText()).toBe("one title is already at the edge of the order.");
  });

  it("commits a drag to the model without ever passing the anchor", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");
    addCandidate("three");
    for (const row of document.querySelectorAll<HTMLElement>("[data-synthesize-source]")) {
      const index = selectedKeys().indexOf(row.getAttribute("data-synthesize-source") || "");
      row.getBoundingClientRect = (): DOMRect =>
        ({ top: index * 20, height: 20 }) as DOMRect;
    }

    dragRow("three", 5);
    expect(selectedKeys()).toEqual(["anchor", "three", "one", "two"]);
    expect(statusText()).toBe("Moved three title to position 2 of 4.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-handle='three']")
    );
  });

  it("removes a source and moves focus to the row that took its place", async () => {
    await withSuggestions();
    addCandidate("one");
    addCandidate("two");
    query<HTMLButtonElement>("[data-synthesize-remove='one']").click();

    expect(selectedKeys()).toEqual(["anchor", "two"]);
    expect(statusText()).toBe("Removed one title.");
    expect(document.activeElement).toBe(
      query<HTMLButtonElement>("[data-synthesize-remove='two']")
    );
    expect(query<HTMLButtonElement>("[data-synthesize-add='one']").disabled).toBe(false);
  });
});

describe("synthesis vault search", () => {
  it("adds a searched note using its vault key rather than its route", async () => {
    const fetcher = routedFetch({
      search: (url) =>
        url.searchParams.get("q") === "topic"
          ? jsonResponse([
              { key: "Folder/found", path: "/n/Folder/found", title: "Found", snippet: "hit" },
            ])
          : jsonResponse([]),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });

    query<HTMLInputElement>("[data-synthesize-search-input]").value = "topic";
    query<HTMLFormElement>("[data-synthesize-search-form]").dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true })
    );
    await vi.waitFor(() => {
      expect(document.querySelector("[data-synthesize-add='Folder/found']")).not.toBeNull();
    });
    expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
      "1 match for topic."
    );

    addCandidate("Folder/found");
    expect(selectedKeys()).toEqual(["anchor", "Folder/found"]);
    expect(
      query<HTMLElement>("[data-synthesize-source='Folder/found'] .synthesize-source-path")
        .textContent
    ).toBe("Folder/found");
  });

  it("reports an empty search and a failed search", async () => {
    const fetcher = routedFetch({
      search: (url) =>
        url.searchParams.get("q") === "boom"
          ? new Response("search error", { status: 500 })
          : jsonResponse([]),
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    const form = query<HTMLFormElement>("[data-synthesize-search-form]");
    const input = query<HTMLInputElement>("[data-synthesize-search-input]");

    input.value = "nothing";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
        "No notes matched that search."
      );
    });

    input.value = "boom";
    form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
        "search error"
      );
    });
  });

  it("names the Search control when a failed search carries no reason", async () => {
    const fetcher = routedFetch({
      search: () => {
        throw new Error("");
      },
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null });
    query<HTMLInputElement>("[data-synthesize-search-input]").value = "topic";
    query<HTMLFormElement>("[data-synthesize-search-form]").dispatchEvent(
      new Event("submit", { bubbles: true, cancelable: true })
    );
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-search-status]").textContent).toBe(
        "Couldn't load search results. Check your connection, then select Search."
      );
    });
  });
});
