import {
  CONFIG_STATUS_ENDPOINT,
  normalizeConfigStatusResponse,
  type PropertyConfigStatus,
} from "./api";
import type { FetchLike } from "./properties-model";

export const CONFIG_STATUS_POLL_MS = 5000;

export const configInvalidMessage = (config: PropertyConfigStatus): string => {
  if (config.status === "ok") {
    return "";
  }
  const detail = config.error || "";
  const cause =
    detail === ""
      ? "brainnotes.toml is invalid."
      : `brainnotes.toml is invalid: ${detail}`;
  return `${cause} The last valid configuration is still in use and property writes are refused until it parses.`;
};

export const configRestartMessage = (config: PropertyConfigStatus): string => {
  const keys = config.restart_required;
  if (keys.length === 0) {
    return "";
  }
  return `${keys.join(", ")} changed on disk. Restart bn serve to apply ${keys.length === 1 ? "it" : "them"}.`;
};

export type ConfigStatusListener = (config: PropertyConfigStatus) => void;

export type ConfigStatusPoller = {
  poll: () => Promise<void>;
  start: () => void;
  stop: () => void;
  latest: () => PropertyConfigStatus | null;
};

export type ConfigStatusPollerDeps = {
  fetch?: FetchLike;
  intervalMs?: number;
  onStatus: ConfigStatusListener;
  onGenerationChange?: (generation: number) => void;
};

// createConfigStatusPoller watches brainnotes.toml through the server. It runs
// only while the page is visible, so a backgrounded tab does not keep asking a
// question nobody is reading the answer to.
export const createConfigStatusPoller = (
  doc: Document,
  deps: ConfigStatusPollerDeps
): ConfigStatusPoller => {
  const fetcher = deps.fetch || fetch;
  const intervalMs = deps.intervalMs || CONFIG_STATUS_POLL_MS;
  let timer = 0;
  let seenGeneration = -1;
  let current: PropertyConfigStatus | null = null;

  const poll = async (): Promise<void> => {
    if (doc.visibilityState !== "visible") {
      return;
    }
    try {
      const resp = await fetcher(CONFIG_STATUS_ENDPOINT, {
        headers: { Accept: "application/json" },
      });
      if (!resp.ok) {
        return;
      }
      const result = normalizeConfigStatusResponse(await resp.json());
      current = result;
      deps.onStatus(result);
      if (seenGeneration >= 0 && result.generation !== seenGeneration) {
        deps.onGenerationChange?.(result.generation);
      }
      seenGeneration = result.generation;
    } catch {
      // A failed poll is not worth a banner: the next tick reports the truth.
    }
  };

  const stop = (): void => {
    if (timer !== 0) {
      clearInterval(timer);
      timer = 0;
    }
  };

  const start = (): void => {
    stop();
    if (doc.visibilityState !== "visible") {
      return;
    }
    timer = setInterval(() => {
      void poll();
    }, intervalMs) as unknown as number;
  };

  doc.addEventListener("visibilitychange", () => {
    if (doc.visibilityState === "visible") {
      void poll();
      start();
      return;
    }
    stop();
  });

  return { poll, start, stop, latest: () => current };
};
