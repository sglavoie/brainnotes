// Deterministic corpora live only in dedicated disposable performance vaults.
export function performanceNotes(count) {
  const files = {};
  for (let i = 0; i < count; i++) {
    const name = `Record${String(i).padStart(5, "0")}`;
    const next = `Record${String((i + 1) % count).padStart(5, "0")}`;
    files[`Research/${name}.md`] = `---\ntitle: ${name}\nstatus: ${i % 2 ? "draft" : "reference"}\ntags: [research, group${i % 10}]\n---\n\n# Observation ${i}\n\nCorpus observation about rivers, fieldwork, and migration. Compare [[Research/${next}]].\n\n${"Measurements and supporting observations. ".repeat(20)}\n`;
  }
  files["LongWriting.md"] = "---\ntitle: Long writing\nstatus: draft\n---\n\n" +
    Array.from({ length: 1200 }, (_, i) => `## Section ${i}\n\nThe morning trail follows the river. Record the changing light and compare [[Research/Record00000]].\n\n`).join("");
  return files;
}
