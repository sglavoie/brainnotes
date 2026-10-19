import { normalizeManagementErrorMessage, normalizeSearchResults } from "./api";
import { readResponsePayload } from "./daily-brief-http";
import { buildRelatedRequestURL, normalizeRelatedResponse, requestRelated } from "./related-api";
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
// identity of its source note and reports a key it cannot resolve as a 404,
// which is exactly the missing-note signal the restore path needs. Any other
// failure says nothing about the note, so it is thrown for the caller to retry
// rather than reported as a deletion.
export const resolveSynthesisSource = async (
  fetcher: SynthesisFetch,
  endpoint: string,
  key: string
): Promise<SynthesisSource | null> => {
  const response = await fetcher(buildRelatedRequestURL(endpoint, key, "balanced", 1), {
    headers: { Accept: "application/json" },
  });
  if (response.status === 404) {
    return null;
  }
  const payload = await readResponsePayload(response);
  if (!response.ok) {
    throw new Error(normalizeManagementErrorMessage(response.status, payload));
  }
  const normalized = normalizeRelatedResponse(payload);
  if (!normalized) {
    throw new Error("Related notes returned an invalid response.");
  }
  return {
    key: normalized.source.key,
    title: normalized.source.title || normalized.source.path,
    path: normalized.source.path,
    route: normalized.source.route,
  };
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
