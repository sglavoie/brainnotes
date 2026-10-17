import { responseFailure, unansweredRequest, type RequestPurpose } from "./request-outcome";
import {
  SETTINGS_APPLY_ENDPOINT,
  SETTINGS_ENDPOINT,
  SETTINGS_PREVIEW_ENDPOINT,
  TEMPLATES_APPLY_ENDPOINT,
  TEMPLATES_ENDPOINT,
  TEMPLATES_PREVIEW_ENDPOINT,
  buildConfigRequestInit,
  normalizeSettingsResponse,
  normalizeTemplatesResponse,
  type SettingMutationRequest,
  type SettingsRequestBody,
  type SettingsResult,
  type TemplateMutationRequest,
  type TemplatesRequestBody,
  type TemplatesResult,
} from "./api";

export type FetchLike = (input: string, init?: RequestInit) => Promise<Response>;

export type Outcome<T> = { ok: true; result: T } | { ok: false; message: string };

const readPayload = async (resp: Response): Promise<unknown> => {
  try {
    return await resp.json();
  } catch {
    return null;
  }
};

// finish is the one place a transport answer becomes an outcome, for both
// families. A non-2xx carries the server's typed refusal, and a 200 whose body
// says `ok: false` is still a refusal — the settings and templates routes answer
// the same envelope either way.
const finish = async <T extends { ok: boolean; message?: string }>(
  resp: Response,
  normalize: (payload: unknown) => T,
  purpose: RequestPurpose
): Promise<Outcome<T>> => {
  const payload = await readPayload(resp);
  const failure = responseFailure(resp, payload, purpose);
  if (failure) return failure;
  const result = normalize(payload);
  return { ok: true, result };
};

const get = async <T extends { ok: boolean; message?: string }>(
  fetcher: FetchLike,
  endpoint: string,
  normalize: (payload: unknown) => T
): Promise<Outcome<T>> => {
  try {
    const resp = await fetcher(endpoint, { headers: { Accept: "application/json" } });
    return await finish(resp, normalize, "read");
  } catch {
    return unansweredRequest("read");
  }
};

const post = async <T extends { ok: boolean; message?: string }>(
  fetcher: FetchLike,
  endpoint: string,
  token: string,
  body: SettingsRequestBody | TemplatesRequestBody,
  normalize: (payload: unknown) => T
): Promise<Outcome<T>> => {
  const purpose = endpoint.endsWith("/apply") ? "write" : "preview";
  try {
    const resp = await fetcher(endpoint, buildConfigRequestInit(token, body));
    return await finish(resp, normalize, purpose);
  } catch {
    return unansweredRequest(purpose);
  }
};

/** Lists the editable portable settings. Read-only, so it carries no token. */
export const fetchSettings = (fetcher: FetchLike): Promise<Outcome<SettingsResult>> =>
  get(fetcher, SETTINGS_ENDPOINT, normalizeSettingsResponse);

export const previewSetting = (
  fetcher: FetchLike,
  token: string,
  body: SettingMutationRequest
): Promise<Outcome<SettingsResult>> =>
  post(fetcher, SETTINGS_PREVIEW_ENDPOINT, token, body, normalizeSettingsResponse);

export const applySetting = (
  fetcher: FetchLike,
  token: string,
  previewID: string
): Promise<Outcome<SettingsResult>> =>
  post(
    fetcher,
    SETTINGS_APPLY_ENDPOINT,
    token,
    { preview_id: previewID },
    normalizeSettingsResponse
  );

/** Lists the configured templates directory. Read-only, so it carries no token. */
export const fetchTemplates = (fetcher: FetchLike): Promise<Outcome<TemplatesResult>> =>
  get(fetcher, TEMPLATES_ENDPOINT, normalizeTemplatesResponse);

/**
 * Reads one template's exact bytes. The name is a query parameter rather than a
 * path segment because the server addresses a template by name inside the
 * configured directory, never by path — which is what makes a separator a
 * refusal instead of a traversal.
 */
export const fetchTemplate = (
  fetcher: FetchLike,
  name: string
): Promise<Outcome<TemplatesResult>> =>
  get(
    fetcher,
    `${TEMPLATES_ENDPOINT}?name=${encodeURIComponent(name)}`,
    normalizeTemplatesResponse
  );

export const previewTemplate = (
  fetcher: FetchLike,
  token: string,
  body: TemplateMutationRequest
): Promise<Outcome<TemplatesResult>> =>
  post(fetcher, TEMPLATES_PREVIEW_ENDPOINT, token, body, normalizeTemplatesResponse);

export const applyTemplate = (
  fetcher: FetchLike,
  token: string,
  previewID: string
): Promise<Outcome<TemplatesResult>> =>
  post(
    fetcher,
    TEMPLATES_APPLY_ENDPOINT,
    token,
    { preview_id: previewID },
    normalizeTemplatesResponse
  );
