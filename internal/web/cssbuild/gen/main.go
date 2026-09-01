// Command gen regenerates internal/web/static/app.css from its parts. It is the
// stylesheet half of `just web-build`, and it runs from the repository root.
package main

import (
	"fmt"
	"os"

	"github.com/sglavoie/sglavoie_brainnotes/internal/web/cssbuild"
)

func main() {
	root, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cssbuild: determine working directory: %v\n", err)
		os.Exit(1)
	}

	changed, err := cssbuild.WriteBundle(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if changed {
		fmt.Printf("wrote %s\n", cssbuild.OutputFile)
		return
	}
	fmt.Printf("%s already current\n", cssbuild.OutputFile)
}
