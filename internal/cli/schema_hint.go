package cli

import (
	"fmt"
	"io"
)

func writeSchemaHint(w io.Writer, cmdName, configPath string) {
	fmt.Fprintf(w, "%s: no frontmatter properties defined in config.\n", cmdName)
	if configPath != "" {
		fmt.Fprintf(w, "\n  Config: %s\n", configPath)
	}
	fmt.Fprint(w, `
  Add a [frontmatter.properties.<name>] section, for example:

    [frontmatter.properties.status]
    type     = "string"
    required = true
    allowed  = ["WIP", "Done", "Archived"]

    [frontmatter.properties.tags]
    type  = "list"
    items = "string"

  To open the config in your editor, run: bn config edit
`)
}
