import type { Scheduler } from "./query-bar-model";

/**
 * Shared fixtures for the query-bar tests, following the same
 * `<cluster>.test-helpers.ts` convention as daily-fixes and synthesize.
 */

export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

export const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 0));
    }
  }
  throw lastError;
};

/**
 * A scheduler whose timers only fire when a test says so, so the debounce
 * windows are exercised without waiting on real time.
 */
export type ManualScheduler = Scheduler & {
  flush: () => void;
  pending: () => number;
};

export const manualScheduler = (): ManualScheduler => {
  const tasks = new Map<number, () => void>();
  let nextHandle = 1;
  return {
    schedule: (fn) => {
      const handle = nextHandle++;
      tasks.set(handle, fn);
      return handle;
    },
    cancel: (handle) => {
      tasks.delete(handle);
    },
    flush: () => {
      const due = Array.from(tasks.entries());
      tasks.clear();
      for (const [, fn] of due) {
        fn();
      }
    },
    pending: () => tasks.size,
  };
};

export const validatePayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  query: "tag:focus",
  normalized: "tag:focus",
  fields_used: ["tag"],
  needs_graph: false,
  ...overrides,
});

export const invalidValidatePayload = (
  query: string,
  error: Record<string, unknown> = {}
) => ({
  ok: false,
  query,
  fields_used: [],
  needs_graph: false,
  error: {
    message: "unknown field",
    start: 0,
    end: query.length,
    line: 1,
    column: 1,
    positioned: true,
    suggestions: [],
    ...error,
  },
});

export const completionsPayload = (
  query: string,
  values: Array<Partial<{ value: string; insert: string; kind: string; detail: string; count: number }>>,
  overrides: Record<string, unknown> = {}
) => ({
  ok: true,
  query,
  kind: "value",
  field: "tag",
  arg: "",
  prefix: "",
  pos: query.length,
  replace: { start: query.length, end: query.length },
  completions: values.map((value) => ({
    value: value.value || "",
    insert: value.insert || value.value || "",
    kind: value.kind || "tag",
    detail: value.detail || "",
    count: value.count || 0,
  })),
  ...overrides,
});

/** The Library markup the query bar hydrates, kept in step with library.html. */
export const queryBarMarkup = `
<section class="panel query-bar" data-query-bar data-query-bar-phase="empty">
  <div class="query-bar-row">
    <label for="query-bar-input">Expression</label>
    <div class="query-bar-field">
      <input
        id="query-bar-input"
        type="text"
        role="combobox"
        aria-autocomplete="list"
        aria-expanded="false"
        aria-invalid="false"
        aria-controls="query-bar-completions"
        data-query-bar-input
      >
      <ul id="query-bar-completions" role="listbox" data-query-bar-completions hidden></ul>
    </div>
    <button type="button" data-query-bar-clear>Clear</button>
  </div>
  <div class="query-bar-error" data-query-bar-error hidden>
    <p data-query-bar-error-message></p>
    <pre aria-hidden="true"><code data-query-bar-error-query></code>
<code data-query-bar-error-caret></code></pre>
    <p data-query-bar-error-suggestions hidden></p>
  </div>
  <p id="query-bar-status" data-query-bar-status role="status" aria-live="polite"></p>
  <button class="query-bar-button" type="button" data-query-bar-retry hidden>Retry check</button>
</section>
`;

export type MountedQueryBar = {
  input: HTMLInputElement;
  list: HTMLElement;
  statusEl: HTMLElement;
  errorEl: HTMLElement;
  root: HTMLElement;
  options: () => HTMLElement[];
};

export const mountQueryBar = (): MountedQueryBar => {
  document.body.innerHTML = queryBarMarkup;
  const root = document.querySelector("[data-query-bar]") as HTMLElement;
  const input = document.querySelector("[data-query-bar-input]") as HTMLInputElement;
  const list = document.querySelector("[data-query-bar-completions]") as HTMLElement;
  return {
    root,
    input,
    list,
    statusEl: document.querySelector("[data-query-bar-status]") as HTMLElement,
    errorEl: document.querySelector("[data-query-bar-error]") as HTMLElement,
    options: () => Array.from(list.querySelectorAll("[role=\"option\"]")) as HTMLElement[],
  };
};

/** Types into the mounted input and fires the `input` event the bar listens to. */
export const typeQuery = (input: HTMLInputElement, text: string, pos = text.length): void => {
  input.value = text;
  input.setSelectionRange(pos, pos);
  input.dispatchEvent(new Event("input", { bubbles: true }));
};

export const pressKey = (input: HTMLInputElement, key: string, init: KeyboardEventInit = {}): KeyboardEvent => {
  const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
  input.dispatchEvent(event);
  return event;
};

/** Routes the two query endpoints to per-path handlers. */
export type QueryRoutes = {
  validate?: (url: URL) => Response | Promise<Response>;
  completions?: (url: URL) => Response | Promise<Response>;
};

export const routedFetch = (routes: QueryRoutes) => {
  const calls: string[] = [];
  const fetcher = async (input: RequestInfo | URL): Promise<Response> => {
    const raw = typeof input === "string" ? input : input.toString();
    calls.push(raw);
    const url = new URL(raw, "http://localhost");
    if (url.pathname === "/api/query/validate" && routes.validate) {
      return routes.validate(url);
    }
    if (url.pathname === "/api/query/completions" && routes.completions) {
      return routes.completions(url);
    }
    return jsonResponse({ ok: false }, 404);
  };
  return { fetcher, calls };
};
