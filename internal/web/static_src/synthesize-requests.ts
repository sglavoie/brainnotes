import { normalizeManagementErrorMessage, normalizeSearchResults } from "./api";
import { readResponsePayload } from "./daily-brief-http";
import { requestRelated } from "./related-api";
import type { RelatedMode, RelatedResponse } from "./related-types";
import type { SearchResult } from "./api";
import type { SynthesisSource } from "./synthesize-model";

export type SynthesisFetch = (
  input: RequestInfo | URL,
  init?: RequestInit
) => Promise<Response>;

export const SYNTHESIS_SUGGESTION_LIMIT = 8;

export const requestSynthesisSuggestions = (
  fetcher: SynthesisFetch,
  endpoint: string,
  source: string,
  mode: RelatedMode
): Promise<RelatedResponse> =>
  requestRelated(fetcher, endpoint, source, mode, SYNTHESIS_SUGGESTION_LIMIT);

// A saved selection holds note keys only, so a restored key has to be resolved
// against the vault as it is now. The Related endpoint answers with the current
// identity of its source note and reports a key it cannot resolve as an error,
// which is exactly the missing-note signal the restore path needs.
export const resolveSynthesisSource = async (
  fetcher: SynthesisFetch,
  endpoint: string,
  key: string
): Promise<SynthesisSource | null> => {
  try {
    const response = await requestRelated(fetcher, endpoint, key, "balanced", 1);
    return {
      key: response.source.key,
      title: response.source.title || response.source.path,
      path: response.source.path,
      route: response.source.route,
    };
  } catch {
    return null;
  }
};

export const buildSynthesisSearchURL = (endpoint: string, query: string): string => {
  const params = new URLSearchParams();
  params.set("q", query);
  return `${endpoint}?${params.toString()}`;
};

export const requestSynthesisSearch = async (
  fetcher: SynthesisFetch,
  endpoint: string,
  query: string
): Promise<SearchResult[]> => {
  const response = await fetcher(buildSynthesisSearchURL(endpoint, query), {
    headers: { Accept: "application/json" },
  });
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw new Error(normalizeManagementErrorMessage(response.status, payload));
  }
  return normalizeSearchResults(payload);
};
