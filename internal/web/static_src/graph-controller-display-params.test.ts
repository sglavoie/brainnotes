// @vitest-environment jsdom

import { afterEach, describe, expect, it } from "vitest";

import {
  chainPayload,
  control,
  displayParam,
  flush,
  graphRequests,
  mountedEdgeIDs,
  mountedNodeIDs,
  resetGraphTest,
  startGraph,
  statusText,
  tapNode,
} from "./graph-controller.test-helpers";

afterEach(resetGraphTest);

describe("graph display controls", () => {
  it("isolates the selection through display-only params without refetching", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    const clearButton = control<HTMLButtonElement>("graph-isolate-clear-button");

    // Isolation needs an anchor, so both controls start dead.
    expect(isolateButton.disabled).toBe(true);
    expect(clearButton.disabled).toBe(true);

    tapNode("alpha");
    expect(isolateButton.disabled).toBe(false);

    isolateButton.click();
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(clearButton.disabled).toBe(false);
    // The reduction is stated outright rather than left for the user to notice.
    expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');
    // The radius is not a knob, so isolating writes one display param and no
    // retired companion alongside it.
    expect(displayParam("g_hops")).toBeNull();

    clearButton.click();
    expect(displayParam("g_isolate")).toBeNull();
    expect(clearButton.disabled).toBe(true);
    expect(statusText()).not.toContain("isolated");

    // The whole exchange is display state: not one of those knobs may refetch.
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("reproduces an isolated view from the URL without leaking g_ params into the fetch", async () => {
    const fetchMock = startGraph("/graph?g_isolate=beta", chainPayload);
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(false);
    expect(statusText()).toContain('Showing 3 of 4 nodes — isolated 1 hop from "Beta"');
  });

  it("clears an isolate anchor the loaded graph does not contain, and says so", async () => {
    startGraph("/graph?g_isolate=missing", chainPayload);
    await flush();

    expect(displayParam("g_isolate")).toBeNull();
    expect(control<HTMLButtonElement>("graph-isolate-clear-button").disabled).toBe(true);
    expect(statusText()).toContain('Isolation cleared: "missing" is not in the current graph.');
  });
});

// `g_hops` was the isolation radius before the neighborhood became fixed. Links
// carrying it are still out there — in notes, in chat logs, in browser history —
// and they have to keep working, which means rendering the one-hop view and
// saying so in the address bar rather than half-honouring a radius that no
// longer exists.
describe("graph legacy g_hops URLs", () => {
  for (const legacy of ["2", "3"]) {
    it(`strips g_hops=${legacy} and renders exactly one hop`, async () => {
      const fetchMock = startGraph(`/graph?g_isolate=alpha&g_hops=${legacy}`, chainPayload);
      await flush();

      expect(displayParam("g_hops")).toBeNull();
      expect(displayParam("g_isolate")).toBe("alpha");
      // The value is not merely dropped from the URL, it is dropped from the
      // view: gamma sat inside the old radius and is not mounted.
      expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
      expect(mountedEdgeIDs()).toEqual(["e1"]);
      expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');
      expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    });
  }

  it("keeps the path, the hash and every other parameter while canonicalizing", async () => {
    startGraph("/graph?q=daily&g_hops=2&tag=work&g_mindeg=2#anchor", chainPayload);
    await flush();

    expect(window.location.pathname).toBe("/graph");
    expect(window.location.hash).toBe("#anchor");
    expect(window.location.search).toBe("?q=daily&tag=work&g_mindeg=2");
    // The surviving display knob is still in force, not just still in the URL.
    expect(control<HTMLInputElement>("graph-mindegree-input").value).toBe("2");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 2");
  });

  it("removes every value of a repeated g_hops", async () => {
    startGraph("/graph?g_hops=2&g_isolate=alpha&g_hops=3", chainPayload);
    await flush();

    expect(window.location.search).toBe("?g_isolate=alpha");
  });

  it("keeps display params out of the graph request", async () => {
    const fetchMock = startGraph(
      "/graph?q=daily&g_isolate=alpha&g_hops=3&g_mindeg=1&g_collapse=1",
      chainPayload
    );
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json?q=daily"]);
  });
});
