package cli

import (
	"fmt"
	"io"
)

func writeSchemaHint(w io.Writer, cmdName, configPath string) {
	fmt.Fprintf(w, "%s: no schema defined in config.\n", cmdName)
	if configPath != "" {
		fmt.Fprintf(w, "\n  Config: %s\n", configPath)
	}
	fmt.Fprint(w, `
  Add a [frontmatter.schema] section, for example:

    [frontmatter.schema.status]
    type     = "string"
    required = true
    allowed  = ["WIP", "Done", "Archived"]

  To open the config in your editor, run: bn config edit
`)
}
