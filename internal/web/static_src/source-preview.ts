// The preview scheduler, and the one thing it exists to get right: a preview
// that arrives after a newer one must never be shown.
//
// Rendering happens on the server, so every keystroke that survives the debounce
// becomes a request, and requests over a network do not answer in the order they
// were sent. Without an ordering rule the pane flickers back to older text and
// — worse — settles on it whenever the last response to arrive is not the last
// one issued. The rule here is a monotonic counter and two numbers: a response
// is applied only when its sequence is the highest one yet applied, and the
// "rendering" state only clears when the response is the newest one issued.

export type PreviewRenderer = (content: string) => Promise<string>;

export type PreviewSink = {
  // render is handed the server's HTML for the newest content. It is never
  // called with an out-of-order response.
  render(html: string): void;
  // pending says a render is in flight and nothing newer has landed.
  pending(): void;
  // settled says the newest issued render answered, successfully or not.
  settled(message: string, failed: boolean): void;
};

export type PreviewScheduler = {
  // schedule debounces, then renders. Scheduling the content already shown is
  // a no-op, so arrow keys and selections cost nothing.
  schedule(content: string): void;
  // flush renders immediately, for the moment a pane becomes visible and the
  // user is waiting rather than typing.
  flush(content: string): void;
  dispose(): void;
};

export type PreviewSchedulerOptions = {
  render: PreviewRenderer;
  sink: PreviewSink;
  delayMs?: number;
};

const DEFAULT_DELAY_MS = 350;

export const createPreviewScheduler = (
  options: PreviewSchedulerOptions
): PreviewScheduler => {
  const delayMs = options.delayMs ?? DEFAULT_DELAY_MS;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let issued = 0;
  let applied = 0;
  let rendered: string | null = null;
  let disposed = false;

  const run = (content: string): void => {
    if (disposed || content === rendered) {
      return;
    }
    issued += 1;
    const sequence = issued;
    rendered = content;
    options.sink.pending();

    options.render(content).then(
      (html) => finish(sequence, () => options.sink.render(html), "", false),
      (error: unknown) => {
        // The content is *not* left recorded as rendered: a failed attempt has
        // to be retried on the next keystroke, and remembering it as shown
        // would suppress exactly that retry.
        if (rendered === content) {
          rendered = null;
        }
        finish(sequence, () => {}, previewErrorMessage(error), true);
      }
    );
  };

  // finish is where the ordering rule lives. `sequence <= applied` is a
  // response overtaken by a newer one that already landed — discarded outright.
  // `sequence !== issued` is a response that is newest-so-far but no longer the
  // newest asked for, so its HTML is worth showing while the status stays
  // "rendering" for the request still in flight.
  const finish = (
    sequence: number,
    apply: () => void,
    message: string,
    failed: boolean
  ): void => {
    if (disposed || sequence <= applied) {
      return;
    }
    applied = sequence;
    apply();
    if (sequence === issued) {
      options.sink.settled(message, failed);
    }
  };

  return {
    schedule(content: string): void {
      if (disposed) {
        return;
      }
      if (timer !== null) {
        clearTimeout(timer);
      }
      timer = setTimeout(() => {
        timer = null;
        run(content);
      }, delayMs);
    },
    flush(content: string): void {
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
      run(content);
    },
    dispose(): void {
      disposed = true;
      if (timer !== null) {
        clearTimeout(timer);
        timer = null;
      }
    },
  };
};

const previewErrorMessage = (error: unknown): string => {
  if (typeof error === "string" && error.trim() !== "") {
    return error.trim();
  }
  if (error instanceof Error && error.message.trim() !== "") {
    return error.message.trim();
  }
  return "The preview could not be rendered.";
};
