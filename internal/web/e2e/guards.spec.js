// The harness's own safety predicates, tested directly.
//
// Everything else in this directory tests the application. These two functions
// are the harness testing itself, and they are the two places where a quiet bug
// would not surface as a red suite — it would surface as a suite that keeps
// passing while it stops isolating:
//
//   isFixtureOrigin              decides which requests the browser may make.
//                                Too permissive and "no request leaves the
//                                fixture origin" stays green with traffic
//                                leaving the fixture origin.
//   refuseFixtureStorageInsideCheckout
//                                decides where the disposable world may live.
//                                Too permissive and `bn serve` finds this
//                                checkout, rebuilds the bundle into the working
//                                tree, and the suite tests what it just built.
//
// The functions under test need neither a browser nor a server. These tests run
// here rather than under Vitest so that every file in this directory is reachable
// by the one command that owns it — `npm run test:e2e` — instead of isolation
// logic being tested by a gate that never launches a browser. The cost is that
// importing `test` from fixture.js brings the auto `foreignRequests` fixture with
// it, so Playwright does build a page for each of these; they simply never use it.

import { mkdtempSync, realpathSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { expect, isFixtureOrigin, manifest, test } from "./fixture.js";
import { REPO_ROOT } from "./manifest.mjs";
import { refuseFixtureStorageInsideCheckout } from "./prepare.mjs";

const FIXTURE_ORIGIN = "http://127.0.0.1:53211";

test("isFixtureOrigin admits the fixture and nothing that merely resembles it", () => {
  for (const url of [
    `${FIXTURE_ORIGIN}/`,
    `${FIXTURE_ORIGIN}/api/ready`,
    `${FIXTURE_ORIGIN}/n/Alpha?highlight=1#h`,
    // Inline payloads never leave the page, so they are not traffic.
    "data:text/css,body{}",
    "blob:http://127.0.0.1:53211/8f0e",
  ]) {
    expect(isFixtureOrigin(url, FIXTURE_ORIGIN), `${url} should be admitted`).toBe(true);
  }

  for (const url of [
    // Everything before the `@` is userinfo: this is a request to evil.example.
    // This is the whole prefix-bypass class — see isFixtureOrigin — and the one
    // case here that a `startsWith` guard would admit.
    `${FIXTURE_ORIGIN}@evil.example/x`,
    // Prefix-matches the fixture origin and is not a URL: the authority splits
    // at the last `:`, so the "port" is `53211.evil.example`, which is not
    // digits, and parsing throws. It is here to pin the *unparseable* branch
    // against an input that resembles the fixture, rather than only against
    // obvious junk like the two at the end of this list.
    "http://127.0.0.1:53211.evil.example/x",
    // Right host, wrong port — a different origin, and the case a naive
    // hostname check misses.
    "http://127.0.0.1:53212/api/ready",
    // Right host and port, wrong scheme.
    "https://127.0.0.1:53211/api/ready",
    "https://brainnotes.production.invalid/api/ready",
    // Not a URL at all. Unprovable is not the fixture.
    "not a url",
    "",
  ]) {
    expect(isFixtureOrigin(url, FIXTURE_ORIGIN), `${url} should be refused`).toBe(false);
  }
});

test("refuseFixtureStorageInsideCheckout admits throwaway storage", () => {
  // The path this run is actually using, so the predicate is exercised against
  // the real arrangement rather than a constructed one.
  expect(() => refuseFixtureStorageInsideCheckout(manifest.fixtureRoot)).not.toThrow();

  // Removed rather than left behind: cleanup.mjs only knows about the manifest's
  // fixtureRoot, and a suite whose thesis is disposable storage should not leak a
  // directory per run.
  const another = mkdtempSync(join(realpathSync(tmpdir()), "bn-e2e-guard-"));
  try {
    expect(() => refuseFixtureStorageInsideCheckout(another)).not.toThrow();
  } finally {
    rmSync(another, { recursive: true, force: true });
  }
});

test("refuseFixtureStorageInsideCheckout refuses storage under the checkout", () => {
  const checkout = realpathSync(REPO_ROOT);

  for (const candidate of [
    // The repo-local $TMPDIR arrangement this exists for.
    join(checkout, "tmp", "bn-e2e-abc123"),
    // Directly inside the tree `serveWebProjectRoot` would find.
    join(checkout, "internal", "web", "e2e", ".artifacts", "bn-e2e-abc123"),
    // A first component that begins with two dots without being `..`. This is
    // inside the checkout, but `relative()` reports it as `..cache/…`, which an
    // unanchored `startsWith("..")` containment test reads as an escape and
    // admits. The anchoring in insideCheckout is what this case pins.
    join(checkout, "..cache", "bn-e2e-abc123"),
    // The checkout itself.
    checkout,
  ]) {
    expect(
      () => refuseFixtureStorageInsideCheckout(candidate),
      `${candidate} is inside the checkout and must be refused`,
    ).toThrow(/inside the checkout/);
  }
});
