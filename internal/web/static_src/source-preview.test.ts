import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createPreviewScheduler, type PreviewSink } from "./source-preview";

// A renderer whose responses are resolved by hand. The whole point of this
// module is what happens when they answer out of order, which cannot be
// observed with a renderer that resolves immediately.
const deferredRenderer = () => {
  const pending: { content: string; resolve: (html: string) => void; reject: (error: unknown) => void }[] = [];
  return {
    pending,
    render: (content: string) =>
      new Promise<string>((resolve, reject) => {
        pending.push({ content, resolve, reject });
      }),
  };
};

const recordingSink = () => {
  const rendered: string[] = [];
  const settled: { message: string; failed: boolean }[] = [];
  let pendingCalls = 0;
  const sink: PreviewSink = {
    render: (html) => {
      rendered.push(html);
    },
    pending: () => {
      pendingCalls += 1;
    },
    settled: (message, failed) => {
      settled.push({ message, failed });
    },
  };
  return { sink, rendered, settled, pendingCount: () => pendingCalls };
};

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("createPreviewScheduler", () => {
  it("debounces a burst of keystrokes into one render", async () => {
    const renderer = deferredRenderer();
    const { sink, rendered } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 50 });

    scheduler.schedule("a");
    scheduler.schedule("ab");
    scheduler.schedule("abc");
    vi.advanceTimersByTime(50);

    expect(renderer.pending).toHaveLength(1);
    expect(renderer.pending[0].content).toBe("abc");

    renderer.pending[0].resolve("<p>abc</p>");
    await vi.waitFor(() => expect(rendered).toEqual(["<p>abc</p>"]));
  });

  it("does not re-render content it already rendered", async () => {
    const renderer = deferredRenderer();
    const { sink, rendered } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 10 });

    scheduler.schedule("same");
    vi.advanceTimersByTime(10);
    renderer.pending[0].resolve("<p>same</p>");
    await vi.waitFor(() => expect(rendered).toHaveLength(1));

    scheduler.schedule("same");
    vi.advanceTimersByTime(10);
    expect(renderer.pending).toHaveLength(1);
  });

  it("flush renders straight away and cancels the pending debounce", () => {
    const renderer = deferredRenderer();
    const { sink } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 100 });

    scheduler.schedule("typed");
    scheduler.flush("typed");
    expect(renderer.pending).toHaveLength(1);

    vi.advanceTimersByTime(100);
    expect(renderer.pending).toHaveLength(1);
  });

  // A failed attempt must not be remembered as rendered, or the retry the next
  // keystroke would have triggered is suppressed and the pane stays stale.
  it("re-renders the same content after a failure", async () => {
    const renderer = deferredRenderer();
    const { sink, settled } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 10 });

    scheduler.schedule("text");
    vi.advanceTimersByTime(10);
    renderer.pending[0].reject(new Error("preview: frontmatter is not valid YAML"));
    await vi.waitFor(() =>
      expect(settled).toEqual([
        { message: "preview: frontmatter is not valid YAML", failed: true },
      ])
    );

    scheduler.schedule("text");
    vi.advanceTimersByTime(10);
    expect(renderer.pending).toHaveLength(2);
  });

  it("stops rendering and answering once disposed", async () => {
    const renderer = deferredRenderer();
    const { sink, rendered } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 10 });

    scheduler.schedule("text");
    vi.advanceTimersByTime(10);
    scheduler.dispose();
    renderer.pending[0].resolve("<p>text</p>");
    await Promise.resolve();
    await Promise.resolve();

    expect(rendered).toEqual([]);

    scheduler.schedule("more");
    vi.advanceTimersByTime(100);
    expect(renderer.pending).toHaveLength(1);
  });
});

describe("a preview response that answers out of order", () => {
  // The failure this module exists to prevent. Two renders are in flight, the
  // older one answers last, and the pane must keep the newer text.
  it("discards a response overtaken by a newer one", async () => {
    const renderer = deferredRenderer();
    const { sink, rendered, settled } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 10 });

    scheduler.schedule("first");
    vi.advanceTimersByTime(10);
    scheduler.schedule("second");
    vi.advanceTimersByTime(10);
    expect(renderer.pending).toHaveLength(2);

    renderer.pending[1].resolve("<p>second</p>");
    await vi.waitFor(() => expect(rendered).toEqual(["<p>second</p>"]));

    renderer.pending[0].resolve("<p>first</p>");
    await Promise.resolve();
    await Promise.resolve();

    expect(rendered).toEqual(["<p>second</p>"]);
    expect(settled).toEqual([{ message: "", failed: false }]);
  });

  // An in-order response that is no longer the newest request is still worth
  // showing — it is closer to the text than what is on screen — but it must not
  // clear the "rendering" state, because a newer render is still in flight.
  it("shows an intermediate response without claiming the preview has settled", async () => {
    const renderer = deferredRenderer();
    const { sink, rendered, settled } = recordingSink();
    const scheduler = createPreviewScheduler({ render: renderer.render, sink, delayMs: 10 });

    scheduler.schedule("first");
    vi.advanceTimersByTime(10);
    scheduler.schedule("second");
    vi.advanceTimersByTime(10);

    renderer.pending[0].resolve("<p>first</p>");
    await vi.waitFor(() => expect(rendered).toEqual(["<p>first</p>"]));
    expect(settled).toEqual([]);

    renderer.pending[1].resolve("<p>second</p>");
    await vi.waitFor(() => expect(settled).toHaveLength(1));
    expect(rendered).toEqual(["<p>first</p>", "<p>second</p>"]);
  });
});
