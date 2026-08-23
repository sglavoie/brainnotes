import type { RelatedMode } from "./related-types";

export const waitForExpect = async (assertion: () => void): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < 30; attempt += 1) {
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

export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

export const deferred = <T>(): { promise: Promise<T>; resolve: (value: T) => void } => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise;
  });
  return { promise, resolve };
};

export const identity = (key: string, overrides: Record<string, unknown> = {}): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Folder/${key}.md`,
  route: `/notes/${key}`,
  ...overrides,
});

export const payload = (
  mode: RelatedMode,
  limit: number,
  overrides: Record<string, unknown> = {}
): Record<string, unknown> => ({
  server_date: "2026-08-19",
  source: identity("source"),
  mode,
  has_more: limit < 20,
  results: Array.from({ length: limit }, (_, index) => ({
    candidate: identity(`${mode}-${index + 1}`),
    excerpt: `${mode} excerpt ${index + 1}`,
    relationship: index % 2 === 0 ? "direct" : "two-hop",
    reasons: [{ kind: "text", detail: `${mode} reason ${index + 1}` }],
    useful: false,
  })),
  feedback: [],
  ...overrides,
});

export const rootHTML = (): string => `
  <section
    class="panel related-notes"
    data-related-notes
    data-related-source-key="source"
    data-related-endpoint="/api/related"
    data-related-item-endpoint="/api/related/item"
    data-related-csrf="csrf-token"
  >
    <h2>Related Notes</h2>
    <p data-related-status role="status" aria-live="polite">Related notes will load here.</p>
  </section>
`;

export const requestOptions = (input: RequestInfo | URL): { mode: RelatedMode; limit: number } => {
  const url = new URL(String(input), "https://example.test");
  return {
    mode: url.searchParams.get("mode") as RelatedMode,
    limit: Number(url.searchParams.get("limit")),
  };
};

export const postFields = (init?: RequestInit): URLSearchParams =>
  new URLSearchParams(String(init?.body || ""));

export const itemPayload = (
  action: string,
  candidate: string,
  overrides: Record<string, unknown> = {}
): Record<string, unknown> => ({
  ok: true,
  action,
  source: identity("source"),
  candidate: identity(candidate),
  ...(action === "useful" || action === "dismissed" || action === "later" || action === "clear"
    ? { state: action }
    : {}),
  ...overrides,
});
