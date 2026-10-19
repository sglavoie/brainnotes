// Whether a browser run needs the performance vaults.
//
// Seeding the 1,000- and 10,000-note vaults is the slowest part of preparing
// the fixture, and starting their servers adds two more processes to every run.
// Only the `performance-chromium-*` projects use them, so run.mjs prepares and
// serves them only when the requested projects can include one of those.
//
// The decision is made from the same `--project` filters Playwright receives,
// with Playwright's own matching rule (`*` wildcards, case-insensitive), so a
// filter that selects a performance project here selects it there too. No
// `--project` filter means every project, performance included.
// BN_E2E_PERFORMANCE=1 or =0 overrides the derivation.

export const PERFORMANCE_COUNTS = [1000, 10000];

export function performanceProjectName(count) {
  return `performance-chromium-${count}`;
}

// projectFilters returns the values of every `--project=X` and `--project X`
// argument, in order.
export function projectFilters(args) {
  const filters = [];
  for (let i = 0; i < args.length; i += 1) {
    const arg = args[i];
    if (arg.startsWith("--project=")) {
      filters.push(arg.slice("--project=".length));
    } else if (arg === "--project" && i + 1 < args.length) {
      filters.push(args[i + 1]);
      i += 1;
    }
  }
  return filters;
}

function filterMatches(filter, name) {
  const escaped = filter.replace(/[.+?^${}()|[\]\\]/g, "\\$&").replaceAll("*", ".*");
  return new RegExp(`^${escaped}$`, "i").test(name);
}

export function needsPerformance(args, env = process.env) {
  const override = env.BN_E2E_PERFORMANCE;
  if (override === "1") return true;
  if (override === "0") return false;
  if (override !== undefined && override !== "") {
    throw new Error(`BN_E2E_PERFORMANCE must be 1 or 0, not ${JSON.stringify(override)}`);
  }
  const filters = projectFilters(args);
  if (filters.length === 0) return true;
  const names = PERFORMANCE_COUNTS.map(performanceProjectName);
  return filters.some((filter) => names.some((name) => filterMatches(filter, name)));
}
