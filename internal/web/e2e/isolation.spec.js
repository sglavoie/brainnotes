// The browser suite cannot reach the real vault or production services.
//
// This is the half of the harness that is not about the application at all. A
// browser test is the most dangerous kind this project has: it runs a real
// engine against a real server, and the real server's whole job is to write
// notes. The rules below state, as assertions rather than as intentions, that
// what it writes to is a throwaway directory nobody owns and that nothing it
// loads comes from off the machine.

import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { readFileSync, realpathSync } from "node:fs";
import { createServer } from "node:http";
import { connect } from "node:net";
import { networkInterfaces, tmpdir } from "node:os";
import { join, relative } from "node:path";

import { expect, manifest, test } from "./fixture.js";

// MACHINE_LOCAL are the paths a running server creates that its own history is
// defined to exclude: internal/vaultgit/scope.go allowlists `.bnotes/`, and
// `index/` and `locks/` are outside that allowlist because both are regenerable
// machine-local caches. `git status` reports them as untracked all the same, so
// a raw porcelain comparison would call a correctly-behaving server dirty. The
// list is a prefix match on purpose — a *new* `.bnotes/` file, which is what a
// future in-scope state file would look like, is not covered and will fail here.
const MACHINE_LOCAL = [".bnotes/index/", ".bnotes/locks/"];

// gitStatus reports the vault's porcelain status with those caches removed.
// `--untracked-files=all` matters: the default collapses an untracked directory
// to a single `?? .bnotes/` line, and a prefix filter cannot tell that line
// apart from one hiding a real note.
function gitStatus(vault) {
  const out = execFileSync("git", ["status", "--porcelain=v1", "--untracked-files=all"], {
    cwd: vault,
    encoding: "utf8",
    env: { PATH: process.env.PATH, HOME: manifest.home, GIT_CONFIG_NOSYSTEM: "1" },
  });
  return out
    .split("\n")
    .filter((line) => line.trim() !== "")
    .filter((line) => !MACHINE_LOCAL.some((prefix) => line.slice(3).startsWith(prefix)))
    .join("\n");
}

// expectThrowawayStorage is the containment predicate, applied to whatever path
// is passed in. The interesting caller is the one that passes a path the
// *server* demonstrably wrote to, rather than a path this harness chose.
function expectThrowawayStorage(path, what) {
  const temporaryRoot = realpathSync(tmpdir());
  // Both sides are resolved through realpath before comparing. On macOS
  // os.tmpdir() reports /var/folders/... while the fixture paths are already
  // resolved to /private/var/folders/..., and a textual compare of the two calls
  // a directory that *is* under the temporary root an escape from it.
  const rel = relative(temporaryRoot, realpathSync(path));
  expect(rel.startsWith(".."), `${what} (${path}) is outside the system temporary directory`).toBe(
    false,
  );
  expect(rel, `${what} (${path}) is the system temporary directory itself`).not.toBe("");
  expect(path.split("/"), `${what} (${path}) is named like a real vault`).not.toContain(
    "BrainNotesVault",
  );
}

test("the server is serving the disposable fixture vault and not another one", async ({ request }) => {
  const status = await (await request.get("/api/config/status")).json();

  // Each fixture vault's brainnotes.toml carries a line unique to this run, and
  // /api/config/status publishes the SHA-256 of the file the running server
  // loaded. Comparing those hashes identifies the vault itself — "a note called
  // Alpha rendered" would not, because the real vault could have one too.
  const localHash = createHash("sha256")
    .update(readFileSync(join(manifest.localVault, "brainnotes.toml")))
    .digest("hex");
  const peerHash = createHash("sha256")
    .update(readFileSync(join(manifest.peerVault, "brainnotes.toml")))
    .digest("hex");

  expect(localHash).not.toBe(peerHash);
  expect(status.source_hash).toBe(localHash);
});

// The suite's one mutating test, and it is here rather than in smoke.spec.js on
// purpose. Every other containment claim in this file is read-shaped — "the peer
// vault is still clean", "the server would not serve PeerOnly" — and a read-only
// run proves nothing about where a write would land. This drives the real write
// path the server exists for, from the browser, and then identifies the vault by
// looking for the bytes.
//
// It also replaces the check that used to stand here, which asserted
// manifest.localVault was under the temporary directory. That was
// self-referential: prepare.mjs chose that path, so the assertion could only
// fail if prepare.mjs contradicted itself, and it would stay green if `bn serve`
// resolved a completely different vault. The path checked below is the path the
// *server* put bytes in.
test("a capture typed in the browser lands in the fixture vault and nowhere else", async ({
  page,
}) => {
  const inbox = join(manifest.localVault, manifest.inboxNote);
  const peerInbox = join(manifest.peerVault, manifest.inboxNote);
  const before = readFileSync(inbox, "utf8");
  const peerBefore = readFileSync(peerInbox, "utf8");
  expect(gitStatus(manifest.localVault), "the local vault did not start clean").toBe("");
  expect(gitStatus(manifest.peerVault), "the peer vault did not start clean").toBe("");

  // A phrase no other file on this machine contains, so finding it identifies
  // this run's write rather than any pre-existing text.
  const phrase = `e2e capture ${manifest.fixtureRoot.split("/").pop()}`;

  await page.goto("/");
  // The home page's own scripts fetch the daily brief on load, and serving that
  // writes .bnotes/brief.json through vaultstate.Save, which holds the vaultgit
  // safety lock. A capture posted into that window is refused with "another
  // BrainNotes process is already performing a Git safety operation" -- correct
  // product behaviour, and a user simply clicks again, but in a suite it is a
  // race against page startup. Waiting for the page to go quiet writes *after*
  // the page has finished its own work, which is also what a person does.
  await page.waitForLoadState("networkidle");

  await page.locator("[data-capture-input]").fill(phrase);
  await page.locator("[data-capture-submit]").click();
  await expect(page.locator("[data-capture-status]")).toHaveClass(/is-success/);
  await expect(page.locator("[data-capture-recent] .capture-recent-item").first()).toContainText(
    phrase,
  );

  const after = readFileSync(inbox, "utf8");
  expect(after, "the browser reported success but the note on disk is unchanged").not.toBe(before);
  expect(after).toContain(phrase);
  expect(after.indexOf(phrase)).toBeGreaterThan(after.indexOf(manifest.captureHeading));

  // The server wrote here, so this is the vault it resolved -- not a path this
  // harness asserted about itself.
  expectThrowawayStorage(inbox, "the note the server actually wrote");
  expectThrowawayStorage(manifest.peerVault, "the peer vault");

  expect(readFileSync(peerInbox, "utf8"), "the capture reached the peer vault too").toBe(peerBefore);
  expect(gitStatus(manifest.peerVault), "the peer vault changed during a local capture").toBe("");
  // vaultgit snapshots the write, so the local vault is clean again but ahead by
  // a commit. A dirty tree here would mean the snapshot did not happen.
  expect(gitStatus(manifest.localVault), "the capture was written but never snapshotted").toBe("");
});

test("the peer vault is not reachable through the served vault", async ({ page, request }) => {
  const before = gitStatus(manifest.peerVault);
  expect(before, "the peer vault did not start clean").toBe("");

  // PeerOnly.md exists in the peer vault and nowhere else. A server that could
  // serve it would be serving a vault this one is supposed to know nothing
  // about.
  expect((await request.get(`/n/PeerOnly`)).status()).toBe(404);

  await page.goto("/library");
  // Scoped to the server-rendered note list, and paired with a positive. A
  // `not.toContainText` against the whole `body` passes on a blank page, on an
  // error page, and on a page whose list failed to render -- every way of
  // showing no notes at all satisfies "PeerOnly is absent". Asserting that a
  // seeded note *is* listed is what makes the absence mean something.
  const recent = page.locator(".recent-list");
  await expect(recent).toContainText("Alpha");
  await expect(recent).not.toContainText("PeerOnly");

  expect(gitStatus(manifest.peerVault), "the peer vault changed during the browser session").toBe("");
});

test("no request leaves the fixture origin", async ({ page, foreignRequests }) => {
  for (const path of ["/", "/library", "/n/Alpha", "/health"]) {
    await page.goto(path);
  }
  await page.waitForLoadState("networkidle");

  expect(foreignRequests, "the pages loaded something from outside the fixture server").toEqual([]);
});

// Without this test, the one above is satisfied by a guard that never fires.
//
// The earlier version of this test fetched a host in the reserved .invalid TLD
// and asserted the fetch rejected. That assertion was tautological: a `.invalid`
// host fails DNS resolution, so the fetch rejects whether the guard exists or
// not, and the test would have passed with `page.route` deleted. What is used
// here instead is a real, reachable, listening server on another loopback port.
// It is a different origin from the fixture, so the guard must stop it — and
// "did it arrive" is a fact the beacon reports rather than an inference from an
// exception.
test("the origin guard stops a request that really could have been delivered", async ({
  page,
  foreignRequests,
}) => {
  const beacon = await startBeacon();
  try {
    await page.goto("/");

    const targets = [
      // A plainly foreign origin, reachable and listening.
      `http://127.0.0.1:${beacon.port}/beacon-plain`,
      // And the production shape this suite exists to keep out. Nothing can
      // observe its arrival, which is exactly why it is not the load-bearing
      // case here.
      "https://brainnotes.production.invalid/api/ready",
    ];

    await page.evaluate(async (urls) => {
      for (const url of urls) {
        try {
          await fetch(url, { mode: "no-cors", cache: "no-store" });
        } catch {
          // Blocked or unresolvable; which one is not this expression's business.
        }
      }
    }, targets);

    // The same destination, dressed up to *begin with* the fixture origin:
    // everything before the `@` is userinfo, so this is a request to the beacon.
    // A guard that prefix-matched the origin string admits it, and the beacon
    // then records the arrival.
    //
    // A navigation rather than a fetch, and that is the whole reason this is a
    // separate step. Chromium refuses to construct a Request from a URL carrying
    // credentials, so the fetch above would throw inside the page before the
    // guard was consulted at all -- the attempt would never reach page.route,
    // the beacon would stay empty no matter what the guard did, and the case
    // would look covered while testing nothing. Top-level navigations do carry
    // userinfo, so this vector actually exercises the decision.
    await page
      .goto(`http://127.0.0.1:${manifest.port}@127.0.0.1:${beacon.port}/beacon-userinfo`)
      .catch(() => {
        // Blocking a navigation rejects the goto. That the guard blocked it is
        // asserted below, from the beacon, not from this exception.
      });
    // Keep this navigation last in the test. The blocked page commits
    // `chrome-error://chromewebdata/` asynchronously, *after* goto rejects, so a
    // later page action here races it and fails with "interrupted by another
    // navigation". Assertions on beacon.hits and foreignRequests touch no page.

    expect(beacon.hits, "a request reached a server outside the fixture origin").toEqual([]);
    expect(
      foreignRequests.some((url) => url.includes(`127.0.0.1:${beacon.port}`)),
      `the guard did not record an attempt to reach 127.0.0.1:${beacon.port}`,
    ).toBe(true);
    expect(foreignRequests).toContain("https://brainnotes.production.invalid/api/ready");
  } finally {
    await beacon.close();
  }
});

// startBeacon listens on a loopback port and records the paths it is asked for.
// It answers nothing useful; its only output is the list of requests that got
// through, which is empty when the guard works.
function startBeacon() {
  const hits = [];
  const server = createServer((req, res) => {
    hits.push(req.url);
    res.writeHead(204, { "Access-Control-Allow-Origin": "*" });
    res.end();
  });
  return new Promise((resolveBeacon, reject) => {
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      resolveBeacon({
        port: server.address().port,
        hits,
        close: () => new Promise((closed) => server.close(closed)),
      });
    });
  });
}

test("the fixture server is bound to loopback only", async () => {
  const external = Object.values(networkInterfaces())
    .flat()
    .filter((iface) => iface.family === "IPv4" && !iface.internal)
    .map((iface) => iface.address);
  test.skip(external.length === 0, "this machine has no non-loopback IPv4 address to probe");

  for (const address of external) {
    await expect(reachable(address, manifest.port)).resolves.toBe(false);
  }
});

// reachable resolves true only if a TCP connection to address:port is
// established. A refusal and a timeout are both "not reachable"; the point is
// that `bn serve --addr 127.0.0.1:<port>` did not also bind the network.
function reachable(address, port) {
  return new Promise((resolveReachable) => {
    const socket = connect({ host: address, port, timeout: 1500 });
    const settle = (value) => {
      socket.destroy();
      resolveReachable(value);
    };
    socket.once("connect", () => settle(true));
    socket.once("timeout", () => settle(false));
    socket.once("error", () => settle(false));
  });
}
