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

export const rottedDetail = `unknown field "statuz"
  statuz:wip
  ^
did you mean: status`;

export const viewsPayload = () => ({
  ok: true,
  generation: 3,
  action: "list",
  views: [
    {
      name: "Focus",
      description: "Active focus work",
      query: "tag:focus and not folder:Archive",
      normalized: "tag:focus and not folder:Archive",
      sort: "-modified",
      relative_to: "",
      limit: 0,
      enabled: true,
      detail: "",
      needs_graph: false,
    },
    {
      name: "Near Hub",
      description: "",
      query: "near:[[Hub]]<=2",
      normalized: "near:[[Hub]]<=2",
      sort: "relevance",
      relative_to: "areas/hub",
      limit: 20,
      enabled: true,
      detail: "",
      needs_graph: true,
    },
    {
      name: "Rotted",
      description: "",
      query: "statuz:wip",
      normalized: "",
      sort: "",
      relative_to: "",
      limit: 0,
      enabled: false,
      detail: rottedDetail,
      needs_graph: false,
    },
  ],
  created: false,
  config_diff: [],
  config: { generation: 3, source_hash: "abc", status: "ok", restart_required: [], writable: true },
});

export const mountNav = (): HTMLElement => {
  document.body.innerHTML = `
    <nav class="site-nav" aria-label="Primary">
      <a href="/">Brief</a>
      <a href="/library">Library</a>
      <div class="saved-views-nav" data-saved-views-nav role="group" aria-label="Saved views"></div>
    </nav>
  `;
  const container = document.querySelector("[data-saved-views-nav]");
  if (!(container instanceof HTMLElement)) {
    throw new Error("nav container missing");
  }
  return container;
};

export const navItems = (container: HTMLElement): HTMLElement[] =>
  Array.from(container.querySelectorAll(".saved-views-nav-item")).flatMap((item) =>
    item instanceof HTMLElement ? [item] : []
  );

export const setLocation = (path: string): void => {
  window.history.replaceState(null, "", path);
};

export const savePayload = (created: boolean, overrides: Record<string, unknown> = {}) => ({
  ok: true,
  generation: 4,
  action: "upsert",
  name: "Focus",
  views: viewsPayload().views.slice(0, 1),
  created,
  config_diff: created
    ? [
        { kind: "+", line: 12, text: "[[views]]" },
        { kind: "+", line: 13, text: 'name = "Focus"' },
      ]
    : [
        { kind: "-", line: 13, text: 'query = "tag:old"' },
        { kind: "+", line: 13, text: 'query = "tag:focus"' },
      ],
  config: { generation: 4, source_hash: "abc", status: "ok", restart_required: [], writable: true },
  ...overrides,
});

export const mountSave = (): void => {
  document.body.innerHTML = `
    <div class="saved-views-save" data-saved-views-save data-saved-views-csrf="csrf-token">
      <button type="button" data-saved-views-open aria-expanded="false">Save as view</button>
      <form data-saved-views-form hidden>
        <pre data-saved-views-scope></pre>
        <p data-saved-views-excluded hidden></p>
        <input type="text" data-saved-views-name>
        <input type="text" data-saved-views-description>
        <button type="submit">Save</button>
        <button type="button" data-saved-views-confirm hidden>Confirm overwrite</button>
        <button type="button" data-saved-views-cancel>Cancel</button>
        <pre data-saved-views-diff hidden></pre>
        <p data-saved-views-status role="status"></p>
      </form>
    </div>
  `;
};

export const saveElement = <T extends Element>(selector: string, ctor: new () => T): T => {
  const found = document.querySelector(selector);
  if (!(found instanceof ctor)) {
    throw new Error(`missing ${selector}`);
  }
  return found;
};

export const submitSave = async (name: string): Promise<void> => {
  saveElement("[data-saved-views-name]", HTMLInputElement).value = name;
  saveElement("[data-saved-views-form]", HTMLFormElement).dispatchEvent(
    new Event("submit", { cancelable: true, bubbles: true })
  );
  await new Promise((resolve) => setTimeout(resolve, 0));
};
