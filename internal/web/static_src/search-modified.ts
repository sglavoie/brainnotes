export const renderSearchModified = (meta: HTMLElement, value?: string): void => {
  if (!value) return;
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return;
  const time = meta.ownerDocument.createElement("time");
  time.dateTime = value;
  time.title = `Modified: ${value}`;
  // A calendar date has no timezone. Keep it as written rather than shifting
  // UTC midnight to the previous day in browsers west of Greenwich.
  const label = /^\d{4}-\d{2}-\d{2}$/.test(value) ? value : date.toLocaleString();
  time.textContent = `Modified ${label}`;
  meta.appendChild(time);
};
