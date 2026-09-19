// The shared test object for the browser suite.
//
// Every test gets an origin guard, always on. A browser is not a jsdom stub: a
// page it renders will follow whatever a stylesheet, a script tag, or a fetch
// asks it to, and this project's production surface names a Pi over Cloudflare
// Access. "The smoke tests cannot reach production" has to be enforced rather
// than intended, so requests leaving the fixture origin are aborted and
// recorded, and a spec asserts the recording is empty.

import { execFileSync } from "node:child_process";

import { test as base, expect } from "@playwright/test";

import { readManifest } from "./manifest.mjs";

export const manifest = readManifest();

// MACHINE_LOCAL are the paths a running server creates that its own history is
// defined to exclude: internal/vaultgit/scope.go excludes `.bnotes/` wholesale and
// allowlists three named files back in, so `index/` and `locks/` are outside the
// history because nothing let them in — both are regenerable machine-local caches.
// `git status` reports them as untracked all the same, so
// a raw porcelain comparison would call a correctly-behaving server dirty. The
// list is a prefix match on purpose — a *new* `.bnotes/` file, which is what a
// future in-scope state file would look like, is not covered and will fail here.
// `source-saves/` joined them with the whole-file source API: it is the
// per-save_id idempotency ledger, deliberately kept outside vaultgit.InScope
// because it records that a submission was answered, not what a note holds.
// `uploads/` is the attachment staging tree (internal/attachments/store.go), and
// is outside the allowlist on the same terms: a file a person has chosen but not
// yet saved is not yet part of the note, and the vault's history must not hold it
// until the save that references it says it does.
// `sync/` is the synchronization channel and its state: the enrollment record,
// the packages the transport delivered, the receipts this device owes the peer,
// and the retained versions of a contested path under `sync/conflicts/`. It is
// outside the allowlist for a stronger reason than the caches above — what
// travels between two replicas must not depend on what the content history
// tracks, so internal/vaultsync's portable scope excludes `.bnotes` *wholesale*
// and a vault's history therefore never holds a package or a retained branch.
// Committing them would be worse than untidy: resolving a conflict discards the
// retained bytes, and a tracked path deleted with nothing authorizing it is what
// latches vaultgit's `blocked.json` and refuses every later mutation.
const MACHINE_LOCAL = [
  ".bnotes/index/",
  ".bnotes/locks/",
  ".bnotes/source-saves/",
  ".bnotes/sync/",
  ".bnotes/uploads/",
];

// gitRead runs a read-only git command in a vault under the fixture's own HOME
// and with the system configuration off, which is the part worth centralizing: a
// suite that read the developer's `~/.gitconfig` would behave differently on
// every machine it ran on.
export function gitRead(vault, ...args) {
  return runGit(vault, args, "inherit");
}

// gitReadQuiet is gitRead for a question whose answer may legitimately be "there
// is no such path at that commit" — which is exactly what a caller asserting a
// deletion landed is asking. git diagnoses that on stderr, and an inherited
// `fatal:` line in the middle of a passing suite reads as a failure.
export function gitReadQuiet(vault, ...args) {
  return runGit(vault, args, "ignore");
}

function runGit(vault, args, stderr) {
  return execFileSync("git", args, {
    cwd: vault,
    encoding: "utf8",
    stdio: ["ignore", "pipe", stderr],
    env: { PATH: process.env.PATH, HOME: manifest.home, GIT_CONFIG_NOSYSTEM: "1" },
  });
}

// gitStatus reports the vault's porcelain status with those caches removed.
// `--untracked-files=all` matters: the default collapses an untracked directory
// to a single `?? .bnotes/` line, and a prefix filter cannot tell that line
// apart from one hiding a real note.
//
// It lives here rather than in a spec because every spec that writes has the
// same two questions afterwards — did the snapshot happen, and did the peer
// vault move — and a second copy of the exclusion list is a second thing to
// keep in step with internal/vaultgit/scope.go.
export function gitStatus(vault) {
  const out = gitRead(vault, "status", "--porcelain=v1", "--untracked-files=all");
  return out
    .split("\n")
    .filter((line) => line.trim() !== "")
    .filter((line) => !MACHINE_LOCAL.some((prefix) => line.slice(3).startsWith(prefix)))
    .join("\n");
}

export const test = base.extend({
  // foreignRequests is the list of request URLs that tried to leave the fixture
  // origin, in order. It is `auto` so the guard is installed for every test
  // whether or not the test names it — an opt-in guard protects only the specs
  // that remembered it.
  foreignRequests: [
    async ({ page, baseURL }, use) => {
      const attempted = [];
      const fixtureOrigin = new URL(baseURL).origin;
      await page.route("**/*", async (route) => {
        const url = route.request().url();
        if (isFixtureOrigin(url, fixtureOrigin)) {
          await route.continue();
          return;
        }
        attempted.push(url);
        await route.abort("blockedbyclient");
      });
      await use(attempted);
    },
    { auto: true },
  ],
});

// isFixtureOrigin parses rather than prefix-matches, which is the whole point.
// `http://127.0.0.1:53211@evil.example/x` *starts with* the fixture origin and
// is a request to evil.example, because everything before the `@` is userinfo.
// A startsWith guard admits it and records nothing, so the "no request leaves
// the fixture origin" spec stays green while the browser talks to somebody
// else; isolation.spec.js drives exactly that URL as a navigation and watches a
// listening server for the arrival.
//
// Userinfo is the whole of that bypass class, not one example of it: a valid URL
// whose serialization begins with `http://host:port` can only continue with `/`,
// `?`, `#`, `@`, or nothing, and only `@` changes the host. A string like
// `http://127.0.0.1:53211.evil.example/x` is not a counter-example — it is not a
// URL at all, since the authority splits at the last `:` and `53211.evil.example`
// is not a port — which is what the catch below is for.
export function isFixtureOrigin(url, fixtureOrigin) {
  if (url.startsWith("data:") || url.startsWith("blob:")) {
    return true;
  }
  try {
    return new URL(url).origin === fixtureOrigin;
  } catch {
    // An unparseable URL is not provably the fixture, so it is not the fixture.
    return false;
  }
}

export { expect };
