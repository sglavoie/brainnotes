// Helpers shared by the Vitest suites. Only test files import this module, so
// no bundle entry in internal/cli/web-build-args.json can reach it.

export interface WaitOptions {
  // How many times the assertion is tried before its last failure is thrown.
  attempts?: number;
  // How long to wait between attempts; 0 yields one macrotask.
  delayMs?: number;
}

// waitForExpect retries a synchronous assertion until it passes, yielding to
// the event loop between attempts so pending fetch promises and timers settle.
export const waitForExpect = async (
  assertion: () => void,
  { attempts = 20, delayMs = 0 }: WaitOptions = {},
): Promise<void> => {
  let lastError: unknown;
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      assertion();
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, delayMs));
    }
  }
  throw lastError;
};

// jsonResponse is a JSON fetch Response with the given status.
export const jsonResponse = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// jsdom has no modal dialog. installDialogPolyfill gives HTMLDialogElement the
// open state and close event that the code under test relies on.
export const installDialogPolyfill = (): void => {
  if (typeof HTMLDialogElement === "undefined") return;
  const existing: { showModal?: unknown } = HTMLDialogElement.prototype;
  if (typeof existing.showModal === "function") return;
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement): void {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement, value?: string): void {
    if (!this.open) return;
    this.open = false;
    if (value !== undefined) this.returnValue = value;
    this.dispatchEvent(new Event("close"));
  };
};
