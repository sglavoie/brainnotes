package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func TestRelatedInteractivePreservesFZFExitBehavior(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	originalPicker := relatedPicker
	t.Cleanup(func() { relatedPicker = originalPicker })

	tests := []struct {
		name    string
		result  []string
		err     error
		wantErr error
	}{
		{name: "no match"},
		{name: "cancelled", err: ErrFZFInterrupted, wantErr: ErrFZFInterrupted},
		{name: "missing binary", err: ErrFZFNotFound, wantErr: ErrFZFNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
				return test.result, test.err
			}
			stdout, stderr, err := runRelatedWithConfig(t, cfg, "Source", "-i")
			if test.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if stdout != "" || stderr != "" {
					t.Fatalf("no-match output: stdout=%q stderr=%q", stdout, stderr)
				}
				return
			}
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.wantErr)
			}
		})
	}
}

func TestRelatedInteractiveRejectsCorruptedSelection(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Open.Editor = "mock-editor"

	originalPicker := relatedPicker
	originalOpenExec := relatedOpenExec
	t.Cleanup(func() {
		relatedPicker = originalPicker
		relatedOpenExec = originalOpenExec
	})
	relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
		return []string{rows[0] + "\ttampered"}, nil
	}
	editorCalled := false
	relatedOpenExec = func(editorSpec, file string) error {
		editorCalled = true
		return nil
	}

	_, _, err = runRelatedWithConfig(t, cfg, "Source", "-i", "--open")
	if err == nil || !strings.Contains(err.Error(), "fzf returned an invalid selection") {
		t.Fatalf("error = %v, want invalid-selection failure", err)
	}
	if editorCalled {
		t.Fatal("editor ran for a corrupted fzf selection")
	}
}

func TestRelatedInteractiveReportsEditorErrors(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Open.Editor = "mock-editor"

	originalPicker := relatedPicker
	originalOpenExec := relatedOpenExec
	t.Cleanup(func() {
		relatedPicker = originalPicker
		relatedOpenExec = originalOpenExec
	})
	relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
		return []string{rows[0]}, nil
	}
	relatedOpenExec = func(editorSpec, file string) error {
		return errors.New("mock editor failure")
	}

	_, _, err = runRelatedWithConfig(t, cfg, "Source", "-i", "--open")
	if err == nil || !strings.Contains(err.Error(), "related: open selected note: mock editor failure") {
		t.Fatalf("error = %v, want wrapped editor failure", err)
	}
}

func slicesContainSequence(values []string, first, second string) bool {
	for index := 0; index+1 < len(values); index++ {
		if values[index] == first && values[index+1] == second {
			return true
		}
	}
	return false
}

func TestRelatedEmptyCandidateSetSucceedsInEveryOutputMode(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	markVaultRoot(t, root, "")
	if err := os.WriteFile(filepath.Join(root, "Only.md"), []byte("Only note.\n"), 0o644); err != nil {
		t.Fatalf("write only note: %v", err)
	}

	for _, args := range [][]string{
		{"related", "Only"},
		{"related", "Only", "--paths"},
		{"related", "Only", "--paths", "--null"},
	} {
		output, stderr, err := runRelated(t, root, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if output != "" || stderr != "" {
			t.Fatalf("%v: stdout=%q stderr=%q, want empty", args, output, stderr)
		}
	}

	output, _, err := runRelated(t, root, "related", "Only", "--json")
	if err != nil {
		t.Fatalf("related empty --json: %v", err)
	}
	response := decodeRelatedJSON(t, output)
	if response.Results == nil || len(response.Results) != 0 {
		t.Fatalf("results = %#v, want non-nil empty slice", response.Results)
	}
}

func TestRelatedFeedbackFiltersBoostsClearsAndSnoozes(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	location := time.FixedZone("related-test", -6*60*60)
	now := time.Date(2026, time.July, 13, 15, 30, 0, 0, location)
	originalNow := relatedNow
	relatedNow = func() time.Time { return now }
	t.Cleanup(func() { relatedNow = originalNow })
	statePath := filepath.Join(root, ".bnotes", "recall.json")

	initial, _, err := runRelated(t, root, "related", "Source", "--json")
	if err != nil {
		t.Fatalf("initial related query: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(statePath)); !os.IsNotExist(err) {
		t.Fatalf("non-interactive related query created .bnotes: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, initial), "Direct.md"); !ok {
		t.Fatalf("initial results do not include Direct.md: %s", initial)
	}

	if stdout, stderr, err := runRelated(
		t,
		root,
		"related", "feedback", "Source", "Direct", "--state", "dismissed",
	); err != nil || stdout != "" || stderr != "" {
		t.Fatalf("dismiss feedback: stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
	dismissed, _, err := runRelated(t, root, "related", "Source", "--json")
	if err != nil {
		t.Fatalf("query dismissed state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, dismissed), "Direct.md"); ok {
		t.Fatalf("dismissed Direct.md remained in results: %s", dismissed)
	}

	if _, _, err := runRelated(t, root, "related", "feedback", "Source", "Direct", "--state", "clear"); err != nil {
		t.Fatalf("clear dismissed feedback: %v", err)
	}
	cleared, _, err := runRelated(t, root, "related", "Source", "--json")
	if err != nil {
		t.Fatalf("query cleared state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, cleared), "Direct.md"); !ok {
		t.Fatalf("cleared Direct.md did not return: %s", cleared)
	}

	if _, _, err := runRelated(t, root, "related", "feedback", "Source", "Direct", "--state", "useful"); err != nil {
		t.Fatalf("useful feedback: %v", err)
	}
	useful, _, err := runRelated(t, root, "related", "Source", "--json")
	if err != nil {
		t.Fatalf("query useful state: %v", err)
	}
	usefulResult, ok := relatedResultByPath(decodeRelatedJSON(t, useful), "Direct.md")
	if !ok || usefulResult.Signals.Recall != recall.UsefulRecallBoost {
		t.Fatalf("useful Direct.md result = %#v (present=%v), want recall boost %v", usefulResult, ok, recall.UsefulRecallBoost)
	}

	if _, _, err := runRelated(
		t,
		root,
		"related", "feedback", "Source", "Direct",
		"--state", "later", "--until", "2026-07-14",
	); err != nil {
		t.Fatalf("later feedback: %v", err)
	}
	snoozed, _, err := runRelated(t, root, "related", "Source", "--json")
	if err != nil {
		t.Fatalf("query snoozed state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, snoozed), "Direct.md"); ok {
		t.Fatalf("snoozed Direct.md remained in results: %s", snoozed)
	}
	if _, _, err := runRelated(t, root, "related", "feedback", "Source", "Direct", "--state", "clear"); err != nil {
		t.Fatalf("clear snoozed feedback: %v", err)
	}
}

func TestRelatedInteractiveSelectionRecordsLastOpened(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	location := time.FixedZone("related-test", -6*60*60)
	now := time.Date(2026, time.July, 13, 15, 30, 0, 0, location)
	originalNow := relatedNow
	originalPicker := relatedPicker
	relatedNow = func() time.Time { return now }
	relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
		for _, row := range rows {
			if strings.Contains(row, " Direct.md\t") {
				return []string{row}, nil
			}
		}
		return nil, errors.New("Direct.md picker row not found")
	}
	t.Cleanup(func() {
		relatedNow = originalNow
		relatedPicker = originalPicker
	})

	stdout, stderr, err := runRelated(t, root, "related", "Source", "--interactive")
	if err != nil {
		t.Fatalf("interactive related: %v", err)
	}
	wantPath := filepath.Join(root, "Direct.md") + "\n"
	if stdout != wantPath || stderr != "" {
		t.Fatalf("interactive output: stdout=%q stderr=%q, want %q", stdout, stderr, wantPath)
	}
	store := recall.NewStateStore(root, recall.WithClock(func() time.Time { return now }))
	signal, err := store.Signal("Source.md", "Direct.md")
	if err != nil {
		t.Fatalf("load selected pair signal: %v", err)
	}
	if !signal.LastOpenedAt.Equal(now) {
		t.Fatalf("last_opened_at = %s, want %s", signal.LastOpenedAt, now)
	}
}

func TestRelatedFeedbackValidatesStateAndLocalDateBeforeOpeningVault(t *testing.T) {
	location := time.FixedZone("related-test", -6*60*60)
	now := time.Date(2026, time.July, 13, 23, 30, 0, 0, location)
	originalNow := relatedNow
	relatedNow = func() time.Time { return now }
	t.Cleanup(func() { relatedNow = originalNow })

	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing state", args: []string{"feedback", "Source", "Candidate"}, want: "--state must be useful, dismissed, later, or clear"},
		{name: "unknown state", args: []string{"feedback", "Source", "Candidate", "--state", "maybe"}, want: "--state must be useful, dismissed, later, or clear"},
		{name: "later requires until", args: []string{"feedback", "Source", "Candidate", "--state", "later"}, want: "--until is required with --state later"},
		{name: "until only later", args: []string{"feedback", "Source", "Candidate", "--state", "useful", "--until", "2026-07-14"}, want: "--until is only valid with --state later"},
		{name: "invalid date", args: []string{"feedback", "Source", "Candidate", "--state", "later", "--until", "07/14/2026"}, want: "YYYY-MM-DD"},
		{name: "today is not future", args: []string{"feedback", "Source", "Candidate", "--state", "later", "--until", "2026-07-13"}, want: "future local calendar date"},
		{name: "past is not future", args: []string{"feedback", "Source", "Candidate", "--state", "later", "--until", "2026-07-12"}, want: "future local calendar date"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewRelatedCmd()
			missingVault := filepath.Join(t.TempDir(), "does-not-exist")
			cmd.SetContext(context.WithValue(context.Background(), configKey, &config.Config{Vault: missingVault}))
			cmd.SetArgs(test.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "does-not-exist") {
				t.Fatalf("invalid feedback opened the vault before failing: %v", err)
			}
		})
	}
}

func TestRelatedCorruptStateFailsWithoutOverwriting(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	statePath := filepath.Join(root, ".bnotes", "recall.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state directory: %v", err)
	}
	wantState := []byte("{not json\n")
	if err := os.WriteFile(statePath, wantState, 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}

	if _, _, err := runRelated(t, root, "related", "Source", "--json"); !errors.Is(err, recall.ErrCorruptState) {
		t.Fatalf("related error = %v, want corrupt state", err)
	}
	if _, _, err := runRelated(
		t,
		root,
		"related", "feedback", "Source", "Direct", "--state", "useful",
	); !errors.Is(err, recall.ErrCorruptState) {
		t.Fatalf("feedback error = %v, want corrupt state", err)
	}
	gotState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read corrupt state: %v", err)
	}
	if !bytes.Equal(gotState, wantState) {
		t.Fatalf("corrupt state was overwritten: got %q want %q", gotState, wantState)
	}
}

func relatedResultByPath(response relatedJSONResponse, wanted string) (relatedJSONResult, bool) {
	for _, result := range response.Results {
		if result.Path == wanted {
			return result, true
		}
	}
	return relatedJSONResult{}, false
}

func TestRootRegistersRelatedCommand(t *testing.T) {
	command, _, err := NewRootCmd().Find([]string{"related"})
	if err != nil {
		t.Fatalf("find related command: %v", err)
	}
	if command == nil || command.Name() != "related" {
		t.Fatalf("related command not registered: %#v", command)
	}
}
