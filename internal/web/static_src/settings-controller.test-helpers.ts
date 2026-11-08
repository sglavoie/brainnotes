import { createSettingsPanel } from "./settings-controller";
import type { FetchLike } from "./settings-requests";

/** The markup `templates/settings.html` renders for the settings panel. */
export const PANEL_HTML = `
  <section data-settings-panel data-settings-csrf="csrf-token">
    <p data-settings-status></p>
    <input type="search" data-settings-filter>
    <p data-settings-filter-summary></p>
    <pre data-settings-diff hidden></pre>
    <button type="button" data-settings-confirm hidden></button>
    <button type="button" data-settings-cancel hidden></button>
    <button type="button" data-settings-refresh></button>
    <ul data-settings-list></ul>
  </section>
`;

export const setting = (overrides: Record<string, unknown> = {}) => ({
  key: "capture.file",
  kind: "string",
  summary: "Note quick captures are appended to.",
  assigned: true,
  layer: "portable",
  shadowed: false,
  value: { kind: "string", text: "Inbox.md" },
  ...overrides,
});

export const listPayload = (settings: unknown[]) => ({
  ok: true,
  generation: 7,
  action: "list",
  settings,
  config_diff: [],
  config: { generation: 7, source_hash: "h", status: "ok", restart_required: [], writable: true },
});

export const previewPayload = () => ({
  ...listPayload([setting()]),
  action: "set",
  key: "capture.file",
  preview_id: "receipt-1",
  config_diff: [{ kind: "+", line: 4, text: 'file = "Inbox.md"' }],
});

type Call = { url: string; body: unknown };

type Mounted = {
  root: HTMLElement;
  calls: Call[];
  panel: NonNullable<ReturnType<typeof createSettingsPanel>>;
};

export const mount = (routes: Record<string, () => Response>, html = PANEL_HTML): Mounted => {
  document.body.innerHTML = html;
  const root = document.querySelector("[data-settings-panel]") as HTMLElement;
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
  const panel = createSettingsPanel({ root, fetch: fetcher, token: "csrf-token" });
  if (!panel) {
    throw new Error("panel did not mount");
  }
  return { root, calls, panel };
};

export const rows = (root: HTMLElement): HTMLElement[] =>
  Array.from(root.querySelectorAll("[data-settings-row]"));

export const rowFor = (root: HTMLElement, key: string): HTMLElement => {
  const row = root.querySelector(`[data-settings-row="${key}"]`);
  if (!(row instanceof HTMLElement)) {
    throw new Error(`no row for ${key}`);
  }
  return row;
};

export const button = (row: HTMLElement, attribute: string): HTMLButtonElement =>
  row.querySelector(`[${attribute}]`) as HTMLButtonElement;

export const control = (row: HTMLElement): HTMLInputElement | HTMLTextAreaElement =>
  row.querySelector("[data-settings-control]") as HTMLInputElement | HTMLTextAreaElement;

export const statusText = (root: HTMLElement): string =>
  (root.querySelector("[data-settings-status]") as HTMLElement).textContent || "";
