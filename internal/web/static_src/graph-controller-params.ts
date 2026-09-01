import { updateBrowserQuery } from "./filters";
import { normalizeSearchMode } from "./graph-controller-labels";
import { type GraphSearchMode } from "./graph";

/**
 * The facet mode this page publishes and listens for. It matches
 * `graphFacetControlsData().Mode` on the server, so the graph's own facet chips
 * and its own controls speak the same mode; the query bar publishes `mode: ""`,
 * which reaches every mode.
 */
export const graphFilterMode = "graph";

/** Debounce for a keystroke in the graph's free-text filter. */
export const graphFilterDebounceMs = 150;

export const readPageSearchMode = (): GraphSearchMode =>
  normalizeSearchMode(new URLSearchParams(window.location.search).get("q_mode"));

export const syncPageSearchModeParam = (mode: GraphSearchMode): void => {
  const params = new URLSearchParams(window.location.search);
  if (mode === "focus") {
    params.set("q_mode", "focus");
  } else {
    params.delete("q_mode");
  }
  updateBrowserQuery(params);
};

/** Slider bound of the min-degree control. The pipeline itself has no upper clamp. */
const maxMinDegree = 10;

/** Folder-path depth bound of the collapse control, matching the pipeline's clamp. */
const maxCollapseDepth = 3;

const clampSliderInteger = (value: number, min: number, max: number): number =>
  Number.isFinite(value) ? Math.min(max, Math.max(min, Math.round(value))) : min;

export const clampMinDegree = (value: number): number =>
  clampSliderInteger(value, 0, maxMinDegree);

export const clampCollapseDepth = (value: number): number =>
  clampSliderInteger(value, 0, maxCollapseDepth);

/** The full display-knob set the URL carries. */
export type GraphDisplayParams = {
  isolateID: string;
  minDegree: number;
  collapseDepth: number;
};

/** The isolation radius parameter this page used to carry, now retired. */
const legacyHopsParamKey = "g_hops";

// `g_isolate` / `g_mindeg` / `g_collapse` are display-only params, read and
// written exactly like `q_mode` above: never through `publishFilterChange`, and
// never copied by `buildFilteredAPIURL`, so moving them cannot refetch the
// graph.
export const readPageDisplayParams = (): GraphDisplayParams => {
  const params = new URLSearchParams(window.location.search);
  const readInt = (key: string): number => Number.parseInt(params.get(key) || "", 10);
  return {
    isolateID: params.get("g_isolate") || "",
    minDegree: clampMinDegree(readInt("g_mindeg")),
    collapseDepth: clampCollapseDepth(readInt("g_collapse")),
  };
};

/**
 * Drops the retired isolation-radius parameter from a legacy link.
 *
 * Isolation is fixed at one hop, so a shared URL carrying a radius describes a
 * view this page can no longer render. Deleting it before the first load — not
 * merely ignoring it — is what keeps the address bar an honest description of
 * what is on screen, and `URLSearchParams.delete` removes every value under the
 * key, so a doubled `g_hops=2&g_hops=3` leaves nothing behind. Everything else
 * survives: `updateBrowserQuery` rebuilds the URL from the current pathname, the
 * remaining parameters, and the current hash.
 */
export const canonicalizeLegacyDisplayParams = (): void => {
  const params = new URLSearchParams(window.location.search);
  if (!params.has(legacyHopsParamKey)) {
    return;
  }
  params.delete(legacyHopsParamKey);
  updateBrowserQuery(params);
};

/**
 * Writes the whole display-knob set into the URL at once.
 *
 * Every knob is omitted at its off value, so a default view keeps a clean,
 * shareable URL and only the knobs the user actually moved show up in it.
 */
export const syncPageDisplayParams = (display: GraphDisplayParams): void => {
  const params = new URLSearchParams(window.location.search);
  const write = (key: string, value: string, offValue: string): void => {
    if (value === offValue) {
      params.delete(key);
      return;
    }
    params.set(key, value);
  };
  write("g_isolate", display.isolateID, "");
  write("g_mindeg", String(display.minDegree), "0");
  write("g_collapse", String(display.collapseDepth), "0");
  // Defensive, not merely tidy: this is the one writer of the display knobs, so
  // deleting the retired radius here means no path can put it back into a URL
  // the user is about to share.
  params.delete(legacyHopsParamKey);
  updateBrowserQuery(params);
};

export const formatMinDegreeLabel = (minDegree: number): string =>
  minDegree <= 0 ? "off" : `≥ ${minDegree}`;

export const formatCollapseDepthLabel = (depth: number): string =>
  depth <= 0 ? "off" : `depth ${depth}`;
