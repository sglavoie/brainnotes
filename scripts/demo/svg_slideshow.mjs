// Builds docs/assets/demo-web.svg: browser screenshots stacked in one SVG that
// CSS steps through, the same technique scripts/demo/render_terminal_svg.py uses
// for the terminal. Each frame is a full-colour JPEG inside a drawn browser
// window, so the README gets sharp screens without a GIF's 256-colour palette,
// and it plays as an <img> with no script.

const CHROME_HEIGHT = 48;
const RADIUS = 12;

function escapeXML(text) {
  return text.replace(/[<>&"']/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;", "'": "&apos;" })[c]);
}

// frames: [{ jpeg: Buffer, url: string, caption: string, holdMs: number }]
export function buildSlideshowSVG(frames, { width, height }) {
  const total = frames.reduce((sum, frame) => sum + frame.holdMs, 0);
  const outerHeight = height + CHROME_HEIGHT;
  const rules = [
    `.bn-frame { visibility: hidden; animation: ${total}ms step-end infinite; }`,
    ".bn-url { font: 15px ui-monospace, Menlo, monospace; fill: #a9b1d6; }",
    ".bn-caption { font: bold 16px ui-monospace, Menlo, monospace; fill: #7aa2f7; }",
  ];
  const bodies = [];
  let start = 0;
  frames.forEach((frame, index) => {
    const end = start + frame.holdMs;
    rules.push(
      `.bn-f${index} { animation-name: bn-k${index}; } ` +
        `@keyframes bn-k${index} { 0% { visibility: hidden; } ` +
        `${((100 * start) / total).toFixed(3)}% { visibility: visible; } ` +
        `${((100 * end) / total).toFixed(3)}% { visibility: ${end === total ? "visible" : "hidden"}; } }`,
    );
    bodies.push(
      `<g class="bn-frame bn-f${index}">` +
        `<text class="bn-url" x="172" y="30">${escapeXML(frame.url)}</text>` +
        `<text class="bn-caption" text-anchor="end" x="${width - 20}" y="30">${escapeXML(frame.caption)}</text>` +
        `<image x="0" y="${CHROME_HEIGHT}" width="${width}" height="${height}" clip-path="url(#bn-clip)" ` +
        `href="data:image/jpeg;base64,${frame.jpeg.toString("base64")}"/></g>`,
    );
    start = end;
  });
  // Readers who asked for less motion get the opening frame, held.
  rules.push("@media (prefers-reduced-motion: reduce) { .bn-frame { animation: none; } .bn-f0 { visibility: visible; } }");

  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${width} ${outerHeight}" role="img">
<title>Brain Notes browser workspace</title>
<style>
${rules.join("\n")}
</style>
<defs><clipPath id="bn-clip"><path d="M0 ${CHROME_HEIGHT}H${width}V${outerHeight - RADIUS}a${RADIUS} ${RADIUS} 0 0 1-${RADIUS} ${RADIUS}H${RADIUS}a${RADIUS} ${RADIUS} 0 0 1-${RADIUS}-${RADIUS}Z"/></clipPath></defs>
<rect width="${width}" height="${outerHeight}" rx="${RADIUS}" fill="#1a1b26"/>
<circle cx="24" cy="24" r="7" fill="#ff5f57"/><circle cx="46" cy="24" r="7" fill="#febc2e"/><circle cx="68" cy="24" r="7" fill="#28c840"/>
<rect x="156" y="10" width="${Math.round(width * 0.42)}" height="28" rx="14" fill="#24283b"/>
${bodies.join("\n")}
</svg>
`;
}
