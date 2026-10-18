// @vitest-environment jsdom

import { afterEach, expect, it, vi } from "vitest";

import {
  chainPayload,
  control,
  detailsTitle,
  displayParam,
  fireNodeEvent,
  flush,
  graphRequests,
  mountCount,
  mountedEdgeIDs,
  mountedNodeIDs,
  moveSlider,
  pressCanvasKey,
  resetGraphTest,
  setStaleCytoscapeSelectionID,
  startGraph,
  statusText,
  tapNode,
} from "./graph-controller.test-helpers";

afterEach(resetGraphTest);

// What the display pipeline excludes must leave Cytoscape, not merely stop being
// mentioned in the status line: an element that is still mounted is still
// tappable, hoverable and reachable from the keyboard.
  it("mounts only the anchor neighborhood while isolated, and the whole source again after", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();

    // The fetched graph, mounted whole: alpha - beta - gamma, with delta adrift.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta", "gamma", "delta"]);
    expect(mountedEdgeIDs()).toEqual(["e1", "e2"]);

    tapNode("alpha");
    control<HTMLButtonElement>("graph-isolate-button").click();

    // One hop from alpha: the anchor and beta. gamma is two hops out and delta is
    // unreachable, so e2 loses an endpoint and goes with them.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(mountedEdgeIDs()).toEqual(["e1"]);

    control<HTMLButtonElement>("graph-isolate-clear-button").click();

    // "Show whole graph" is a remount of the fetched set, not an unhide — and it
    // comes from the payload already in hand, so restoring costs no request.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta", "gamma", "delta"]);
    expect(mountedEdgeIDs()).toEqual(["e1", "e2"]);
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("ignores late node events for an element isolation removed", async () => {
    const fetchMock = startGraph("/graph", chainPayload);
    await flush();

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    const canvas = control("graph-canvas");
    const openNote = control<HTMLAnchorElement>("graph-open-note");

    tapNode("alpha");
    isolateButton.click();
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(detailsTitle()).toBe("Alpha");
    // The anchor is the selection, so isolating it again is a no-op the control
    // already refuses.
    expect(isolateButton.disabled).toBe(true);
    const anchorHref = openNote.getAttribute("href");
    const mountsBefore = mountCount();

    // gamma left the graph with the rebuild; Cytoscape can still deliver a queued
    // gesture or a pointer event that named it.
    tapNode("gamma");
    fireNodeEvent("select", "gamma");
    fireNodeEvent("mouseover", "gamma");

    // Selection, details, and the display controls all still describe the anchor.
    expect(detailsTitle()).toBe("Alpha");
    expect(openNote.getAttribute("href")).toBe(anchorHref);
    expect(isolateButton.disabled).toBe(true);
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');

    // The hidden node cannot be acted on either: the isolate control stays dead,
    // so clicking it cannot re-anchor the view onto gamma.
    isolateButton.click();
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(mountCount()).toBe(mountsBefore);

    // None of it touched the network, either.
    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  // A mouseout is delivered after the pointer has already left, so it is the one
  // node event that routinely arrives *late* — after a rebuild removed its node,
  // or after the hover has already moved on to another node.
  it("ignores a late mouseout for an element isolation removed", async () => {
    const fetchMock = startGraph("/graph?g_isolate=alpha", chainPayload);
    await flush();

    const canvas = control("graph-canvas");
    // Isolated from the URL, so nothing is selected and the panel is free to
    // report the hover.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);

    fireNodeEvent("mouseover", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    // gamma left the graph with the rebuild; its queued mouseout names a node
    // the user cannot see and must not end beta's hover.
    fireNodeEvent("mouseout", "gamma");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    // The node that owns the hover still ends it.
    fireNodeEvent("mouseout", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(detailsTitle()).toBe("Choose a note");

    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  it("does not let an older mouseout clear a newer hover on another node", async () => {
    startGraph("/graph", chainPayload);
    await flush();

    const canvas = control("graph-canvas");

    // The pointer crosses alpha and lands on beta before alpha's mouseout is
    // delivered — both nodes are displayed, so the displayed-set guard alone
    // would let the stale event through.
    fireNodeEvent("mouseover", "alpha");
    fireNodeEvent("mouseover", "beta");
    expect(detailsTitle()).toBe("Beta");

    fireNodeEvent("mouseout", "alpha");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Beta");

    fireNodeEvent("mouseout", "beta");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);
    expect(detailsTitle()).toBe("Choose a note");
  });

  // A rebuild can take the node under the pointer with it. The hover state goes
  // with the node and the canvas cursor has to go with the hover: the excluded
  // node's late mouseout is refused by the displayed-set guard, so it is not
  // coming back to clean the class up.
  it("clears hover cursor when a remount excludes the hovered node", async () => {
    vi.useFakeTimers();
    const fetchMock = startGraph("/graph", chainPayload);
    await vi.advanceTimersByTimeAsync(0);

    const canvas = control("graph-canvas");

    // Nothing is selected, so the details panel reports the hover alone.
    fireNodeEvent("mouseover", "gamma");
    expect(canvas.classList.contains("is-node-hovered")).toBe(true);
    expect(detailsTitle()).toBe("Gamma");

    // A degree floor of 2 keeps only beta, so gamma leaves the graph while the
    // pointer is still on it.
    moveSlider("graph-mindegree-input", "2");
    await vi.advanceTimersByTimeAsync(150);
    expect(mountedNodeIDs()).toEqual(["beta"]);

    expect(detailsTitle()).toBe("Choose a note");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);

    // gamma's mouseout lands afterwards, naming a node the user can no longer
    // see: it is refused, and the cursor it would have cleared is already gone.
    fireNodeEvent("mouseout", "gamma");
    expect(detailsTitle()).toBe("Choose a note");
    expect(canvas.classList.contains("is-node-hovered")).toBe(false);

    // Display state throughout: the exclusion cost no request.
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  // Keyboard activation is the one interactive path that does not start from a
  // node event: with no hover and no controller selection it asks Cytoscape which
  // node is selected. A remount clears the controller's selection but the
  // renderer can still be holding a selection flag on an element the display
  // pipeline removed, so that answer has to be checked against the displayed set
  // like every id arriving from Cytoscape.
  it("refuses a keyboard activation of a stale Cytoscape selection outside the displayed set", async () => {
    const fetchMock = startGraph("/graph?g_isolate=alpha", chainPayload);
    await flush();

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    const openNote = control<HTMLAnchorElement>("graph-open-note");

    // Isolated from the URL: gamma is two hops out and never reached Cytoscape,
    // and with nothing hovered or selected the details panel is at rest.
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(mountedEdgeIDs()).toEqual(["e1"]);
    expect(detailsTitle()).toBe("Choose a note");
    expect(isolateButton.disabled).toBe(true);
    const mountsBefore = mountCount();

    setStaleCytoscapeSelectionID("gamma");

    // The key is left unclaimed — the controller never calls preventDefault for a
    // target it refuses — and nothing else moves either.
    expect(pressCanvasKey("Enter")).toBe(false);
    expect(pressCanvasKey(" ")).toBe(false);

    expect(detailsTitle()).toBe("Choose a note");
    expect(openNote.getAttribute("aria-disabled")).toBe("true");
    expect(openNote.hasAttribute("href")).toBe(false);
    expect(isolateButton.disabled).toBe(true);
    expect(displayParam("g_isolate")).toBe("alpha");
    expect(statusText()).toContain('Showing 2 of 4 nodes — isolated 1 hop from "Alpha"');
    expect(mountedNodeIDs()).toEqual(["alpha", "beta"]);
    expect(mountedEdgeIDs()).toEqual(["e1"]);
    expect(mountCount()).toBe(mountsBefore);

    await flush();
    expect(graphRequests(fetchMock)).toHaveLength(1);
  });

  // The other half of the same guard: without this, the refusal above would pass
  // just as well if keyboard activation had quietly stopped working at all.
  it("still activates a Cytoscape selection the display pipeline kept", async () => {
    startGraph("/graph?g_isolate=alpha", chainPayload);
    await flush();

    const isolateButton = control<HTMLButtonElement>("graph-isolate-button");
    expect(detailsTitle()).toBe("Choose a note");

    setStaleCytoscapeSelectionID("beta");

    expect(pressCanvasKey("Enter")).toBe(true);
    expect(detailsTitle()).toBe("Beta");
    // A real selection, not just a details repaint: the isolate control wakes up
    // because there is now a note to re-anchor on.
    expect(isolateButton.disabled).toBe(false);
  });
