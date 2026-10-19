import type { SynthesisSource } from "./synthesize-model";
import { setSynthesisWarning } from "./synthesize-render";
import { resolveSynthesisSource } from "./synthesize-requests";
import type { SynthesisRuntime } from "./synthesize-runtime";
import { dedupeSynthesisSources } from "./synthesize-sources";

type SourceResolution = { resolved: SynthesisSource[]; missing: string[]; unreachable: string[] };

const notes = (count: number): string => `${count} saved ${count === 1 ? "note" : "notes"}`;

const restoreWarning = (missing: string[], unreachable: string[]): string => {
  const parts: string[] = [];
  if (missing.length > 0) {
    parts.push(`${notes(missing.length)} ${missing.length === 1 ? "is" : "are"} no longer in this vault and ${missing.length === 1 ? "was" : "were"} removed from the selection: ${missing.join(", ")}.`);
  }
  if (unreachable.length > 0) {
    parts.push(`Could not reach the vault to restore ${notes(unreachable.length)}. ${unreachable.length === 1 ? "It stays" : "They stay"} saved for this workbench until a retry succeeds.`);
  }
  return parts.join(" ");
};

// Only a note the vault reports missing is dropped. Any other failure says
// nothing about the note, so its key is kept for a retry.
const resolveSources = async (runtime: SynthesisRuntime, keys: string[]): Promise<SourceResolution> => {
  const resolution: SourceResolution = { resolved: [], missing: [], unreachable: [] };
  for (const key of keys) {
    try {
      const source = await resolveSynthesisSource(runtime.fetcher, runtime.config.relatedEndpoint, key);
      if (source) {
        resolution.resolved.push(source);
      } else {
        resolution.missing.push(key);
      }
    } catch {
      resolution.unreachable.push(key);
    }
  }
  return resolution;
};

/**
 * Returns the restore step for saved source keys. It resolves true when every
 * key settled, and false when some could not be reached; those stay pending
 * behind a Retry control.
 */
export const createSynthesisSourceRestorer = (
  runtime: SynthesisRuntime
): ((keys: string[]) => Promise<boolean>) => {
  const { config, model, view } = runtime;
  let retrying = false;
  const retry = async (): Promise<void> => {
    if (retrying) return;
    retrying = true;
    try {
      await settleSources(runtime.unresolvedSources, true);
    } finally { retrying = false; }
  };

  // A restore replaces the selection; a retry adds to it. Either way the
  // selection can change while the requests are in flight, so a source the
  // person added meanwhile is kept, and the keys stay pending so a save made
  // before they settle still carries them.
  const settleSources = async (keys: string[], retried: boolean): Promise<boolean> => {
    const before = [...model.sources];
    runtime.unresolvedSources = [...keys];
    const { resolved, missing, unreachable } = await resolveSources(runtime, keys);
    const added = model.sources.filter((source) => !before.includes(source));
    const ordered = retried ? [...model.sources, ...resolved] : [...resolved, ...added];
    model.sources = dedupeSynthesisSources(ordered, config.anchor.key, config.maxSupporters);
    runtime.unresolvedSources = unreachable;
    runtime.render();
    runtime.renderCandidatePanels();
    const warning = restoreWarning(missing, unreachable);
    if (warning !== "" || retried) {
      setSynthesisWarning(view, warning, unreachable.length > 0 ? { label: "Retry", run: () => void retry() } : undefined);
    }
    // Unreachable keys ride along in every save, so persisting here only drops
    // the notes the vault reported missing.
    runtime.persist();
    return unreachable.length === 0;
  };

  return (keys) => settleSources(keys, false);
};
