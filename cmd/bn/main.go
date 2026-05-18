// Command bn is the entrypoint for Brain Notes, a CLI for plain-markdown vaults.
package main

import (
	"fmt"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "bn:", err)
		os.Exit(1)
	}
}
