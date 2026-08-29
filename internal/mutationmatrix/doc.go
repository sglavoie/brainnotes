// Package mutationmatrix holds no production code.
//
// It exists because bn's write safety is a property of the whole system rather
// than of any one package. internal/vault refuses a forbidden location,
// internal/vaultgit records exactly the paths a change touched, and
// internal/noteops is the seam that makes a caller go through both. Each of
// those has thorough tests of its own, and none of them can answer the question
// that actually matters to a person with a vault: does the command I just ran
// get the guarantees, end to end, through the layer I reached it by?
//
// The test here answers that question the same way for every writer, from one
// table. Each entry names a real production entry point — a CLI command run
// through the Cobra tree, an HTTP route served by internal/web, a note mutation,
// a restore, a durable state write — and the same six assertions run against all
// of them:
//
//  1. A vault on refused storage fails before any artifact exists.
//  2. A vault on ordinary storage mutates.
//  3. An operation with nothing to do leaves no empty commit.
//  4. A successful mutation adds exactly one local commit, holding only the
//     paths the operation touched and the journal entry that authorizes them.
//  5. An unrelated edit sitting in the working tree is not swept into it.
//  6. A snapshot that fails leaves the change on disk, says so, and refuses the
//     next mutation.
//
// The value is in the uniformity. A new mutating command is one table row away
// from being covered, and a row that cannot be written is the signal that the
// command bypassed the seam — which is the same thing internal/audit checks
// from the source side, arrived at from the other direction.
package mutationmatrix
