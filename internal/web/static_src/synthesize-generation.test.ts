// @vitest-environment jsdom

import { afterEach, describe, expect, it, vi } from "vitest";

import type { SynthesisDeps } from "./synthesize";
import {
  addCandidate,
  callsTo,
  generateCandidate,
  generatePayload,
  jsonResponse,
  memoryStorage,
  prepareContext,
  query,
  relatedPayload,
  routedFetch,
  start,
  typeInto,
} from "./synthesize.test-helpers";

afterEach(() => {
  document.body.innerHTML = "";
  vi.restoreAllMocks();
});

describe("synthesis generation", () => {
  const withContext = async (
    handlers: Parameters<typeof routedFetch>[0] = {},
    deps: SynthesisDeps = {},
    agent: "enabled" | "disabled" = "enabled"
  ): Promise<ReturnType<typeof routedFetch>> => {
    const fetcher = routedFetch({
      related: () => jsonResponse(relatedPayload("balanced", ["one"])),
      ...handlers,
    });
    await start({ fetch: fetcher, storage: memoryStorage(), clipboard: null, ...deps }, agent);
    addCandidate("one");
    await prepareContext();
    typeInto("[data-synthesize-draft]", "my own words");
    return fetcher;
  };

  it("asks for consent once and never puts the candidate into the draft", async () => {
    let generations = 0;
    const confirm = vi.fn((message: string) => message !== "");
    const fetcher = await withContext(
      {
        generate: () => {
          generations += 1;
          return jsonResponse(
            generatePayload({
              output: `candidate ${generations}`,
              rendered_html: `<p>candidate ${generations}</p>`,
            })
          );
        },
      },
      { confirm }
    );

    await generateCandidate();
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(String(confirm.mock.calls[0][0])).toContain("[agent].command");
    expect(query<HTMLElement>("[data-synthesize-candidate-markdown]").textContent).toBe(
      "candidate 1"
    );
    expect(query<HTMLElement>("[data-synthesize-candidate-rendered]").innerHTML).toBe(
      "<p>candidate 1</p>"
    );
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");

    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-candidate-markdown]").textContent).toBe(
        "candidate 2"
      );
    });
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(2);
    expect(
      new URLSearchParams(
        String((callsTo(fetcher, "/api/synthesis/generate")[0][1] as RequestInit).body)
      ).get("context_id")
    ).toBe("ctx-1");
  });

  it("states the server's real retention before asking for consent", async () => {
    const confirm = vi.fn((_message: string) => false);
    const fetcher = await withContext({}, { confirm });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    expect(confirm).toHaveBeenCalledTimes(1);
    const message = String(confirm.mock.calls[0][0]);
    expect(message).toContain(
      "Your prompt and that packet are retained in server memory until the context expires, is evicted to keep memory bounded, or the server restarts, and are never written to disk."
    );
    expect(message).toContain("The stored packet is sent to the command only after you accept this notice.");
    expect(message).toContain("The generated candidate is not stored on the server");
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(0);
  });

  it("sends nothing when the disclosure is declined", async () => {
    const fetcher = await withContext({}, { confirm: () => false });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
      "Generation canceled."
    );
    expect(callsTo(fetcher, "/api/synthesis/generate")).toHaveLength(0);
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
  });

  it("reports the disabled agent without asking for consent or losing the draft", async () => {
    const confirm = vi.fn(() => true);
    await withContext(
      {
        generate: () =>
          jsonResponse({
            ok: true,
            enabled: false,
            context_id: "ctx-1",
            message: "Generation is unavailable until [agent].command is configured.",
          }),
      },
      { confirm },
      "disabled"
    );

    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "Generation is unavailable until [agent].command is configured."
      );
    });
    expect(confirm).not.toHaveBeenCalled();
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
  });

  it("replaces, appends, and dismisses the candidate only when asked", async () => {
    await withContext();
    await generateCandidate();

    query<HTMLButtonElement>("[data-synthesize-candidate-append]").click();
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "my own words\n\n# Generated\n\nagent prose\n"
    );
    expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
      "Candidate appended to the draft."
    );

    query<HTMLButtonElement>("[data-synthesize-candidate-replace]").click();
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "# Generated\n\nagent prose\n"
    );

    query<HTMLButtonElement>("[data-synthesize-candidate-dismiss]").click();
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe(
      "# Generated\n\nagent prose\n"
    );
  });

  it("copies the candidate and disables copying with no writable clipboard", async () => {
    const writeText = vi.fn<(text: string) => Promise<void>>().mockResolvedValue(undefined);
    await withContext({}, { clipboard: { writeText } });
    await generateCandidate();
    query<HTMLButtonElement>("[data-synthesize-candidate-copy]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "Candidate copied."
      );
    });
    expect(writeText).toHaveBeenCalledWith("# Generated\n\nagent prose\n");

    await withContext();
    await generateCandidate();
    expect(query<HTMLButtonElement>("[data-synthesize-candidate-copy]").disabled).toBe(true);
  });

  it("keeps the draft when the generation command fails", async () => {
    await withContext({
      generate: () =>
        jsonResponse(
          { ok: false, code: "internal_error", message: "synthesis: generation command timed out" },
          500
        ),
    });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toBe(
        "synthesis: generation command timed out"
      );
    });
    expect(query<HTMLElement>("[data-synthesize-candidate-panel]").hidden).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(false);
  });

  it("drops an expired context without touching the text the user wrote", async () => {
    await withContext({
      generate: () =>
        jsonResponse({ ok: false, code: "not_found", message: "context expired" }, 404),
    });
    query<HTMLButtonElement>("[data-synthesize-generate]").click();
    await vi.waitFor(() => {
      expect(query<HTMLElement>("[data-synthesize-generate-status]").textContent).toContain(
        "Rebuild it, then preview again."
      );
    });
    expect(query<HTMLElement>("[data-synthesize-packet]").textContent).toBe("");
    expect(query<HTMLButtonElement>("[data-synthesize-prepare]").textContent).toBe(
      "Rebuild context"
    );
    expect(query<HTMLButtonElement>("[data-synthesize-generate]").disabled).toBe(true);
    expect(query<HTMLTextAreaElement>("[data-synthesize-draft]").value).toBe("my own words");
  });
});
