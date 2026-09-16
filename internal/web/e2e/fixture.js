// The shared test object for the browser suite.
//
// Every test gets an origin guard, always on. A browser is not a jsdom stub: a
// page it renders will follow whatever a stylesheet, a script tag, or a fetch
// asks it to, and this project's production surface names a Pi over Cloudflare
// Access. "The smoke tests cannot reach production" has to be enforced rather
// than intended, so requests leaving the fixture origin are aborted and
// recorded, and a spec asserts the recording is empty.

import { test as base, expect } from "@playwright/test";

import { readManifest } from "./manifest.mjs";

export const manifest = readManifest();

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
