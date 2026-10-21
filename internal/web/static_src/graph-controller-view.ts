// The graph display pipeline: a pure transform from the normalized full-vault graph to
// the subset that actually gets drawn. No DOM, no Cytoscape — the controller (Session 5)
// owns both. Stages apply in one fixed order (isolate -> min-degree -> collapse) so the
// knobs compose predictably regardless of the order the user touches them in.

import {
  colorForFolder,
  computeNormalizedGraphDegrees,
  mergeReciprocalGraphEdges,
  nodeSizeForDegree,
  type GraphElements,
  type NormalizedGraphEdge,
  type NormalizedGraphNode,
} from "./graph";

export type GraphDisplayOptions = {
  isolateID: string; // "" = off — when set, the anchor plus exactly its immediate neighbors
  minDegree: number; // 0 = off
  collapseDepth: number; // 0 = off, else 1..3 folder path segments
  expandedFolders: ReadonlySet<string>;
  keepIDs: ReadonlySet<string>; // selected node, isolate anchor — never dropped
};

export type GraphDisplayStats = {
  sourceNodeCount: number;
  displayedNodeCount: number;
  aggregateCount: number;
  droppedByDegree: number;
  isolated: boolean;
};

// An aggregate is identified by the explicit isAggregate flag on the node model, not by
// this prefix: a note key may legitimately start with "folder:". The prefix and its
// predicate exist only so the controller can build and recognize the synthetic ids it
// puts into URL params and expanded-folder sets.
export const aggregateNodeIDPrefix = "folder:";

export const isAggregateNodeID = (id: string): boolean => id.startsWith(aggregateNodeIDPrefix);

export const aggregateNodeIDForFolder = (folder: string): string =>
  `${aggregateNodeIDPrefix}${folder}`;

export const defaultGraphDisplayOptions = (): GraphDisplayOptions => ({
  isolateID: "",
  minDegree: 0,
  collapseDepth: 0,
  expandedFolders: new Set<string>(),
  keepIDs: new Set<string>(),
});

const maxCollapseDepth = 3;

const clampInteger = (value: number, min: number, max: number): number => {
  if (!Number.isFinite(value)) {
    return min;
  }
  return Math.min(max, Math.max(min, Math.round(value)));
};

export const folderGroupKey = (folder: string, depth: number): string => {
  if (depth <= 0) {
    return "";
  }
  const segments = folder.split("/").filter((segment) => segment !== "");
  if (segments.length === 0) {
    return "";
  }
  return segments.slice(0, depth).join("/");
};

const undirectedNeighbors = (edges: readonly NormalizedGraphEdge[]): Map<string, string[]> => {
  const neighbors = new Map<string, string[]>();
  const push = (from: string, to: string): void => {
    const existing = neighbors.get(from);
    if (existing) {
      existing.push(to);
      return;
    }
    neighbors.set(from, [to]);
  };
  for (const edge of edges) {
    if (edge.source === "" || edge.target === "" || edge.source === edge.target) {
      continue;
    }
    push(edge.source, edge.target);
    push(edge.target, edge.source);
  }
  return neighbors;
};

// Exactly one undirected step out of the anchor: the selected note and the notes it is
// immediately linked to, in either direction, and nothing further. The radius is fixed
// rather than configurable — "the selection" means that neighborhood — so there is no
// traversal here to bound. An anchor missing from the source is a no-op rather than an
// empty graph: a query change can filter the anchor away while the isolate param is still
// in the URL.
const isolateStage = (
  elements: GraphElements,
  isolateID: string
): { elements: GraphElements; isolated: boolean } => {
  if (isolateID === "" || !elements.nodes.some((node) => node.id === isolateID)) {
    return { elements, isolated: false };
  }

  const neighbors = undirectedNeighbors(elements.edges);
  const reached = new Set<string>([isolateID, ...(neighbors.get(isolateID) || [])]);

  const nodes = elements.nodes.filter((node) => reached.has(node.id));
  const kept = new Set(nodes.map((node) => node.id));
  const edges = elements.edges.filter((edge) => kept.has(edge.source) && kept.has(edge.target));
  return { elements: { nodes, edges }, isolated: true };
};

// One pass over degrees computed within the current subset — deliberately not iterated
// to a fixed point, so dragging the slider gives a stable, predictable result instead of
// cascading the graph away. keepIDs members are exempt.
const minDegreeStage = (
  elements: GraphElements,
  minDegree: number,
  keepIDs: ReadonlySet<string>
): { elements: GraphElements; droppedByDegree: number } => {
  if (minDegree <= 0) {
    return { elements, droppedByDegree: 0 };
  }

  const degrees = computeNormalizedGraphDegrees(elements.nodes, elements.edges);
  const nodes = elements.nodes.filter((node) => {
    if (keepIDs.has(node.id)) {
      return true;
    }
    return (degrees.get(node.id)?.degree || 0) >= minDegree;
  });
  const kept = new Set(nodes.map((node) => node.id));
  const edges = elements.edges.filter((edge) => kept.has(edge.source) && kept.has(edge.target));
  return {
    elements: { nodes, edges },
    droppedByDegree: elements.nodes.length - nodes.length,
  };
};

const aggregateNode = (folder: string, members: NormalizedGraphNode[]): NormalizedGraphNode => ({
  id: aggregateNodeIDForFolder(folder),
  title: `${folder} (${members.length})`,
  folder,
  color: colorForFolder(folder),
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
  // Not an issue flag: an aggregate hiding a match must not be dimmed in focus mode.
  matchesQuery: members.some((member) => member.matchesQuery),
  isAggregate: true,
  memberIDs: members.map((member) => member.id),
});

const collapseNodes = (
  sourceNodes: readonly NormalizedGraphNode[],
  aggregateByMemberID: ReadonlyMap<string, NormalizedGraphNode>
): { nodes: NormalizedGraphNode[]; aggregateCount: number } => {
  const nodes: NormalizedGraphNode[] = [];
  const emitted = new Set<string>();
  for (const node of sourceNodes) {
    const aggregate = aggregateByMemberID.get(node.id);
    if (!aggregate) {
      nodes.push(node);
      continue;
    }
    if (!emitted.has(aggregate.id)) {
      emitted.add(aggregate.id);
      nodes.push(aggregate);
    }
  }
  return { nodes, aggregateCount: emitted.size };
};

const collapseEdges = (
  sourceEdges: readonly NormalizedGraphEdge[],
  aggregateByMemberID: ReadonlyMap<string, NormalizedGraphNode>
): NormalizedGraphEdge[] => {
  const remap = (id: string): string => aggregateByMemberID.get(id)?.id || id;
  const edges: NormalizedGraphEdge[] = [];
  const indexByPair = new Map<string, number>();
  for (const edge of sourceEdges) {
    const source = remap(edge.source);
    const target = remap(edge.target);
    // Intra-group edges become self-edges before displayed degrees are computed.
    if (source === target) {
      continue;
    }
    const pairKey = `${source}\u0000${target}`;
    const existingIndex = indexByPair.get(pairKey);
    if (existingIndex !== undefined) {
      // First-seen keeps its id, while any reciprocal edge makes the pair mutual.
      if (edge.mutual && !edges[existingIndex].mutual) {
        edges[existingIndex] = { ...edges[existingIndex], mutual: true };
      }
      continue;
    }
    indexByPair.set(pairKey, edges.length);
    edges.push({ ...edge, source, target });
  }
  return edges;
};

// Groups surviving nodes by the first collapseDepth segments of their folder. A group of
// more than one member that is not expanded becomes a single aggregate node placed at the
// list position of its first member, which keeps the output ordering deterministic.
// Empty-folder nodes and single-member groups pass through untouched.
const collapseStage = (
  elements: GraphElements,
  collapseDepth: number,
  expandedFolders: ReadonlySet<string>
): { elements: GraphElements; aggregateCount: number } => {
  if (collapseDepth <= 0) {
    return { elements, aggregateCount: 0 };
  }

  const membersByFolder = new Map<string, NormalizedGraphNode[]>();
  for (const node of elements.nodes) {
    const folder = folderGroupKey(node.folder, collapseDepth);
    if (folder === "" || expandedFolders.has(folder)) {
      continue;
    }
    const existing = membersByFolder.get(folder);
    if (existing) {
      existing.push(node);
      continue;
    }
    membersByFolder.set(folder, [node]);
  }

  const aggregateByMemberID = new Map<string, NormalizedGraphNode>();
  for (const [folder, members] of membersByFolder) {
    if (members.length < 2) {
      continue;
    }
    const aggregate = aggregateNode(folder, members);
    for (const member of members) {
      aggregateByMemberID.set(member.id, aggregate);
    }
  }

  if (aggregateByMemberID.size === 0) {
    return { elements, aggregateCount: 0 };
  }

  const { nodes, aggregateCount } = collapseNodes(elements.nodes, aggregateByMemberID);
  const edges = collapseEdges(elements.edges, aggregateByMemberID);

  return { elements: { nodes, edges }, aggregateCount };
};

// Degrees are recomputed from the displayed edge set *before* the reciprocal merge, for
// the same reason normalizeGraphPayload does: the merge halves each reciprocal pair, so
// counting after it would undercount every mutual link.
const withRecomputedDegrees = (elements: GraphElements): GraphElements => {
  const degrees = computeNormalizedGraphDegrees(elements.nodes, elements.edges);
  const nodes = elements.nodes.map((node) => {
    const summary = degrees.get(node.id) || { inDegree: 0, outDegree: 0, degree: 0 };
    const next: NormalizedGraphNode = {
      ...node,
      filteredInDegree: summary.inDegree,
      filteredOutDegree: summary.outDegree,
      filteredDegree: summary.degree,
      // An aggregate is sized by how many notes it stands for, not by its handful of
      // surviving inter-folder edges — a 40-note folder must not render smaller than a
      // 5-link note. Every other node keeps the degree-driven sizing.
      size: nodeSizeForDegree(
        node.isAggregate && node.memberIDs ? node.memberIDs.length : summary.degree
      ),
    };
    if (next.isAggregate) {
      // A synthetic node has no source-payload degree; mirroring the displayed counts
      // keeps the details panel from reading a bogus 0 next to a non-zero filtered value.
      next.inDegree = summary.inDegree;
      next.outDegree = summary.outDegree;
      next.degree = summary.degree;
    }
    return next;
  });
  return { nodes, edges: elements.edges };
};

export const buildDisplayGraph = (
  source: GraphElements,
  options: GraphDisplayOptions
): { elements: GraphElements; stats: GraphDisplayStats } => {
  const minDegree = Math.max(0, clampInteger(options.minDegree, 0, Number.MAX_SAFE_INTEGER));
  const collapseDepth = clampInteger(options.collapseDepth, 0, maxCollapseDepth);

  const isolate = isolateStage({ nodes: source.nodes, edges: source.edges }, options.isolateID);
  const thinned = minDegreeStage(isolate.elements, minDegree, options.keepIDs);
  const collapsed = collapseStage(thinned.elements, collapseDepth, options.expandedFolders);
  const sized = withRecomputedDegrees(collapsed.elements);
  const elements: GraphElements = {
    nodes: sized.nodes,
    // Collapse can turn two one-directional note links into a reciprocal pair between
    // aggregates, so the Session 2 merge re-runs over the remapped edge set.
    edges: mergeReciprocalGraphEdges(sized.edges),
  };

  return {
    elements,
    stats: {
      sourceNodeCount: source.nodes.length,
      displayedNodeCount: elements.nodes.length,
      aggregateCount: collapsed.aggregateCount,
      droppedByDegree: thinned.droppedByDegree,
      isolated: isolate.isolated,
    },
  };
};
