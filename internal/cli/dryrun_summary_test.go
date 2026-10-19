package cli

import "testing"

// A dry run that would change nothing still says so, on stderr, so stdout
// stays exactly the preview (empty here) and a script can tell "nothing to do"
// from a command that silently did not run.
func TestDryRunSummaryOnStderrWhenNothingChanges(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"replace", "status", "nope", "never", "--dry-run"}, "would replace 0 file(s)\n"},
		{[]string{"repair", "--dry-run"}, "would repair 0 file(s)\n"},
		{[]string{"relink", "--dry-run"}, "would relink 0 file(s)\n"},
		{[]string{"rename-key", "absent", "other", "--dry-run"}, "would rename absent -> other in 0 file(s)\n"},
	}
	for _, tc := range cases {
		t.Run(tc.args[0], func(t *testing.T) {
			buildSetFixture(t)
			stdout, stderr, err := runCLISplit(t, tc.args...)
			if err != nil {
				t.Fatalf("bn %v: %v\nstderr: %s", tc.args, err, stderr)
			}
			if stdout != "" {
				t.Fatalf("bn %v: stdout = %q, want empty", tc.args, stdout)
			}
			if stderr != tc.want {
				t.Fatalf("bn %v: stderr = %q, want %q", tc.args, stderr, tc.want)
			}
		})
	}
}
