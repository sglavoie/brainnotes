import {
  buildQueryCompletionsURL,
  buildQueryValidateURL,
  normalizeQueryCompletionsResponse,
  normalizeQueryValidateResponse,
  type QueryCompletionsResponse,
  type QueryValidateResponse,
} from "./api";
import type { FetchLike } from "./query-bar-model";

/**
 * The two read-only query endpoints, each behind its own abort controller.
 *
 * Both are fired per keystroke, so replies can and do land out of order. An
 * expression's validation is only meaningful for the exact text it was computed
 * from — the error offsets index that text — so a slow reply must never
 * overwrite a newer one. Aborting the previous request is the enforcement:
 * `fetch` rejects on the aborted one, and this module reports it as `null`
 * rather than a failure, which is not the same thing as the request failing.
 */

export type QueryRequester = {
  validate: (expr: string) => Promise<QueryValidateResponse | null>;
  completions: (expr: string, pos: number) => Promise<QueryCompletionsResponse | null>;
  cancel: () => void;
};

type Slot = {
  controller: AbortController | null;
  token: number;
};

const abortSlot = (slot: Slot): void => {
  if (slot.controller) {
    slot.controller.abort();
    slot.controller = null;
  }
};

export const createQueryRequester = (fetcher: FetchLike): QueryRequester => {
  const validateSlot: Slot = { controller: null, token: 0 };
  const completionsSlot: Slot = { controller: null, token: 0 };

  // The wrapper distinguishes "superseded" (null) from a payload that is itself
  // null, which a bare `unknown` return could not.
  const request = async (slot: Slot, url: string): Promise<{ payload: unknown } | null> => {
    abortSlot(slot);
    const token = (slot.token += 1);
    const controller =
      typeof AbortController === "undefined" ? null : new AbortController();
    slot.controller = controller;

    let resp: Response;
    try {
      resp = await fetcher(url, {
        headers: { Accept: "application/json" },
        ...(controller ? { signal: controller.signal } : {}),
      });
    } catch (error) {
      // An abort and a network drop both reject here; only the token can tell
      // them apart, and only when AbortController is missing entirely does the
      // token do the work alone.
      if (token !== slot.token) {
        return null;
      }
      throw error;
    } finally {
      if (token === slot.token) {
        slot.controller = null;
      }
    }

    if (token !== slot.token) {
      return null;
    }
    if (!resp.ok) {
      throw new Error(`query request failed with status ${resp.status}`);
    }
    const payload: unknown = await resp.json();
    return token === slot.token ? { payload } : null;
  };

  return {
    validate: async (expr) => {
      const result = await request(validateSlot, buildQueryValidateURL(expr));
      return result === null ? null : normalizeQueryValidateResponse(result.payload);
    },
    completions: async (expr, pos) => {
      const result = await request(completionsSlot, buildQueryCompletionsURL(expr, pos));
      return result === null ? null : normalizeQueryCompletionsResponse(result.payload);
    },
    cancel: () => {
      abortSlot(validateSlot);
      abortSlot(completionsSlot);
      validateSlot.token += 1;
      completionsSlot.token += 1;
    },
  };
};
