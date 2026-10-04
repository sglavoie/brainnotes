#!/usr/bin/env node
// Build the disposable world `npm run test:e2e` runs against, and write the
// manifest playwright.config.js reads.
//
// This runs before `playwright test` rather than from the config, because a
// Playwright config is re-evaluated in every worker process: building a binary
// and mkdtemp-ing a vault from there would produce one world per worker and the
// specs would assert against a server none of them built. services/pi-monitoring
// in the deployment repository solves the same problem the same way — a
// prepare step writes a manifest, the config only reads it.
//
// What is reused and what is not, stated precisely, because the difference
// matters more than the intention. The **refusal that decides whether a `bn`
// process may write to a vault** is not re-implemented here: the server is
// launched through scripts/vault_sandbox.py, which scrubs $BNOTES_VAULT from
// the child, passes --vault explicitly, asks `bn config` which root it
// *actually* resolved, and refuses with exit 2 unless that root is the sandbox
// this run asserted. That is the guard the 214-note incident is about, it has
// one implementation, and this harness is a caller of it.
//
// The **seeding** below — notes, git identity, the Unrelated.md sentinel — is
// necessarily a second expression of what internal/vaultfixture does in Go,
// because a Node process cannot call into a Go package. It is fixture
// construction rather than a safety decision: if it drifts, a spec asserts
// against slightly different notes, which a failing spec reports. Nothing here
// decides whether a write is allowed.
//
// refuseFixtureStorageInsideCheckout below is the one refusal this file does
// own, and it exists because no other layer can make it: see its comment.

import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, isAbsolute, join, relative, sep } from "node:path";
import { fileURLToPath } from "node:url";

import { ARTIFACTS_DIR, MANIFEST_PATH, REPO_ROOT } from "./manifest.mjs";

// The sentinel every fixture vault carries, spelled the same way as
// internal/vaultfixture's UnrelatedNote so the two harnesses describe one vault.
const UNRELATED_NOTE = "Unrelated.md";

// PEER_ONLY_NOTE exists in the peer vault and nowhere else. A server that can
// serve it is serving the wrong vault.
const PEER_ONLY_NOTE = "PeerOnly.md";

// INBOX_NOTE is the capture destination. The suite needs one real write path it
// can drive from the browser: read-only containment ("the peer vault is still
// clean") says nothing while nothing in the run ever writes.
const INBOX_NOTE = "Inbox.md";
const CAPTURE_HEADING = "## Captured";

// FIELDWORK_NOTE is findable by a word that appears only in its body, and never
// in its path. That is the one shape the wikilink completion cannot be checked
// without: a completion's label is the note's *path*, so a client that
// re-matches what was typed against its own labels throws away every result the
// vault matched on content — which is most of what the search is for.
const FIELDWORK_NOTE = "Notes/Fieldwork.md";

// ACTIVE_MEDIA are deliberately hostile same-origin attachments. The hosted
// qualification asks the running server for these exact bytes and proves the
// response forces a download under a sandbox instead of letting either format
// execute with the vault page's authority.
const ACTIVE_MEDIA = {
  "attachments/active.html":
    "<!doctype html><script>window.parent.document.body.dataset.mediaExecuted = 'html'</script>\n",
  "attachments/active.svg":
    '<svg xmlns="http://www.w3.org/2000/svg" onload="window.parent.document.body.dataset.mediaExecuted=\'svg\'"></svg>\n',
};

const SEED_NOTES = {
  "Reading.md": "---\nstatus: draft\ntitle: Reading\nlast: 2026-10-03\n---\n\n## First section\n\n" +
    "A paragraph for scrolling through this note.\n\n".repeat(35) +
    "## Second section\n\n```go\npackage main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n```\n\n" +
    "Another paragraph below the code.\n\n".repeat(25),
  "Alpha.md": "---\nstatus: draft\ntitle: Alpha\n---\n\nAlpha body, see [[Beta]].\n",
  "Beta.md": "---\nstatus: draft\ntitle: Beta\n---\n\nBeta body.\n",
  "Notes/Gamma.md": "---\nstatus: draft\ntitle: Gamma\n---\n\nGamma body.\n",
  [FIELDWORK_NOTE]: "---\nstatus: draft\ntitle: Fieldwork\n---\n\nCartographer notes live here.\n",
  [INBOX_NOTE]: `---\nstatus: draft\ntitle: Inbox\n---\n\n${CAPTURE_HEADING}\n`,
  [UNRELATED_NOTE]: "---\nstatus: draft\ntitle: Unrelated\n---\n\nNobody under test writes this.\n",
};

// Each vault's brainnotes.toml carries a line no other file on this machine
// has. /api/config/status publishes the SHA-256 of the file the running server
// loaded, so that hash identifies *which* vault is being served — an assertion
// "the page rendered a note called Alpha" cannot make, because the real vault
// could have one too.
function vaultConfig(marker) {
  return `# e2e fixture: ${marker}
# The background committer is off. \`bn serve\` otherwise polls the working tree
# every five seconds and snapshots it, taking the same vaultgit safety lock a
# capture needs; a capture that lands mid-poll fails with "another BrainNotes
# process is already performing a Git safety operation on this vault". That is
# correct product behaviour and a real user retries, but in a suite it is a race
# whose outcome depends on when the click happens. What this suite asserts is
# where a *user-initiated* write lands, so the autosave timer is removed rather
# than raced with.
[autosave]
min_interval = "off"

[capture]
inbox_note = "${INBOX_NOTE}"
heading    = "${CAPTURE_HEADING}"

[frontmatter.properties.status]
type = "string"

[frontmatter.properties.tags]
type  = "list"
items = "string"
`;
}

function run(command, args, cwd, env) {
  execFileSync(command, args, { cwd, env, stdio: ["ignore", "pipe", "pipe"], encoding: "utf8" });
}

// gitEnv is built rather than inherited so a developer's locale, identity, or
// global core.excludesFile cannot change what a fixture commit contains.
function gitEnv(home) {
  return {
    PATH: process.env.PATH,
    HOME: home,
    TMPDIR: process.env.TMPDIR ?? "",
    GIT_CONFIG_GLOBAL: join(home, ".gitconfig"),
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_TERMINAL_PROMPT: "0",
    LC_ALL: "C",
    TZ: "UTC",
    GIT_AUTHOR_NAME: "Fixture User",
    GIT_AUTHOR_EMAIL: "fixture@example.invalid",
    GIT_COMMITTER_NAME: "Fixture User",
    GIT_COMMITTER_EMAIL: "fixture@example.invalid",
  };
}

function buildVault(root, name, home, marker, files) {
  mkdirSync(root, { recursive: true });
  writeFileSync(join(root, ".gitignore"), "brainnotes.local.toml\n");
  writeFileSync(join(root, "brainnotes.toml"), vaultConfig(marker));
  writeFileSync(
    join(root, "brainnotes.local.toml"),
    `[open]\neditor = "private-${name}-${marker}"\n`,
  );
  for (const [rel, body] of Object.entries(files)) {
    const path = join(root, rel);
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, body);
  }
  const env = gitEnv(home);
  run("git", ["-c", "init.defaultBranch=main", "init", "-q"], root, env);
  run("git", ["config", "user.name", "Fixture User"], root, env);
  run("git", ["config", "user.email", "fixture@example.invalid"], root, env);
  run("git", ["add", "-A"], root, env);
  run("git", ["commit", "-q", "-m", `${name} fixture`], root, env);
}

// seedSync enrolls the vault pair and leaves five conflicts and one blocked
// dependency waiting in the served vault's incoming channel, then returns what it
// built so the manifest can carry it.
//
// The work is a Go program (scripts/syncfixture) rather than more of this file
// because it cannot be anything else: a hold is only real once
// internal/vaultimport has refused to apply a package, and a package is only real
// once internal/vaultsync's Builder has produced one. Seeding the state file by
// hand from here would give sync.spec.js a review page that agrees with this
// harness instead of with the engine, which is the one thing that spec is for.
//
// It runs *after* both vaults are committed, so everything it writes into the
// served vault is either committed by it or under `.bnotes/sync/` — and every
// other spec's `gitStatus` assertion still sees a clean tree.
function seedSync(fixtureRoot, localVault, peerVault, home) {
  const expected = join(fixtureRoot, "expected");
  const out = execFileSync(
    "go",
    [
      "run",
      "./scripts/syncfixture",
      `-local=${localVault}`,
      `-peer=${peerVault}`,
      `-home=${home}`,
      `-sandbox=${fixtureRoot}`,
      `-expected=${expected}`,
    ],
    { cwd: REPO_ROOT, env: process.env, stdio: ["ignore", "pipe", "inherit"], encoding: "utf8" },
  );
  return JSON.parse(out);
}

// reservePort returns a loopback port that was free a moment ago. The gap
// before `bn serve` binds it is a real race and the reason this is 127.0.0.1:0
// rather than a constant: a fixed port would collide with whatever a developer
// already has serving, and the suite would assert against that server instead.
function reservePort() {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.once("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => resolve(port));
    });
  });
}

// insideCheckout answers whether `candidate` is this checkout or lives under it.
//
// The `..` test is anchored on purpose. `relative("/a", "/a/..cache")` returns
// `"..cache"`, and the unanchored `startsWith("..")` that reads naturally here
// would take that for an escape — admitting a directory that is in fact inside
// the checkout. Only `..` itself and `../…` mean "outside".
function insideCheckout(candidate) {
  const rel = relative(realpathSync(REPO_ROOT), candidate);
  if (rel === "") {
    return true; // the checkout itself
  }
  if (isAbsolute(rel)) {
    return false; // no relative path connects them at all
  }
  return rel !== ".." && !rel.startsWith(`..${sep}`);
}

// refuseFixtureStorageInsideCheckout is this file's own refusal, and no other
// layer can make it. vault_sandbox.py decides which *vault* `bn` may write; this
// decides where the whole disposable world is allowed to live, which that script
// never sees. `bn serve` looks for a web project root in *every* ancestor of its
// working directory (internal/web serveWebProjectRoot), so a fixture root under
// this checkout means the server finds the checkout, rebuilds
// internal/web/static/app.js and app.css into the working tree, and serves those
// — the suite would stop testing the shipped bundle and would dirty tracked
// files to do it, both silently, with every spec still green.
//
// This is not hypothetical: $TMPDIR pointing at a repo-local ./tmp is a normal
// CI arrangement, and mkdtemp obeys it.
export function refuseFixtureStorageInsideCheckout(candidate) {
  if (!insideCheckout(candidate)) {
    return;
  }
  throw new Error(
    `refusing to build the e2e fixture under ${candidate}: it is inside the checkout ${realpathSync(REPO_ROOT)}.\n` +
      `\`bn serve\` would find this repository by walking up from it, rebuild internal/web/static/app.js\n` +
      `and app.css into the working tree, and serve those instead of the bundle the binary ships.\n` +
      `The temporary directory decides this: $TMPDIR is ${process.env.TMPDIR ?? "(unset)"}. Point it outside the checkout.`,
  );
}

export async function prepare() {
  rmSync(ARTIFACTS_DIR, { recursive: true, force: true });
  mkdirSync(ARTIFACTS_DIR, { recursive: true });

  // realpathSync because macOS reaches the temp directory through
  // /var -> /private/var, and vault_sandbox.py compares resolved paths.
  //
  // The refusal is made on the temporary root, *before* mkdtemp, rather than on
  // the fixture root after it. Same decision — mkdtemp appends exactly one
  // component, so the fixture root is inside the checkout if and only if the
  // temporary root is — but a refusal that has already created its target has
  // left behind exactly the artifact it exists to prevent, and nothing here
  // would remove it. internal/vaultfixture states the same ordering rule.
  const temporaryRoot = realpathSync(tmpdir());
  refuseFixtureStorageInsideCheckout(temporaryRoot);
  const fixtureRoot = mkdtempSync(join(temporaryRoot, "bn-e2e-"));
  const home = join(fixtureRoot, "home");
  mkdirSync(home, { recursive: true });

  const bn = join(fixtureRoot, "bn");
  // An unstamped binary: `just build` bakes this checkout's path in so a
  // developer's `bn` rebuilds web assets here before serving. That would have
  // the suite write into the working tree and serve the result, when what needs
  // verifying is the bundle the binary actually ships.
  run("go", ["build", "-o", bn, "./cmd/bn"], REPO_ROOT, process.env);

  const marker = fixtureRoot.split("/").pop();
  const localVault = join(fixtureRoot, "local");
  const peerVault = join(fixtureRoot, "peer");
  const hostedVault = join(fixtureRoot, "hosted");
  buildVault(localVault, "local", home, `local ${marker}`, { ...SEED_NOTES, ...ACTIVE_MEDIA });
  buildVault(peerVault, "peer", home, `peer ${marker}`, {
    ...SEED_NOTES,
    ...ACTIVE_MEDIA,
    [PEER_ONLY_NOTE]: "---\ntitle: PeerOnly\n---\n\nOnly the peer device has this note.\n",
  });
  buildVault(hostedVault, "hosted", home, `hosted ${marker}`, {
    ...SEED_NOTES,
    ...ACTIVE_MEDIA,
  });

  const sync = seedSync(fixtureRoot, localVault, peerVault, home);

  const port = await reservePort();
  const hostedPort = await reservePort();
  const readOnlyPort = await reservePort();
  const readOnlyState = join(fixtureRoot, "read-only-state");
  mkdirSync(readOnlyState, { recursive: true });
  const manifest = {
    repoRoot: REPO_ROOT,
    fixtureRoot,
    home,
    bn,
    localVault,
    peerVault,
    hostedVault,
    readOnlyState,
    host: "127.0.0.1",
    port,
    hostedPort,
    readOnlyPort,
    baseURL: `http://127.0.0.1:${port}`,
    hostedBaseURL: `http://127.0.0.1:${hostedPort}`,
    readOnlyBaseURL: `http://127.0.0.1:${readOnlyPort}`,
    activeMedia: Object.keys(ACTIVE_MEDIA),
    unrelatedNote: UNRELATED_NOTE,
    peerOnlyNote: PEER_ONLY_NOTE,
    inboxNote: INBOX_NOTE,
    captureHeading: CAPTURE_HEADING,
    seedNotes: Object.keys(SEED_NOTES),
    // Deliberately not folded into seedNotes: those are the notes every vault is
    // supposed to serve, and these are one side of a conflict — one of them a note
    // the incoming operation deletes.
    sync,
  };
  writeFileSync(MANIFEST_PATH, `${JSON.stringify(manifest, null, 2)}\n`);
  return manifest;
}

// Only when run as a command. guards.spec.js imports the refusal above to test
// it, and an import that also built a whole fixture world would make asserting
// on the refusal cost a Go build and two Git repositories.
if (process.argv[1] && realpathSync(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const manifest = await prepare();
  console.log(`e2e fixture ready: ${manifest.localVault} served on ${manifest.baseURL}`);
}
