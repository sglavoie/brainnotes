package recall

// Standard context purpose names. Every caller that offers the standard
// research purposes uses these names and the instructions they resolve to, so
// the CLI and the web cannot present different contracts under one name.
const (
	PurposeSynthesis      = "synthesis"
	PurposeContradictions = "contradictions"
	PurposeQuestions      = "questions"
)

var standardPurposes = map[string]ContextPurpose{
	PurposeSynthesis: {
		Name:         PurposeSynthesis,
		Instructions: "Synthesize established ideas, tensions, missing evidence, and next questions.",
	},
	PurposeContradictions: {
		Name:         PurposeContradictions,
		Instructions: "Identify potentially conflicting claims, uncertainty, and supporting paths.",
	},
	PurposeQuestions: {
		Name:         PurposeQuestions,
		Instructions: "Identify unanswered questions and the most useful notes or evidence to pursue.",
	},
}

// StandardPurposeNames returns the standard purpose names in presentation
// order. The first name is the default every caller starts from.
func StandardPurposeNames() []string {
	return []string{PurposeSynthesis, PurposeContradictions, PurposeQuestions}
}

// StandardPurpose returns the shared definition for a standard purpose name.
func StandardPurpose(name string) (ContextPurpose, bool) {
	purpose, ok := standardPurposes[name]
	return purpose, ok
}
