import { searchResultPath } from "./api-search";
import type { SearchResult } from "./api";
import type { RelatedResult } from "./related-types";
import {
  invalidateSynthesisContext,
  type SynthesisConfig,
  type SynthesisModel,
  type SynthesisSource,
} from "./synthesize-model";

export type SynthesisCandidate = SynthesisSource & {
  detail: string;
};

// ok reports whether the selection changed; message is always user-facing, so a
// refusal explains itself and a success announces the resulting position.
export type SynthesisSelectionOutcome = {
  ok: boolean;
  message: string;
};

export const candidateFromRelatedResult = (result: RelatedResult): SynthesisCandidate => ({
  key: result.candidate.key,
  title: result.candidate.title || result.candidate.path,
  path: result.candidate.path,
  route: result.candidate.route,
  detail: result.excerpt,
});

// A search result reports its note route in path, so the vault-relative location
// comes from the key instead.
export const candidateFromSearchResult = (
  result: SearchResult
): SynthesisCandidate | null => {
  const key = (result.key || "").trim();
  if (key === "") {
    return null;
  }
  return {
    key,
    title: result.title || key,
    path: key,
    route: searchResultPath(result),
    detail: result.snippet,
  };
};

export const addSynthesisSource = (
  model: SynthesisModel,
  config: SynthesisConfig,
  candidate: SynthesisCandidate
): SynthesisSelectionOutcome => {
  if (candidate.key === "") {
    return { ok: false, message: "That note cannot be added as a source." };
  }
  if (candidate.key === config.anchor.key) {
    return { ok: false, message: "The anchor note is already the first source." };
  }
  if (model.sources.some((source) => source.key === candidate.key)) {
    return { ok: false, message: `${candidate.title} is already selected.` };
  }
  if (model.sources.length >= config.maxSupporters) {
    return {
      ok: false,
      message: `Only ${config.maxSupporters} supporting notes can be selected. Remove one first.`,
    };
  }
  model.sources.push({
    key: candidate.key,
    title: candidate.title,
    path: candidate.path,
    route: candidate.route,
  });
  invalidateSynthesisContext(model);
  return {
    ok: true,
    message: `Added ${candidate.title} as source ${model.sources.length + 1} of ${model.sources.length + 1}.`,
  };
};

export const removeSynthesisSource = (
  model: SynthesisModel,
  key: string
): SynthesisSelectionOutcome => {
  const index = model.sources.findIndex((source) => source.key === key);
  if (index < 0) {
    return { ok: false, message: "That note is not selected." };
  }
  const [removed] = model.sources.splice(index, 1);
  invalidateSynthesisContext(model);
  return { ok: true, message: `Removed ${removed.title}.` };
};

export const moveSynthesisSource = (
  model: SynthesisModel,
  key: string,
  direction: -1 | 1
): SynthesisSelectionOutcome => {
  const index = model.sources.findIndex((source) => source.key === key);
  if (index < 0) {
    return { ok: false, message: "That note is not selected." };
  }
  const target = index + direction;
  if (target < 0 || target >= model.sources.length) {
    return { ok: false, message: `${model.sources[index].title} is already at the edge of the order.` };
  }
  const moved = model.sources[index];
  model.sources[index] = model.sources[target];
  model.sources[target] = moved;
  invalidateSynthesisContext(model);
  return {
    ok: true,
    message: `Moved ${moved.title} to position ${target + 2} of ${model.sources.length + 1}.`,
  };
};

// keys is the order a drag left behind. Anything the list does not hold is
// ignored and anything the order omits keeps its relative position at the end,
// so a partial or stale key list can never drop a selected note.
export const reorderSynthesisSources = (
  model: SynthesisModel,
  keys: string[]
): SynthesisSelectionOutcome => {
  const byKey = new Map(model.sources.map((source) => [source.key, source]));
  const ordered: SynthesisSource[] = [];
  for (const key of keys) {
    const source = byKey.get(key);
    if (source && !ordered.includes(source)) {
      ordered.push(source);
    }
  }
  for (const source of model.sources) {
    if (!ordered.includes(source)) {
      ordered.push(source);
    }
  }
  const unchanged = ordered.every((source, index) => source === model.sources[index]);
  if (unchanged) {
    return { ok: false, message: "" };
  }
  model.sources = ordered;
  invalidateSynthesisContext(model);
  return { ok: true, message: `Reordered sources: ${ordered.length + 1} in total.` };
};

export const dedupeSynthesisSources = (
  sources: SynthesisSource[],
  anchorKey: string,
  maxSupporters: number
): SynthesisSource[] => {
  const seen = new Set<string>();
  const kept: SynthesisSource[] = [];
  for (const source of sources) {
    if (source.key === "" || source.key === anchorKey || seen.has(source.key)) {
      continue;
    }
    seen.add(source.key);
    kept.push(source);
    if (kept.length >= maxSupporters) {
      break;
    }
  }
  return kept;
};
