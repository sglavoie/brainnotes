// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import {
  chainPayload,
  clusterPayload,
  control,
  displayParam,
  flush,
  graphRequests,
  mountCount,
  moveSlider,
  resetGraphTest,
  startGraph,
  statusText,
  tapNode,
} from "./graph-controller.test-helpers";

afterEach(resetGraphTest);

describe("graph thinning controls", () => {
  it("round-trips both params into the URL and drops each at its off value", async () => {
    vi.useFakeTimers();
    const fetchMock = startGraph("/graph", chainPayload);
    await vi.advanceTimersByTimeAsync(0);
    expect(graphRequests(fetchMock)).toHaveLength(1);

    // Off is the default, so a clean view keeps a clean URL.
    expect(displayParam("g_mindeg")).toBeNull();
    expect(displayParam("g_collapse")).toBeNull();
    expect(control("graph-mindegree-value").textContent).toBe("off");
    expect(control("graph-collapse-value").textContent).toBe("off");

    moveSlider("graph-mindegree-input", "1");
    moveSlider("graph-collapse-input", "1");
    expect(displayParam("g_mindeg")).toBe("1");
    expect(displayParam("g_collapse")).toBe("1");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 1");
    expect(control("graph-collapse-value").textContent).toBe("depth 1");

    await vi.advanceTimersByTimeAsync(150);
    // delta has no edges, so the degree floor takes it; the three survivors all
    // sit in Notes and fold into one aggregate.
    expect(statusText()).toContain(
      "Showing 1 of 4 nodes — degree ≥ 1, folders collapsed to depth 1."
    );

    moveSlider("graph-mindegree-input", "0");
    moveSlider("graph-collapse-input", "0");
    await vi.advanceTimersByTimeAsync(150);
    expect(displayParam("g_mindeg")).toBeNull();
    expect(displayParam("g_collapse")).toBeNull();
    expect(statusText()).not.toContain("degree ≥");
    expect(statusText()).not.toContain("collapsed");

    // Not one of those moves may cost a request: this is display state.
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("reproduces every knob from the URL and names each reduction in one line", async () => {
    const fetchMock = startGraph("/graph?g_isolate=hub&g_mindeg=2&g_collapse=1", clusterPayload);
    await flush();

    expect(graphRequests(fetchMock)).toEqual(["/api/graph.json"]);
    expect(control<HTMLInputElement>("graph-mindegree-input").value).toBe("2");
    expect(control("graph-mindegree-value").textContent).toBe("≥ 2");
    expect(control<HTMLInputElement>("graph-collapse-input").value).toBe("1");
    expect(control("graph-collapse-value").textContent).toBe("depth 1");
    expect(statusText()).toContain(
      'Showing 1 of 6 nodes — isolated 1 hop from "Hub", degree ≥ 2, folders collapsed to depth 1.'
    );
  });

  it("clears expanded folders when the collapse depth changes", async () => {
    vi.useFakeTimers();
    startGraph("/graph", chainPayload);
    await vi.advanceTimersByTimeAsync(0);

    moveSlider("graph-collapse-input", "1");
    await vi.advanceTimersByTimeAsync(150);
    expect(statusText()).toContain("folders collapsed to depth 1.");

    // Tapping an aggregate expands it rather than selecting it.
    tapNode("folder:Notes");
    expect(statusText()).not.toContain("collapsed");

    // Expansion keys name the first N path segments, so they mean something
    // different at depth 2 — the set has to start empty again.
    moveSlider("graph-collapse-input", "2");
    await vi.advanceTimersByTimeAsync(150);
    expect(statusText()).toContain("folders collapsed to depth 2.");
  });

  it("coalesces a slider sweep into a single rebuild", async () => {
    vi.useFakeTimers();
    startGraph("/graph", clusterPayload);
    await vi.advanceTimersByTimeAsync(0);
    // The initial load mounted the graph once.
    expect(mountCount()).toBe(1);

    for (const step of ["1", "2", "3", "4"]) {
      moveSlider("graph-mindegree-input", step);
      await vi.advanceTimersByTimeAsync(20);
    }
    // Four steps inside the window, none of them a rebuild yet.
    expect(mountCount()).toBe(1);
    // The URL still tracks the slider live, so the shareable state is never stale.
    expect(displayParam("g_mindeg")).toBe("4");

    await vi.advanceTimersByTimeAsync(150);
    expect(mountCount()).toBe(2);
  });
});
