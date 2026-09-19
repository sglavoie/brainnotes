// Command syncfixture seeds the synchronization conflicts
// internal/web/e2e/sync.spec.js drives, and prints what it seeded as JSON on
// stdout for prepare.mjs to fold into the fixture manifest.
//
// It is Go rather than another hundred lines of prepare.mjs because nothing else
// can be. A hold is only real once internal/vaultimport has refused to apply a
// package, and a package is only real once internal/vaultsync's Builder has
// produced one — so a hand-written `.bnotes/sync/conflicts` state file would give
// the browser a review page that agrees with *this* program rather than with the
// engine, and agreeing with the engine is the entire reason a browser spec exists.
//
// The isolation rule is borrowed rather than restated, which is the standing
// instruction for it: every directory this program writes to goes through
// vaultfixture.IsolationError before anything is created, and $BNOTES_VAULT is
// scrubbed first, because a `cd` does not override it. What cannot be borrowed is
// vaultfixture's fixture *construction* — New, NewPair and Clock all take a
// *testing.T — so the seeding below is this program's own, the same way
// prepare.mjs's seeding is.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultfixture"
)

// paths are the four directories this program writes to and the sandbox every one
// of them has to be inside.
type paths struct {
	local    string
	peer     string
	home     string
	sandbox  string
	expected string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "syncfixture: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var p paths
	flag.StringVar(&p.local, "local", "", "the vault the browser is served, which receives every package")
	flag.StringVar(&p.peer, "peer", "", "the vault whose identity produces every package")
	flag.StringVar(&p.home, "home", "", "the fixture HOME every git invocation is given")
	flag.StringVar(&p.sandbox, "sandbox", "", "the disposable fixture root every path above must be inside")
	flag.StringVar(&p.expected, "expected", "", "a directory outside both vaults for the bytes the spec compares against")
	flag.Parse()

	if err := vaultfixture.ScrubProcessEnv(); err != nil {
		return err
	}
	if err := confine(p); err != nil {
		return err
	}
	seeded, err := seed(p)
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(seeded, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

// confine refuses every directory this program writes to before it writes to any
// of them.
//
// The ordering is the one internal/vaultfixture states and prepare.mjs repeats: a
// refusal that has already created its target has left behind exactly the
// artifact it exists to prevent, and nothing here would remove it.
func confine(p paths) error {
	for _, candidate := range []struct{ label, path string }{
		{"the served vault", p.local},
		{"the producing vault", p.peer},
		{"the fixture home", p.home},
		{"the expected-bytes directory", p.expected},
	} {
		if err := vaultfixture.IsolationError(candidate.path, p.sandbox); err != nil {
			return fmt.Errorf("%s: %w", candidate.label, err)
		}
	}
	return nil
}
