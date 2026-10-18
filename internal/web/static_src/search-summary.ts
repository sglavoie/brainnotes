export type SearchCounts = {
  total: number;
  limit: number;
  cap: number;
};

// Keep the search array usable by existing clients; counts travel as headers.
export const readSearchCounts = (response: Response): SearchCounts | null => {
  const values = ["Total", "Limit", "Cap"].map((name) =>
    response.headers?.get(`X-BN-Search-${name}`));
  if (values.some((value) => value == null || !/^\d+$/.test(value))) {
    return null;
  }
  const [total, limit, cap] = values.map(Number);
  if (![total, limit, cap].every(Number.isSafeInteger) || cap <= 0) {
    return null;
  }
  return { total, limit, cap };
};

export const searchSummary = (
  shown: number,
  query: string,
  counts: SearchCounts | null,
  offset = 0
): string => {
  const scope = query ? ` for "${query}"` : "";
  if (!counts || counts.total <= shown && offset === 0) {
    return `${shown} matching ${shown === 1 ? "note" : "notes"}${scope}.`;
  }
  const reason = remainingSearchMessage(counts, offset + shown);
  return `Showing ${offset ? `${offset + 1}–${offset + shown}` : shown} of ${counts.total} matching notes${scope}. ${reason} Bulk actions apply only to selected notes.`;
};

const remainingSearchMessage = (counts: SearchCounts, end: number): string => {
  if (counts.limit > 0 && counts.limit <= end) return `Query limit: ${counts.limit}.`;
  return end < counts.total ? "Use Next results to see more matches." : "";
};
