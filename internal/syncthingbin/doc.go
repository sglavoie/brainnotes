// Package syncthingbin resolves the one Syncthing build the transport gate runs.
//
// The transport tests are the only place in this repository that runs somebody
// else's program against a vault, and the value of that test is entirely in
// *which* program it was. A harness that picked up whatever `syncthing` happened
// to be on the developer's PATH would pass or fail for reasons nobody could
// reconstruct: the protocol, the configuration schema and the REST surface this
// codebase writes against all move between releases. So the version is pinned
// here, the release archive is verified against a checksum recorded here, and a
// binary that reports any other version is refused rather than used.
//
// The provisioning is deliberately *not* `go install`. Syncthing's module
// carries replace directives, which `go install pkg@version` refuses outright,
// so the only reproducible source is the published release archive. Verifying
// its SHA-256 against the table in this file is what makes downloading one
// acceptable at all.
//
// A cache hit is held to the same standard as the download that filled it. The
// recorded checksums are of archives, so install writes the digest of the bytes
// it extracted beside the binary and a later run re-hashes the file and compares;
// an unrecorded or altered binary is reinstalled rather than run.
//
// This package does no testing and imports no testing package: `just
// test-sync-transport` provisions through scripts/syncthingbin *before* the test
// binary runs, so the download happens once for a whole tagged run rather than
// once per test. Resolving from inside a test still works and still caches
// outside the fixture — CacheRoot reads this machine's cache directory at
// initialization, before internal/vaultfixture rewrites HOME — but each test
// process then pays for its own resolution. The provisioned path reaches the
// tests through BNOTES_SYNCTHING_BIN, and a test that cannot get a binary at all
// fails rather than skips.
package syncthingbin

// Version is the pinned Syncthing release. It is one half of a pair: the
// configuration internal/synctransport writes declares a schema version that
// this release understands, and moving either without the other is a change to
// both.
const Version = "v1.30.0"

// EnvBinary names an already-provisioned binary, which is how the justfile hands
// its work to the test binary and how an operator supplies an offline copy.
//
// A binary named this way is still checked: it must report Version. That is a
// weaker guarantee than the archive checksum below — a differently-built v1.30.0
// would be accepted, and no digest is recorded for something this package did not
// install — and it is the guarantee the pin is actually about.
const EnvBinary = "BNOTES_SYNCTHING_BIN"

// EnvCache relocates the download cache, which exists so that CI can point it at
// a restorable directory and so that a test never writes to a real home.
const EnvCache = "BNOTES_SYNCTHING_CACHE"

// release is one published archive: what to fetch, and what it must hash to.
type release struct {
	asset  string
	sha256 string
}

// releases is the verified table, keyed by GOOS/GOARCH.
//
// Every entry here was checked against the SHA256SUMS file published with the
// release. A platform that is missing is not "unsupported Syncthing" — it is a
// platform nobody has verified a checksum for, and resolving on it fails with
// that sentence rather than silently downloading something unverified.
var releases = map[string]release{
	"darwin/amd64": {
		asset:  "syncthing-macos-amd64-" + Version + ".zip",
		sha256: "eb375302c79b89b85f32f014e451430efe3a9723b37639698310344a9029799e",
	},
	"darwin/arm64": {
		asset:  "syncthing-macos-arm64-" + Version + ".zip",
		sha256: "dd42cc7a88d08779c305e5f6f2d8bc8dec2c97652c1380679c672d4febd63f8f",
	},
	"linux/amd64": {
		asset:  "syncthing-linux-amd64-" + Version + ".tar.gz",
		sha256: "a2edc833d6dd71a76b7c481dbcc81f8be37da9eb83993c512bf53eb096ba7a73",
	},
	"linux/arm64": {
		asset:  "syncthing-linux-arm64-" + Version + ".tar.gz",
		sha256: "4655e260e94fa5e0110084040751bd0274acdeb74653933f909036e788a911a1",
	},
	"linux/arm": {
		asset:  "syncthing-linux-arm-" + Version + ".tar.gz",
		sha256: "70beb4c43a752d3b11a780bf2ca2d1a3b50f9f74e0b6a9cd35b7f204fea375e1",
	},
}

// downloadBase is the release host. It is a constant so that the one network
// address this repository fetches from is greppable.
const downloadBase = "https://github.com/syncthing/syncthing/releases/download/"
