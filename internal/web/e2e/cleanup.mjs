// Delete the disposable world prepare.mjs built. Playwright runs this as
// globalTeardown, after the server it launched has been killed.
//
// The removal is guarded rather than trusted. A manifest that named a path
// outside the system temporary directory would make this an rm of somebody's
// data, and the whole point of the fixture is that nothing durable is ever in
// reach — so an unexpected path is reported and left alone.

import { existsSync, readFileSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { relative } from "node:path";

import { MANIFEST_PATH } from "./manifest.mjs";

function withinTempDir(path) {
  const rel = relative(realpathSync(tmpdir()), path);
  return rel !== "" && !rel.startsWith("..") && !rel.startsWith("/");
}

export default function cleanup() {
  if (!existsSync(MANIFEST_PATH)) return;
  const { fixtureRoot } = JSON.parse(readFileSync(MANIFEST_PATH, "utf8"));
  if (!withinTempDir(fixtureRoot)) {
    console.error(
      `e2e cleanup: refusing to remove ${fixtureRoot}: it is outside the system ` +
        `temporary directory, so it is not a fixture this run created`,
    );
    return;
  }
  rmSync(fixtureRoot, { recursive: true, force: true });
  rmSync(MANIFEST_PATH, { force: true });
}
