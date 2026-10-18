import type { CockpitTask } from "./api";

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

export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

export const brokenTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_broken",
  signature: "sig-broken",
  kind: "broken_target",
  priority: 30,
  title: "Broken target: missing",
  summary: "1 unresolved link to missing",
  detail: "missing",
  count: 1,
  affected: [{
    title: "Source",
    rel_path: "Work/Source.md",
    folder: "Work",
    location: "line 5",
  }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "relink", label: "Relink" },
    { kind: "create_stub", label: "Create stub note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: false,
  ...overrides,
});

export const orphanTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_orphan",
  signature: "sig-orphan",
  kind: "orphan_note",
  priority: 50,
  title: "Orphan note",
  summary: "No inbound links",
  detail: "",
  count: 1,
  affected: [{ title: "Lonely", rel_path: "Lonely.md" }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

export const deadEndTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_dead_end",
  signature: "sig-dead-end",
  kind: "dead_end_note",
  priority: 60,
  title: "Dead-end note: Terminal",
  summary: "Terminal has no outgoing links",
  detail: "Terminal.md",
  count: 1,
  affected: [{ title: "Terminal", rel_path: "Terminal.md" }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "exempt_dead_end", label: "Mark as intentional dead end" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

export const longTailTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_long_tail",
  signature: "sig-long-tail",
  kind: "long_tail_value",
  priority: 70,
  title: "Long-tail value: status = odd",
  summary: "2 notes use status = odd",
  detail: "status = odd",
  count: 2,
  affected: [
    { title: "RareA", rel_path: "RareA.md", location: "frontmatter.status" },
    { title: "RareB", rel_path: "RareB.md", location: "frontmatter.status" },
  ],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "clean_long_tail", label: "Remove property from affected notes" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

export const sensitiveTask = (overrides: Partial<CockpitTask> = {}): CockpitTask => ({
  id: "task_sensitive",
  signature: "sig-sensitive",
  kind: "sensitive_content",
  priority: 15,
  title: "Possible sensitive content: Secrets",
  summary: "Possible secret detected",
  detail: "Work/Secrets.md",
  count: 1,
  affected: [{
    title: "Secrets",
    rel_path: "Work/Secrets.md",
    folder: "Work",
    location: "line 12: AWS access key ID",
  }],
  actions: [
    { kind: "open", label: "Open note" },
    { kind: "snooze", label: "Snooze" },
    { kind: "mark_done", label: "Mark done" },
  ],
  triage_only: true,
  ...overrides,
});

export const queuePayload = (tasks: CockpitTask[], message = ""): unknown => ({
  ok: true,
  message: message || undefined,
  tasks,
  counts: {
    active: tasks.length,
    done_hidden: 2,
    snoozed_hidden: 1,
    total_generated: tasks.length + 3,
  },
  warnings: [{ message: "Cockpit state could not be loaded." }],
});

export const renderPanel = (csrfToken = "csrf-token"): void => {
  document.body.innerHTML = `
    <section
      data-fixes-panel
      data-fixes-endpoint="/api/cockpit"
      data-fixes-item-endpoint="/api/cockpit/item?view=daily"
      data-fixes-preview-endpoint="/api/fixes/preview"
      data-fixes-apply-endpoint="/api/fixes/apply"
      data-fixes-open-endpoint="/api/open"
      data-fixes-csrf="${csrfToken}"
      aria-busy="false"
    >
      <dd data-fixes-count-active>0</dd>
      <dd data-fixes-count-done-hidden>0</dd>
      <dd data-fixes-count-snoozed-hidden>0</dd>
      <dd data-fixes-count-total-generated>0</dd>
      <p data-fixes-status></p>
      <p data-fixes-stale hidden></p>
      <button data-fixes-button data-fixes-reload>Reload fixes</button>
      <p data-fixes-error hidden></p>
      <p data-fixes-empty hidden></p>
      <ul data-fixes-warnings hidden></ul>
      <ul data-fixes-list hidden></ul>
    </section>
  `;
};
