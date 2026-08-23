import { expect, vi } from "vitest";

import { initSynthesize, type SynthesisDeps } from "./synthesize";
import type { SynthesisStorageLike } from "./synthesize-storage";

export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

export const identity = (key: string): Record<string, unknown> => ({
  key,
  title: `${key} title`,
  path: `Notes/${key}.md`,
  route: `/n/${key}`,
  folder: "Notes",
});

export const relatedPayload = (mode: string, keys: string[]): Record<string, unknown> => ({
  server_date: "2026-08-19",
  source: identity("anchor"),
  mode,
  has_more: false,
  results: keys.map((key) => ({
    candidate: identity(key),
    excerpt: `${key} excerpt`,
    relationship: "direct",
    reasons: [],
    useful: false,
  })),
  feedback: [],
});

export const contextPayload = (candidates: string[], overrides: Record<string, unknown> = {}) => ({
  ok: true,
  context_id: "ctx-1",
  expires_at: "2026-08-19T12:00:00Z",
  vault_id: "vault-1",
  agent_enabled: false,
  purpose: "synthesis",
  prompt: "",
  anchor: identity("anchor"),
  supporters: candidates.map(identity),
  manifest: {
    source_path: "Notes/anchor.md",
    max_bytes: 40960,
    byte_count: 2048,
    notes: [identity("anchor"), ...candidates.map(identity)],
    excerpts: [{ path: "Notes/one.md", start_line: 4, end_line: 9 }],
  },
  markdown: "# Packet\n\nexact bytes\n",
  ...overrides,
});

export const generatePayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  enabled: true,
  context_id: "ctx-1",
  output: "# Generated\n\nagent prose\n",
  rendered_html: "<h1>Generated</h1>",
  ...overrides,
});

export const previewPayload = (body: URLSearchParams, overrides: Record<string, unknown> = {}) => {
  const title = body.get("title") || "";
  const folder = body.get("folder") || "";
  return {
    ok: true,
    preview_id: "prev-1",
    expires_at: "2026-08-19T12:05:00Z",
    context_id: body.get("context_id") || "ctx-1",
    destination: folder === "" ? `${title}.md` : `${folder}/${title}.md`,
    markdown: `---\ntitle: ${title}\n---\n\n${body.get("draft") || ""}\n`,
    rendered_html: "<p>rendered note</p>",
    ...overrides,
  };
};

export const applyPayload = (overrides: Record<string, unknown> = {}) => ({
  ok: true,
  message: "Created note.",
  key: "Synthesis/Result",
  path: "/n/Synthesis/Result",
  rel_path: "Synthesis/Result.md",
  journal_id: "journal-1",
  ...overrides,
});

export const memoryStorage = (): SynthesisStorageLike & { entries: Map<string, string> } => {
  const entries = new Map<string, string>();
  return {
    entries,
    getItem: (key) => (entries.has(key) ? String(entries.get(key)) : null),
    setItem: (key, value) => {
      entries.set(key, value);
    },
    removeItem: (key) => {
      entries.delete(key);
    },
  };
};

export const unclearableStorage = (): SynthesisStorageLike & { entries: Map<string, string> } => ({
  ...memoryStorage(),
  removeItem: () => {
    throw new Error("storage is disabled");
  },
});

export const mount = (agent: "enabled" | "disabled" = "enabled"): void => {
  document.body.innerHTML = `
    <section
      data-synthesize-root
      data-synthesize-csrf="csrf-token"
      data-synthesize-vault="vault-1"
      data-synthesize-anchor-key="anchor"
      data-synthesize-anchor-title="Anchor note"
      data-synthesize-anchor-path="Notes/anchor.md"
      data-synthesize-anchor-route="/n/anchor"
      data-synthesize-default-folder="Synthesis"
      data-synthesize-default-purpose="synthesis"
      data-synthesize-purposes="synthesis,contradictions,questions"
      data-synthesize-max-supporters="3"
      data-synthesize-agent="${agent}"
      data-synthesize-context-endpoint="/api/synthesis/context"
      data-synthesize-generate-endpoint="/api/synthesis/generate"
      data-synthesize-preview-endpoint="/api/synthesis/preview"
      data-synthesize-apply-endpoint="/api/synthesis/apply"
      data-synthesize-related-endpoint="/api/related"
      data-synthesize-search-endpoint="/api/search"
    >
      <p data-synthesize-status role="status"></p>
      <p data-synthesize-fallback>Needs JavaScript.</p>
      <div data-synthesize-workbench></div>
    </section>
  `;
};

// Consent and navigation are stubbed by default so a test that is not about
// them does not have to opt out of jsdom's unimplemented window methods.
export const start = async (
  deps: SynthesisDeps,
  agent: "enabled" | "disabled" = "enabled"
): Promise<void> => {
  mount(agent);
  const controller = initSynthesize({ confirm: () => true, redirect: () => undefined, ...deps });
  expect(controller).not.toBeNull();
  await controller?.ready;
  await Promise.resolve();
};

export const routedFetch = (
  handlers: {
    related?: (url: URL) => Response | Promise<Response>;
    search?: (url: URL) => Response | Promise<Response>;
    context?: (body: URLSearchParams) => Response | Promise<Response>;
    generate?: (body: URLSearchParams) => Response | Promise<Response>;
    preview?: (body: URLSearchParams) => Response | Promise<Response>;
    apply?: (body: URLSearchParams) => Response | Promise<Response>;
  } = {}
) =>
  vi.fn(async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const url = new URL(String(input), "https://example.test");
    const body = new URLSearchParams(String(init?.body || ""));
    if (url.pathname === "/api/related") {
      return (
        handlers.related?.(url) ||
        jsonResponse(relatedPayload(url.searchParams.get("mode") || "balanced", []))
      );
    }
    if (url.pathname === "/api/search") {
      return handlers.search?.(url) || jsonResponse([]);
    }
    if (url.pathname === "/api/synthesis/context") {
      return handlers.context?.(body) || jsonResponse(contextPayload(body.getAll("candidate")));
    }
    if (url.pathname === "/api/synthesis/generate") {
      return handlers.generate?.(body) || jsonResponse(generatePayload());
    }
    if (url.pathname === "/api/synthesis/preview") {
      return handlers.preview?.(body) || jsonResponse(previewPayload(body));
    }
    if (url.pathname === "/api/synthesis/apply") {
      return handlers.apply?.(body) || jsonResponse(applyPayload());
    }
    throw new Error(`unexpected request to ${url.pathname}`);
  });

export const callsTo = (fetcher: ReturnType<typeof routedFetch>, path: string): unknown[][] =>
  fetcher.mock.calls.filter((call) => String(call[0]) === path);

export const workbench = (): HTMLElement => {
  const element = document.querySelector<HTMLElement>("[data-synthesize-workbench]");
  if (!element) {
    throw new Error("workbench is missing");
  }
  return element;
};

export const query = <T extends Element>(selector: string): T => {
  const element = document.querySelector<T>(selector);
  if (!element) {
    throw new Error(`missing ${selector}`);
  }
  return element;
};

export const selectedKeys = (): string[] =>
  Array.from(document.querySelectorAll<HTMLElement>("[data-synthesize-source]")).map(
    (row) => row.getAttribute("data-synthesize-source") || ""
  );

export const statusText = (): string => query<HTMLElement>("[data-synthesize-status]").textContent || "";

export const addCandidate = (key: string): void => {
  query<HTMLButtonElement>(`[data-synthesize-add="${key}"]`).click();
};

export const typeInto = (selector: string, value: string): void => {
  const field = query<HTMLInputElement | HTMLTextAreaElement>(selector);
  field.value = value;
  field.dispatchEvent(new Event("input", { bubbles: true }));
};

export const prepareContext = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-prepare]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).not.toBe("");
  });
};

export const generateCandidate = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-generate]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(false);
  });
};

export const previewNote = async (): Promise<void> => {
  query<HTMLButtonElement>("[data-synthesize-preview]").click();
  await vi.waitFor(() => {
    expect(query<HTMLElement>("[data-synthesize-preview-panel]").hidden).toBe(false);
  });
};

export const deferredResponse = (): { promise: Promise<Response>; release: (value: Response) => void } => {
  let release: (value: Response) => void = () => {};
  const promise = new Promise<Response>((resolve) => {
    release = resolve;
  });
  return { promise, release };
};

export const settle = async (): Promise<void> => {
  for (let tick = 0; tick < 10; tick += 1) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
};

export const dragRow = (key: string, clientY: number): void => {
  const handle = query<HTMLButtonElement>(`[data-synthesize-handle="${key}"]`);
  handle.dispatchEvent(new MouseEvent("pointerdown", { button: 0, bubbles: true }));
  window.dispatchEvent(new MouseEvent("pointermove", { clientY, bubbles: true }));
  window.dispatchEvent(new MouseEvent("pointerup", { bubbles: true }));
};
