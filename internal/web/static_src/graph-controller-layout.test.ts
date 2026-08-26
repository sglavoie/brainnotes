// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

/**
 * fcose registration is module state (it is global to the Cytoscape factory),
 * so every case loads a fresh copy of the module rather than sharing one across
 * tests. Without this, the first successful registration would leak into every
 * later assertion about the fallback path.
 */
const freshLayoutModule = async () => {
  vi.resetModules();
  return import("./graph-controller-layout");
};

afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
});

/** The jsdom stand-in from graph-controller.test.ts: a bare function, no `.use`. */
const bareCytoscapeFake = () => ({});

describe("registerGraphLayouts", () => {
  it("no-ops when the fcose bundle never loaded", async () => {
    const use = vi.fn();
    const factory = Object.assign(() => ({}), { use });
    vi.stubGlobal("cytoscape", factory);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    expect(use).not.toHaveBeenCalled();
    expect(layout.hasFcose()).toBe(false);
  });

  it("no-ops on a Cytoscape fake that has no use()", async () => {
    vi.stubGlobal("cytoscape", bareCytoscapeFake);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    expect(() => layout.registerGraphLayouts()).not.toThrow();
    expect(layout.hasFcose()).toBe(false);
  });

  it("registers exactly once when both globals are present", async () => {
    const use = vi.fn();
    const factory = Object.assign(() => ({}), { use });
    const extension = () => {};
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", extension);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();
    layout.registerGraphLayouts();

    expect(use).toHaveBeenCalledTimes(1);
    expect(use).toHaveBeenCalledWith(extension);
    expect(layout.hasFcose()).toBe(true);
  });
});

describe("layoutOptionsForSpread", () => {
  it("falls back to cose when fcose is absent", async () => {
    vi.stubGlobal("cytoscape", bareCytoscapeFake);

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    expect(layout.layoutOptionsForSpread(2).name).toBe("cose");
  });

  it("selects fcose once registration succeeded", async () => {
    const factory = Object.assign(() => ({}), { use: vi.fn() });
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();

    const options = layout.layoutOptionsForSpread(2) as unknown as Record<string, unknown>;
    expect(options.name).toBe("fcose");
    expect(options.randomize).toBe(false);
    expect(options.nodeDimensionsIncludeLabels).toBe(true);
    expect(options.packComponents).toBe(true);
    expect(options.tile).toBe(true);
  });

  it("keeps labels reserved on the cose fallback too", async () => {
    const layout = await freshLayoutModule();
    expect(layout.coseLayoutOptionsForSpread(2).nodeDimensionsIncludeLabels).toBe(true);
  });
});

const spreads = [0.8, 2.0, 6.0];

/** Reads the numeric option `key` at each spread, through the untyped fcose shape. */
const seriesFor = (
  build: (spread: number) => unknown,
  key: string
): number[] =>
  spreads.map((spread) => (build(spread) as Record<string, unknown>)[key] as number);

const expectStrictlyIncreasing = (values: number[]): void => {
  for (let index = 1; index < values.length; index += 1) {
    expect(values[index]).toBeGreaterThan(values[index - 1]);
  }
};

const expectStrictlyDecreasing = (values: number[]): void => {
  for (let index = 1; index < values.length; index += 1) {
    expect(values[index]).toBeLessThan(values[index - 1]);
  }
};

describe("spread scaling", () => {
  it("stays monotonic across 0.8/2.0/6.0 for the cose builder", async () => {
    const layout = await freshLayoutModule();
    const build = layout.coseLayoutOptionsForSpread;

    for (const key of ["idealEdgeLength", "nodeRepulsion", "componentSpacing", "numIter"]) {
      expectStrictlyIncreasing(seriesFor(build, key));
    }
    expectStrictlyDecreasing(seriesFor(build, "gravity"));
  });

  it("stays monotonic across 0.8/2.0/6.0 for the fcose builder", async () => {
    const factory = Object.assign(() => ({}), { use: vi.fn() });
    vi.stubGlobal("cytoscape", factory);
    vi.stubGlobal("cytoscapeFcose", () => {});

    const layout = await freshLayoutModule();
    layout.registerGraphLayouts();
    const build = layout.layoutOptionsForSpread;

    for (const key of ["idealEdgeLength", "nodeRepulsion", "nodeSeparation", "numIter"]) {
      expectStrictlyIncreasing(seriesFor(build, key));
    }
    expectStrictlyDecreasing(seriesFor(build, "gravity"));
  });
});
