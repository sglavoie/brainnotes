package queryexpr

import (
	"errors"
	"strings"
	"testing"

	"github.com/sglavoie/brainnotes/internal/config"
)

// shadowingEnv declares properties that share a name with a reserved field.
func shadowingEnv() Env {
	return Env{Frontmatter: &config.FrontmatterConfig{Properties: map[string]config.PropertyDefinition{
		"pinned": {Type: config.KindInt},
		"links":  {Type: config.KindList, Items: config.KindWikilink},
	}}}
}

// A reserved field wins over a declared property of the same name. When the
// reserved field then refuses the term, the error has to say so and name the
// spelling that reaches the property.
func TestCompile_ReservedFieldShadowingDeclaredPropertyHints(t *testing.T) {
	const hint = "write prop:pinned to compare the property"

	_, err := Compile("pinned>-1", shadowingEnv())
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

	if _, err := Compile("links:[[Hub]]", shadowingEnv()); err == nil || !strings.Contains(err.Error(), "write prop:links") {
		t.Fatalf("Compile(links:[[Hub]]) error = %v, want the prop:links hint", err)
	}
}

func TestCompile_ShadowedPropertyHintOnlyWhenItApplies(t *testing.T) {
	// The escape hatch the hint names compiles against the declared type, and a
	// reserved field that accepts the term is unaffected by the declaration.
	for _, src := range []string{"prop:pinned>-1", "pinned:true"} {
		if _, err := Compile(src, shadowingEnv()); err != nil {
			t.Errorf("Compile(%s): %v", src, err)
		}
	}
	// Without a declaration there is nothing to point at.
	if _, err := Compile("pinned>-1", Env{}); err == nil || strings.Contains(err.Error(), "prop:pinned") {
		t.Fatalf("Compile(pinned>-1) without a declaration error = %v, want no hint", err)
	}
}
