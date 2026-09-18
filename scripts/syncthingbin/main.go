// Command syncthingbin prints the path of the pinned Syncthing binary, fetching
// it once if this machine has never had it.
//
// It exists so that `just test-sync-transport` can provision before the test
// binary starts. internal/vaultfixture rewrites HOME to isolate a run, so a
// download started from inside a test would write its cache into a directory the
// teardown removes, and every run would pay for the network again. Resolving out
// here and handing the path over in BNOTES_SYNCTHING_BIN keeps the network on one
// side of the gate and the vaults on the other.
//
//	go run ./scripts/syncthingbin
//
// It prints one line: the absolute path. Anything it has to say to a human goes
// to stderr, so the output substitutes cleanly into a shell assignment.
package main

import (
	"fmt"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/syncthingbin"
)

func main() {
	path, err := syncthingbin.Resolve()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(path)
}
