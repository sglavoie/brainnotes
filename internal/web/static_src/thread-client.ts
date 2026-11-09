import { createSourceClient } from "./source-save";

export type ThreadItem = { group: string; quote: string; commentary: string; key: string; title: string; path: string; warning: string };
export type PreparedThread = { content: string; items: ThreadItem[] };
export type ThreadSummary = { key: string; title: string; path: string };
export type ThreadCatalog = { items: ThreadSummary[]; token: string; vault_id: string };

export const threadClient = (key: string, token: string) => createSourceClient({ key, token, endpoints: {
  read: "/api/source", save: "/api/source/save", create: "/api/source/create", preview: "/api/source/preview", search: "/api/search",
} });

export const threadCatalog = async (): Promise<ThreadCatalog> => {
  const response = await fetch("/api/threads", { headers: { Accept: "application/json" } });
  if (!response.ok || response.redirected) throw new Error("Threads could not be loaded. Try again.");
  const result = await response.json() as ThreadCatalog;
  if (!Array.isArray(result.items) || typeof result.token !== "string") throw new Error("Threads could not be loaded.");
  return result;
};

export const prepareThread = async (token: string, content: string, action: string, fields: Record<string, string> = {}): Promise<PreparedThread> => {
  const response = await fetch("/api/threads/prepare", { method: "POST", headers: { "X-BN-CSRF": token }, body: new URLSearchParams({ content, action, ...fields }) });
  if (response.redirected) throw new Error("The vault did not answer. Sign in again and retry.");
  const result = await response.json() as PreparedThread & { message?: string };
  if (!response.ok) throw new Error(result.message || "The thread could not be prepared.");
  if (typeof result.content !== "string" || !Array.isArray(result.items)) throw new Error("The vault did not return a thread.");
  return result;
};

export const threadGroups = ["Evidence", "Counterexamples", "Still unclear"];
export const threadGroupSelect = (): HTMLSelectElement => {
  const select = document.createElement("select");
  for (const group of threadGroups) select.add(new Option(group, group));
  return select;
};
