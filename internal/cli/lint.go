package cli

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/query"
	"github.com/sglavoie/sglavoie_brainnotes/internal/schema"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// LintError signals that lint found violations. main.go prints it and exits 1.
type LintError struct {
	Count int
}

func (e *LintError) Error() string {
	return fmt.Sprintf("%d violation(s) found", e.Count)
}

func NewLintCmd() *cobra.Command {
	var (
		tags        []string
		categories  []string
		status      string
		typeProp    string
		hasProp     string
		missingProp string
		age         string
		created     string
		folder      string
		orphan      bool
		property    string
	)

	cmd := &cobra.Command{
		Use:   "lint",
		Short: "Report schema violations in frontmatter across the vault",
		Long: `Lint checks every note (or a scoped subset) against the property schema
defined in config.toml and reports violations without modifying files.

Exit code 0 means clean; exit code 1 means violations were found.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := ConfigFromContext(cmd.Context())
			if cfg == nil {
				return errors.New("lint: no config in context")
			}
			v, err := vault.Open(cfg)
			if err != nil {
				return err
			}

			if len(cfg.Frontmatter.Schema) == 0 {
				fmt.Fprintln(cmd.ErrOrStderr(), "lint: no schema defined in config; see [frontmatter.schema]")
				return nil
			}

			schemaToUse := cfg.Frontmatter.Schema
			if property != "" {
				rule, ok := cfg.Frontmatter.Schema[property]
				if !ok {
					return fmt.Errorf("lint: property %q not in schema", property)
				}
				schemaToUse = map[string]config.PropertySchema{property: rule}
			}

			notes, err := v.Notes()
			if err != nil {
				return err
			}

			flags := query.Flags{
				Tags:       tags,
				Categories: categories,
				Status:     status,
				Type:       typeProp,
				Has:        hasProp,
				Missing:    missingProp,
				Age:        age,
				Created:    created,
				Folder:     folder,
				Orphan:     orphan,
				Root:       v.Root,
			}
			if flags.Orphan {
				flags.BacklinkIndex = query.BuildBacklinkIndex(notes)
			}
			pred, err := query.FromFlags(flags)
			if err != nil {
				return err
			}

			var allViolations []schema.Violation
			for _, n := range notes {
				if !pred(n) {
					continue
				}
				front, err := n.Front()
				if err != nil {
					return err
				}
				vs := schema.ValidateNote(front, displayPath(n.Path, v.Root), schemaToUse, cfg.Frontmatter.WikilinkProperties)
				allViolations = append(allViolations, vs...)
			}

			if len(allViolations) == 0 {
				return nil
			}

			formatViolations(cmd, allViolations)
			return &LintError{Count: len(allViolations)}
		},
	}

	f := cmd.Flags()
	f.StringSliceVar(&tags, "tag", nil, "filter by tag (repeatable; AND semantics)")
	f.StringSliceVar(&categories, "category", nil, "filter by category target (repeatable; AND semantics)")
	f.StringVar(&status, "status", "", "filter by status property")
	f.StringVar(&typeProp, "type", "", "filter by type/kind property")
	f.StringVar(&hasProp, "has", "", "require property present")
	f.StringVar(&missingProp, "missing", "", "require property absent")
	f.StringVar(&age, "age", "", "filter by 'last' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&created, "created", "", "filter by 'created' property age, e.g. >30d, <7d, =0d")
	f.StringVar(&folder, "folder", "", "limit to a vault-relative folder (matches descendants)")
	f.BoolVar(&orphan, "orphan", false, "only notes nothing in the vault links to")
	f.StringVar(&property, "property", "", "lint only this schema property")

	return cmd
}

type groupKey struct {
	Property string
	Kind     string
	Detail   string
}

func formatViolations(cmd *cobra.Command, violations []schema.Violation) {
	groups := make(map[groupKey][]string)
	for _, v := range violations {
		key := groupKey{Property: v.Property, Kind: v.Kind, Detail: v.Detail}
		groups[key] = append(groups[key], v.NotePath)
	}

	keys := make([]groupKey, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Property != keys[j].Property {
			return keys[i].Property < keys[j].Property
		}
		if keys[i].Kind != keys[j].Kind {
			return keys[i].Kind < keys[j].Kind
		}
		return keys[i].Detail < keys[j].Detail
	})

	out := cmd.OutOrStdout()
	for _, key := range keys {
		paths := groups[key]
		sort.Strings(paths)

		n := len(paths)
		noun := "note(s)"
		if n == 1 {
			noun = "note"
		} else {
			noun = "notes"
		}

		switch key.Kind {
		case "missing":
			fmt.Fprintf(out, "%s: missing in %d %s\n", key.Property, n, noun)
		case "type":
			fmt.Fprintf(out, "%s: wrong type in %d %s (%s)\n", key.Property, n, noun, key.Detail)
		case "value":
			fmt.Fprintf(out, "%s: %s in %d %s\n", key.Property, key.Detail, n, noun)
		}

		for _, p := range paths {
			fmt.Fprintf(out, "  %s\n", p)
		}
	}
}
