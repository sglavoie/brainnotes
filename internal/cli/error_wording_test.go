package cli

import (
	"strings"
	"testing"
)

// buildErrorWordingVault is a two-note vault for the tests below that pin how a
// refusal is spelled: which command it names, and that it names what the user
// typed rather than an absolute path they never saw.
func buildErrorWordingVault(t *testing.T) string {
	t.Helper()
	isolateBnEnv(t)
	return buildVaultFromFiles(t, map[string]string{
		"Notes/Alpha.md": "---\ntitle: Alpha\ntags: [one]\n---\nAlpha links [[Beta]] and [[Missing]].\n",
		"Notes/Beta.md":  "---\ntitle: Beta\n---\nBeta body.\n",
	})
}

type errorWordingCase struct {
	name string
	args []string
	want string
}

func runErrorWordingCases(t *testing.T, root string, cases []errorWordingCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSuffix(runCLIOutcome(t, root, tc.args...), "\n")
			if got != tc.want {
				t.Fatalf("bn %s:\n got %q\nwant %q", strings.Join(tc.args, " "), got, tc.want)
			}
		})
	}
}

func TestErrorWordingNamesTheCommand(t *testing.T) {
	root := buildErrorWordingVault(t)
	runErrorWordingCases(t, root, []errorWordingCase{
		{
			name: "context missing note names context",
			args: []string{"context", "Nope"},
			want: `bn: context: no note titled "Nope", and no note at that path`,
		},
		{
			name: "related missing note names related",
			args: []string{"related", "Nope"},
			want: `bn: related: no note titled "Nope", and no note at that path`,
		},
		{
			name: "set unknown value type uses the enum wording",
			args: []string{"set", "Notes/Beta.md", "--value-type", "x", "k=v"},
			want: `bn: set: --value-type must be date, datetime, int, bool, float, or raw (got "x")`,
		},
	})
}
