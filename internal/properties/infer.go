package properties

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
)

// Proposal is an inferred definition for an unconfigured property. Inference
// never proposes required or allowed: those are editorial policy that observed
// data cannot establish.
type Proposal struct {
	Property string              `json:"property"`
	Type     config.PropertyKind `json:"type"`
	Items    config.PropertyKind `json:"items,omitempty"`
	// Notes is the number of notes the inference was drawn from.
	Notes     int    `json:"notes"`
	Rationale string `json:"rationale"`
	// TOML is the canonical definition block a user would add to
	// brainnotes.toml, rendered for review only.
	TOML string `json:"toml"`
}

// Definition renders the proposal as a property definition.
func (p Proposal) Definition() config.PropertyDefinition {
	return config.PropertyDefinition{Type: p.Type, Items: p.Items}
}

// inferenceState accumulates the shapes and kinds observed for one property.
type inferenceState struct {
	scalars int
	lists   int
	kinds   map[config.PropertyKind]struct{}
}

func (s *inferenceState) observe(value Value) {
	if s.kinds == nil {
		s.kinds = make(map[config.PropertyKind]struct{})
	}
	if value.IsList() {
		s.lists++
	} else {
		s.scalars++
	}
	for _, logical := range value.Logical() {
		s.kinds[logical.Kind] = struct{}{}
	}
}

func (s *inferenceState) observedKinds() []config.PropertyKind {
	kinds := make([]config.PropertyKind, 0, len(s.kinds)+1)
	if s.lists > 0 {
		kinds = append(kinds, config.KindList)
	}
	for kind := range s.kinds {
		kinds = append(kinds, kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	return kinds
}

// proposal infers a definition when every observed value supports one kind.
// Mixed shapes, mixed incompatible kinds, and unsupported values all block
// inference rather than guessing.
func (s *inferenceState) proposal(name string, notes int) *Proposal {
	if notes == 0 || (s.scalars > 0 && s.lists > 0) {
		return nil
	}
	if _, unsupported := s.kinds[KindUnsupported]; unsupported {
		return nil
	}
	scalar, ok := unanimousScalarKind(s.kinds)
	if !ok {
		return nil
	}

	def := config.PropertyDefinition{Type: scalar}
	rationale := fmt.Sprintf("All %s store a %s value.", noteCountPhrase(notes), scalar)
	if s.lists > 0 {
		def = config.PropertyDefinition{Type: config.KindList, Items: scalar}
		rationale = fmt.Sprintf("All %s store a list of %s values.", noteCountPhrase(notes), scalar)
	}

	return &Proposal{
		Property:  name,
		Type:      def.Type,
		Items:     def.Items,
		Notes:     notes,
		Rationale: rationale,
		TOML:      strings.Join(config.RenderPropertyBlock(name, def), "\n"),
	}
}

// unanimousScalarKind returns the single kind every observed value satisfies.
// Two refinements are accepted because the narrower kind's values also satisfy
// the wider one: a bare wikilink is a string, and a date-only timestamp is a
// datetime.
func unanimousScalarKind(kinds map[config.PropertyKind]struct{}) (config.PropertyKind, bool) {
	observed := make([]config.PropertyKind, 0, len(kinds))
	for kind := range kinds {
		observed = append(observed, kind)
	}
	sort.Slice(observed, func(i, j int) bool { return observed[i] < observed[j] })

	switch len(observed) {
	case 1:
		if config.ValidScalarKind(observed[0]) {
			return observed[0], true
		}
	case 2:
		if hasBoth(observed, config.KindString, config.KindWikilink) {
			return config.KindString, true
		}
		if hasBoth(observed, config.KindDate, config.KindDatetime) {
			return config.KindDatetime, true
		}
	}
	return "", false
}

func hasBoth(kinds []config.PropertyKind, left, right config.PropertyKind) bool {
	var foundLeft, foundRight bool
	for _, kind := range kinds {
		switch kind {
		case left:
			foundLeft = true
		case right:
			foundRight = true
		}
	}
	return foundLeft && foundRight
}

func noteCountPhrase(notes int) string {
	if notes == 1 {
		return "1 note"
	}
	return fmt.Sprintf("%d notes", notes)
}
