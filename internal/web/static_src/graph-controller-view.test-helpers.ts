import {
  defaultGraphDisplayOptions,
  type GraphDisplayOptions,
} from "./graph-controller-view";
import {
  nodeSizeForDegree,
  type GraphElements,
  type NormalizedGraphEdge,
  type NormalizedGraphNode,
} from "./graph";

/**
 * Shared fixtures for the display-pipeline tests, following the same
 * `<cluster>.test-helpers.ts` convention as daily-fixes and synthesize.
 *
 * `buildDisplayGraph` is pure, so there is no state to reset here — only the
 * builders and readers the stage-by-stage files share.
 */

export const makeNode = (
  id: string,
  overrides: Partial<NormalizedGraphNode> = {}
): NormalizedGraphNode => ({
  id,
  title: id,
  folder: "",
  color: "#7aa2ff",
  inDegree: 0,
  outDegree: 0,
  degree: 0,
  filteredInDegree: 0,
  filteredOutDegree: 0,
  filteredDegree: 0,
  hasBroken: false,
  brokenCount: 0,
  isOrphan: false,
  isDeadEnd: false,
  matchesQuery: false,
  size: nodeSizeForDegree(0),
  ...overrides,
});

export const makeEdge = (id: string, source: string, target: string): NormalizedGraphEdge => ({
  id,
  source,
  target,
  mutual: false,
});

export const withOptions = (
  overrides: Partial<GraphDisplayOptions> = {}
): GraphDisplayOptions => ({
  ...defaultGraphDisplayOptions(),
  ...overrides,
});

export const nodeIDs = (elements: GraphElements): string[] =>
  elements.nodes.map((node) => node.id);

export const edgeIDs = (elements: GraphElements): string[] =>
  elements.edges.map((edge) => edge.id);

export const edgePairs = (elements: GraphElements): string[] =>
  elements.edges.map((edge) => `${edge.source}->${edge.target}${edge.mutual ? "*" : ""}`);

// a -> b -> c -> d, plus a disconnected island e -> f.
export const chainGraph = (): GraphElements => ({
  nodes: [
    makeNode("a"),
    makeNode("b"),
    makeNode("c"),
    makeNode("d"),
    makeNode("e"),
    makeNode("f"),
  ],
  edges: [
    makeEdge("ab", "a", "b"),
    makeEdge("bc", "b", "c"),
    makeEdge("cd", "c", "d"),
    makeEdge("ef", "e", "f"),
  ],
});
