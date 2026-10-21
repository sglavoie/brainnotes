// Vitest setup file (vitest.config.js `setupFiles`), run before every test file.
//
// The suite runs with `isolate: false`: one jsdom window and one module graph
// serve every file in a worker. Each file still expects the fresh page a new
// jsdom would give it, so this module records what a file leaves behind on the
// long-lived globals and puts the page back after the file finishes:
// listeners on window/document/<html>/<head>/<body>, pending timers and
// animation frames, MutationObservers, fake timers, stubbed or reassigned
// globals, the DOM, the URL, Web Storage and cookies.
//
// Only test files reach this module, so no bundle entry can import it.
import { afterAll, vi } from "vitest";

type Cleanup = () => void;

interface Hygiene {
  cleanups: Set<Cleanup>;
  globals: Map<PropertyKey, PropertyDescriptor>;
  documentKeys: Set<PropertyKey>;
  url: string;
}

const hygieneKey = Symbol.for("brainnotes.vitest.hygiene");
const registry = globalThis as unknown as Record<symbol, Hygiene | undefined>;

const longLivedTarget = (target: unknown): boolean =>
  target === globalThis ||
  target === document.defaultView ||
  target === document ||
  target === document.documentElement ||
  target === document.head ||
  target === document.body;

type AddListener = EventTarget["addEventListener"];

const recordingAdd = (state: Hygiene, add: AddListener, owner?: EventTarget): AddListener =>
  function (this: EventTarget, type, listener, options) {
    const target = owner ?? this;
    add.call(target, type, listener, options);
    if (!listener || !longLivedTarget(target)) return;
    const capture = typeof options === "boolean" ? options : Boolean(options?.capture);
    state.cleanups.add(() => target.removeEventListener(type, listener, capture));
  };

const trackListeners = (state: Hygiene): void => {
  const proto = EventTarget.prototype;
  proto.addEventListener = recordingAdd(state, proto.addEventListener);
  // Vitest copies the jsdom window's methods onto globalThis already bound, so
  // `window.addEventListener` never reaches the prototype patched above.
  globalThis.addEventListener = recordingAdd(state, globalThis.addEventListener, globalThis);
};

// Wraps a scheduling global so each pending callback can be cancelled. The
// wrapper is what vi.useFakeTimers later saves and vi.useRealTimers restores.
const trackScheduler = (
  state: Hygiene,
  scheduleName: "setTimeout" | "setInterval" | "requestAnimationFrame",
  cancelName: "clearTimeout" | "clearInterval" | "cancelAnimationFrame",
): void => {
  const scope = globalThis as unknown as Record<string, (...args: unknown[]) => unknown>;
  const schedule = scope[scheduleName];
  const cancel = scope[cancelName];
  if (typeof schedule !== "function" || typeof cancel !== "function") return;
  const wrapped = function (this: unknown, ...args: unknown[]) {
    const handle = schedule.apply(this, args);
    state.cleanups.add(() => cancel.call(globalThis, handle));
    return handle;
  };
  Object.defineProperty(wrapped, "name", { value: scheduleName });
  scope[scheduleName] = wrapped;
};

const trackMutationObservers = (state: Hygiene): void => {
  const proto = MutationObserver.prototype;
  const observe = proto.observe;
  proto.observe = function (this: MutationObserver, target, options) {
    observe.call(this, target, options);
    state.cleanups.add(() => this.disconnect());
  };
};

const install = (): Hygiene => {
  const state: Hygiene = {
    cleanups: new Set(),
    globals: new Map(),
    documentKeys: new Set(Reflect.ownKeys(document)),
    url: window.location.href,
  };
  trackListeners(state);
  trackScheduler(state, "setTimeout", "clearTimeout");
  trackScheduler(state, "setInterval", "clearInterval");
  trackScheduler(state, "requestAnimationFrame", "cancelAnimationFrame");
  trackMutationObservers(state);
  // Snapshot after wrapping, so restoring a global keeps the wrapped schedulers.
  for (const key of Reflect.ownKeys(globalThis)) {
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, key);
    if (descriptor) state.globals.set(key, descriptor);
  }
  return state;
};

const sameDescriptor = (a: PropertyDescriptor, b: PropertyDescriptor): boolean =>
  a.value === b.value && a.get === b.get && a.set === b.set;

const restoreGlobals = (state: Hygiene): void => {
  for (const [key, descriptor] of state.globals) {
    const current = Object.getOwnPropertyDescriptor(globalThis, key);
    if (current && sameDescriptor(current, descriptor)) continue;
    if (current && !current.configurable) continue;
    Object.defineProperty(globalThis, key, descriptor);
  }
  for (const key of Reflect.ownKeys(document)) {
    if (!state.documentKeys.has(key)) Reflect.deleteProperty(document, key);
  }
};

const clearAttributes = (element: Element): void => {
  for (const name of element.getAttributeNames()) element.removeAttribute(name);
};

const clearCookies = (): void => {
  for (const cookie of document.cookie.split(";")) {
    const name = cookie.split("=")[0]?.trim();
    if (name) document.cookie = `${name}=; expires=Thu, 01 Jan 1970 00:00:00 GMT; path=/`;
  }
};

const resetPage = (state: Hygiene): void => {
  (document.activeElement as HTMLElement | null)?.blur?.();
  document.head.replaceChildren();
  document.body.replaceChildren();
  clearAttributes(document.documentElement);
  clearAttributes(document.body);
  clearAttributes(document.head);
  document.title = "";
  window.history.replaceState(null, "", state.url);
  window.localStorage.clear();
  window.sessionStorage.clear();
  clearCookies();
};

const cleanUp = (state: Hygiene): void => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
  for (const cleanup of state.cleanups) cleanup();
  state.cleanups.clear();
  restoreGlobals(state);
  resetPage(state);
};

// A fresh module graph per file, as isolation would give: module-level state
// starts over, and a file's vi.mock reaches modules other files imported first.
vi.resetModules();

// Node-environment files share this setup but have no page to reset.
if (typeof document !== "undefined") {
  const state = (registry[hygieneKey] ??= install());
  cleanUp(state);
  afterAll(() => cleanUp(state));
}
