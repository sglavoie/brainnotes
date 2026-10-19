package queryexpr

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// A reserved field wins over a declared property of the same name. When the
// reserved field then refuses the term, the error has to say so and name the
// spelling that reaches the property.
func TestCompile_ReservedFieldShadowingDeclaredPropertyHints(t *testing.T) {
	declared := Env{Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
		"pinned": {Type: config.KindInt},
		"links":  {Type: config.KindList, Items: config.KindWikilink},
	}}}
	const hint = "write prop:pinned to compare the property"

	_, err := Compile("pinned>-1", declared)
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("Compile(pinned>-1) error = %v, want a *ParseError", err)
	}
	if !strings.Contains(pe.Msg, `operator ">" is not defined for field "pinned"`) || !strings.Contains(pe.Msg, hint) {
		t.Fatalf("message = %q, want the operator error plus %q", pe.Msg, hint)
	}
	if !strings.Contains(pe.Render(), hint) {
		t.Fatalf("Render() lost the hint:\n%s", pe.Render())
	}

	if _, err := Compile("links:[[Hub]]", declared); err == nil || !strings.Contains(err.Error(), "write prop:links") {
		t.Fatalf("Compile(links:[[Hub]]) error = %v, want the prop:links hint", err)
	}

	// The escape hatch the hint names compiles against the declared type.
	if _, err := Compile("prop:pinned>-1", declared); err != nil {
		t.Fatalf("Compile(prop:pinned>-1): %v", err)
	}
	// A reserved field that accepts the term is unaffected by the declaration.
	if _, err := Compile("pinned:true", declared); err != nil {
		t.Fatalf("Compile(pinned:true): %v", err)
	}
	// Without a declaration there is nothing to point at.
	if _, err := Compile("pinned>-1", Env{}); err == nil || strings.Contains(err.Error(), "prop:pinned") {
		t.Fatalf("Compile(pinned>-1) without a declaration error = %v, want no hint", err)
	}
}
