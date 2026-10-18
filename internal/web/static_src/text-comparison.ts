/** A bounded line comparison shared by draft conflicts and restore previews. */
export type DiffLine = { kind: "same" | "removed" | "added"; text: string };
const lines = (text: string): string[] => text.match(/[^\n]*\n|[^\n]+$/g) || [];
const marked = (text: string[], kind: DiffLine["kind"]): DiffLine[] => text.map((line) => ({ kind, text: line }));

export const compareText = (before: string, after: string): DiffLine[] => {
  const left = lines(before), right = lines(after);
  let start = 0, end = 0;
  while (start < Math.min(left.length, right.length) && left[start] === right[start]) start++;
  while (end < Math.min(left.length, right.length) - start &&
    left[left.length - end - 1] === right[right.length - end - 1]) end++;
  return [...marked(left.slice(0, start), "same"),
    ...compareMiddle(left.slice(start, left.length - end), right.slice(start, right.length - end)),
    ...marked(end ? left.slice(-end) : [], "same")];
};

const compareMiddle = (left: string[], right: string[]): DiffLine[] => {
  // Bound both CPU and memory for long or entirely rewritten notes. The coarse
  // fallback still shows every changed byte; it never pretends omitted text agrees.
  if ((left.length + 1) * (right.length + 1) > 1_000_000) {
    return [...marked(left, "removed"), ...marked(right, "added")];
  }
  const width = right.length + 1;
  const lengths = new Uint32Array((left.length + 1) * width);
  for (let i = left.length - 1; i >= 0; i--) {
    for (let j = right.length - 1; j >= 0; j--) {
      lengths[i * width + j] = left[i] === right[j] ? 1 + lengths[(i + 1) * width + j + 1]
        : Math.max(lengths[(i + 1) * width + j], lengths[i * width + j + 1]);
    }
  }
  return walkComparison(left, right, lengths, width);
};

const walkComparison = (left: string[], right: string[], lengths: Uint32Array, width: number): DiffLine[] => {
  const result: DiffLine[] = [];
  let i = 0, j = 0;
  while (i < left.length && j < right.length) {
    if (left[i] === right[j]) { result.push({ kind: "same", text: left[i++] }); j++; }
    else if (lengths[(i + 1) * width + j] >= lengths[i * width + j + 1]) {
      result.push({ kind: "removed", text: left[i++] });
    } else result.push({ kind: "added", text: right[j++] });
  }
  return [...result, ...marked(left.slice(i), "removed"), ...marked(right.slice(j), "added")];
};

const lineElement = (doc: Document, line: DiffLine): HTMLElement => {
  const row = doc.createElement("div");
  row.className = `comparison-line comparison-${line.kind}`;
  const marker = doc.createElement("span");
  marker.className = "comparison-marker";
  marker.textContent = line.kind === "added" ? "+" : line.kind === "removed" ? "−" : " ";
  marker.setAttribute("aria-label", line.kind === "same" ? "Unchanged" : line.kind === "added" ? "Added" : "Removed");
  const text = doc.createElement("code");
  text.textContent = line.text.endsWith("\n") ? line.text.slice(0, -1) : line.text;
  row.append(marker, text);
  if (!line.text.endsWith("\n")) {
    const ending = doc.createElement("span");
    ending.className = "comparison-ending";
    ending.textContent = " (no newline at end)";
    row.append(ending);
  }
  return row;
};

const appendRun = (host: HTMLElement, run: DiffLine[]): void => {
  const doc = host.ownerDocument;
  if (run[0]?.kind !== "same" && run.length > 200) {
    host.append(lineElement(doc, { kind: run[0].kind, text: run.map((line) => line.text).join("") }));
    return;
  }
  if (run[0]?.kind !== "same" || run.length <= 8) {
    host.append(...run.map((line) => lineElement(doc, line)));
    return;
  }
  host.append(...run.slice(0, 3).map((line) => lineElement(doc, line)));
  const details = doc.createElement("details"), summary = doc.createElement("summary");
  details.className = "comparison-unchanged";
  summary.textContent = `${run.length - 6} unchanged lines`;
  details.append(summary);
  // Populate only when requested, keeping long notes cheap to inspect.
  details.addEventListener("toggle", () => {
    if (details.open && details.childElementCount === 1) {
      for (const line of run.slice(3, -3)) details.append(lineElement(doc, line));
    }
  });
  host.append(details, ...run.slice(-3).map((line) => lineElement(doc, line)));
};

export const renderTextComparison = (
  host: HTMLElement, before: string, after: string, beforeLabel: string, afterLabel: string,
): void => {
  const doc = host.ownerDocument;
  host.replaceChildren();
  host.classList.add("text-comparison");
  const legend = doc.createElement("p");
  legend.className = "comparison-legend";
  legend.textContent = `− ${beforeLabel} · + ${afterLabel}`;
  host.append(legend);
  if (before === after) {
    host.append("The text is unchanged.");
    return;
  }
  const diff = compareText(before, after);
  for (let start = 0; start < diff.length;) {
    let end = start + 1;
    while (end < diff.length && diff[end].kind === diff[start].kind) end++;
    appendRun(host, diff.slice(start, end));
    start = end;
  }
};
