import type { FetchLike } from "./settings-requests";
import { createTemplatesPanel } from "./templates-controller";

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

/** The markup `templates/settings.html` renders for the templates panel. */
const PANEL_HTML = `
  <section data-templates-panel data-templates-csrf="csrf-token">
    <p data-templates-status></p>
    <p data-templates-notice></p>
    <ul data-templates-list></ul>
    <input type="text" data-templates-name>
    <textarea data-templates-content></textarea>
    <pre data-templates-diff hidden></pre>
    <button type="button" data-templates-save></button>
    <button type="button" data-templates-delete disabled></button>
    <button type="button" data-templates-confirm hidden></button>
    <button type="button" data-templates-cancel hidden></button>
    <button type="button" data-templates-new></button>
    <button type="button" data-templates-refresh></button>
  </section>
`;

// A real template's frontmatter holds unrendered actions, which is exactly why
// nothing on this page parses one: these bytes round-trip or the file stops
// being something `bn new` can render.
export const DAILY = "---\ntitle: {{ .Title }}\ncreated: {{ today }}\n---\n\n# {{ .Title }}\n";

export const templateSummary = (overrides: Record<string, unknown> = {}) => ({
  name: "Daily.md",
  rel_path: "Templates/Daily.md",
  size: DAILY.length,
  revision: "sha256:old",
  content: DAILY,
  ...overrides,
});

export const listPayload = (templates: unknown[], overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 7,
  action: "list",
  dir: "Templates",
  extension: ".md",
  templates,
  diff: [],
  templating: { excluded_from_notes: true, portable: true },
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

export const readPayload = () =>
  listPayload([templateSummary()], { action: "read", name: "Daily.md", rel_path: "Templates/Daily.md" });

export const previewPayload = (created: boolean) =>
  listPayload([], {
    action: "preview",
    name: "Daily.md",
    created,
    preview_id: "receipt-1",
    diff: [{ kind: "+", line: 1, text: "# {{ .Title }}" }],
  });

type Call = { url: string; body: unknown };

export type Mounted = {
  root: HTMLElement;
  calls: Call[];
  panel: NonNullable<ReturnType<typeof createTemplatesPanel>>;
};

export const mount = (
  routes: Record<string, () => Response | Promise<Response>>,
  confirm: (message: string) => boolean | Promise<boolean> = () => true
): Mounted => {
  document.body.innerHTML = PANEL_HTML;
  const root = document.querySelector("[data-templates-panel]") as HTMLElement;
  const calls: Call[] = [];
  const fetcher: FetchLike = async (input, init) => {
    const url = String(input);
    calls.push({ url, body: typeof init?.body === "string" ? JSON.parse(init.body) : null });
    const route = routes[url];
    if (!route) {
      throw new Error(`unexpected request ${url}`);
    }
    return route();
  };
  const panel = createTemplatesPanel({ root, fetch: fetcher, token: "csrf-token", confirm });
  if (!panel) {
    throw new Error("panel did not mount");
  }
  return { root, calls, panel };
};

export const element = <T extends HTMLElement>(root: HTMLElement, selector: string): T =>
  root.querySelector(selector) as T;

export const nameField = (root: HTMLElement) =>
  element<HTMLInputElement>(root, "[data-templates-name]");
export const contentField = (root: HTMLElement) =>
  element<HTMLTextAreaElement>(root, "[data-templates-content]");
export const statusText = (root: HTMLElement): string =>
  element<HTMLElement>(root, "[data-templates-status]").textContent || "";
export const openButtons = (root: HTMLElement): HTMLButtonElement[] =>
  Array.from(root.querySelectorAll("[data-templates-open]"));
