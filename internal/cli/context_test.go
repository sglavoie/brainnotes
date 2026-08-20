package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func runContext(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"--vault", vaultRoot, "context"}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func runContextWithAgentRunner(
	t *testing.T,
	cfg *config.Config,
	runner agent.Runner,
	args ...string,
) (string, string, error) {
	t.Helper()
	cmd := newContextCmd(runner)
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetContext(context.WithValue(context.Background(), configKey, cfg))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func standardContextPurpose(t *testing.T, name string) recall.ContextPurpose {
	t.Helper()
	purpose, ok := recall.StandardPurpose(name)
	if !ok {
		t.Fatalf("standard purpose %q is not defined", name)
	}
	return purpose
}

func decodeContextJSON(t *testing.T, output string) recall.ContextBundle {
	t.Helper()
	var bundle recall.ContextBundle
	if err := json.Unmarshal([]byte(output), &bundle); err != nil {
		t.Fatalf("decode context JSON: %v\n%s", err, output)
	}
	return bundle
}

func TestContextMarkdownIsStableCanonicalReadOnlyOutput(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	first, stderr, err := runContext(t, root, "Source")
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	second, _, err := runContext(t, root, "Source.md")
	if err != nil {
		t.Fatalf("context by path: %v", err)
	}
	if first != second {
		t.Fatalf("stable Markdown changed across equivalent targets:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	for _, wanted := range []string{
		"# Research Context\n",
		"- Name: \"synthesis\"",
		standardContextPurpose(t, recall.PurposeSynthesis).Instructions,
		"- Source: \"Source.md\"",
		"<!-- BEGIN SOURCE BODY -->",
		"Contextual retrieval connects graph signals and semantic notes. [[Direct]]",
		fmt.Sprintf("- Byte count: %d", len(first)),
	} {
		if !strings.Contains(first, wanted) {
			t.Errorf("Markdown missing %q:\n%s", wanted, first)
		}
	}
	if !strings.HasSuffix(first, "\n") {
		t.Error("Markdown payload must end with a newline")
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("read-only context query created .bnotes: %v", err)
	}
}

func TestContextAgentReceivesCanonicalMarkdownAndPrintsStdoutVerbatim(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	cfg := &config.Config{
		Vault: root,
		Related: config.RelatedConfig{
			MetadataProperties: append([]string(nil), config.DefaultRelatedMetadataProperties...),
		},
		Agent: config.AgentConfig{
			Command:        []string{"research-agent", "--stdin", "literal argument"},
			TimeoutSeconds: 5,
		},
	}

	preview, _, err := runContextWithAgentRunner(t, cfg, agent.Run, "Source", "--purpose", "questions")
	if err != nil {
		t.Fatalf("context preview: %v", err)
	}
	sourcePath := filepath.Join(root, "Source.md")
	sourceBefore, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source before agent: %v", err)
	}

	var gotArgv []string
	var gotStdin []byte
	const agentOutput = "# Agent research brief\nKeep this output verbatim.\n"
	output, stderr, err := runContextWithAgentRunner(
		t,
		cfg,
		func(_ context.Context, argv []string, stdin []byte) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			gotStdin = append([]byte(nil), stdin...)
			return []byte(agentOutput), nil
		},
		"Source", "--purpose", "questions", "--agent",
	)
	if err != nil {
		t.Fatalf("context --agent: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if output != agentOutput {
		t.Fatalf("stdout = %q, want %q", output, agentOutput)
	}
	if !bytes.Equal(gotStdin, []byte(preview)) {
		t.Fatalf("agent stdin differs from preview\nstdin:\n%s\npreview:\n%s", gotStdin, preview)
	}
	wantArgv := []string{"research-agent", "--stdin", "literal argument"}
	if strings.Join(gotArgv, "\x00") != strings.Join(wantArgv, "\x00") {
		t.Fatalf("agent argv = %#v, want %#v", gotArgv, wantArgv)
	}
	sourceAfter, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read source after agent: %v", err)
	}
	if !bytes.Equal(sourceBefore, sourceAfter) {
		t.Fatal("context --agent modified the source note")
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("context --agent created .bnotes state or journal: %v", err)
	}
}

func TestContextAgentRequiresConfigurationBeforeOpeningVault(t *testing.T) {
	cmd := newContextCmd(func(context.Context, []string, []byte) ([]byte, error) {
		t.Fatal("agent runner called without configuration")
		return nil, nil
	})
	missingVault := filepath.Join(t.TempDir(), "does-not-exist")
	cmd.SetContext(context.WithValue(context.Background(), configKey, &config.Config{Vault: missingVault}))
	cmd.SetArgs([]string{"Source", "--agent"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--agent requires agent.command configuration") {
		t.Fatalf("error = %v, want missing agent configuration", err)
	}
	if strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("missing agent configuration opened vault before failing: %v", err)
	}
}

func TestContextJSONAndMarkdownSelectTheSameSourcesAndExcerpts(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	args := []string{"Source", "--related", "3", "--purpose", "questions"}

	markdown, _, err := runContext(t, root, args...)
	if err != nil {
		t.Fatalf("context Markdown: %v", err)
	}
	jsonOutput, stderr, err := runContext(t, root, append(args, "--format", "json")...)
	if err != nil {
		t.Fatalf("context JSON: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	bundle := decodeContextJSON(t, jsonOutput)
	if bundle.Purpose != standardContextPurpose(t, recall.PurposeQuestions) {
		t.Errorf("purpose = %+v, want %+v", bundle.Purpose, standardContextPurpose(t, recall.PurposeQuestions))
	}
	if bundle.Source.Identity.Path != "Source.md" {
		t.Fatalf("source path = %q, want Source.md", bundle.Source.Identity.Path)
	}
	if bundle.Manifest.SourcePath != bundle.Source.Identity.Path {
		t.Errorf("manifest source = %q, want %q", bundle.Manifest.SourcePath, bundle.Source.Identity.Path)
	}
	if bundle.Manifest.ByteCount != len(jsonOutput) {
		t.Errorf("JSON byte count = %d, want %d", bundle.Manifest.ByteCount, len(jsonOutput))
	}
	if len(jsonOutput) > recall.DefaultBundleMaxBytes || len(markdown) > recall.DefaultBundleMaxBytes {
		t.Errorf("payload sizes Markdown=%d JSON=%d exceed default %d", len(markdown), len(jsonOutput), recall.DefaultBundleMaxBytes)
	}
	if len(bundle.Related) == 0 || len(bundle.Related) > 3 {
		t.Fatalf("related count = %d, want 1..3", len(bundle.Related))
	}
	if !strings.Contains(markdown, fmt.Sprintf("- Source: %q", bundle.Manifest.SourcePath)) {
		t.Errorf("Markdown manifest does not name source %q", bundle.Manifest.SourcePath)
	}
	manifestExcerpts := 0
	for _, related := range bundle.Related {
		if !strings.Contains(markdown, fmt.Sprintf("- Path: %q", related.Identity.Path)) {
			t.Errorf("Markdown does not contain selected related path %q", related.Identity.Path)
		}
		for _, excerpt := range related.Excerpts {
			manifestExcerpts++
			manifestRow := fmt.Sprintf("  - %q (lines %d-%d)", excerpt.Path, excerpt.StartLine, excerpt.EndLine)
			if !strings.Contains(markdown, manifestRow) {
				t.Errorf("Markdown manifest missing %q", manifestRow)
			}
			if !strings.Contains(markdown, excerpt.Markdown) {
				t.Errorf("Markdown missing exact excerpt from %q", excerpt.Path)
			}
		}
	}
	if manifestExcerpts != len(bundle.Manifest.Excerpts) {
		t.Errorf("selected excerpts = %d, JSON manifest excerpts = %d", manifestExcerpts, len(bundle.Manifest.Excerpts))
	}
	jsonAgain, _, err := runContext(t, root, append(args, "--format", "json")...)
	if err != nil {
		t.Fatalf("repeated context JSON: %v", err)
	}
	if jsonOutput != jsonAgain {
		t.Error("repeated context JSON was not byte-identical")
	}
}

func TestContextSupportsExplicitZeroRelatedAndEveryPurpose(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	for _, purposeName := range []string{
		recall.PurposeSynthesis,
		recall.PurposeContradictions,
		recall.PurposeQuestions,
	} {
		t.Run(purposeName, func(t *testing.T) {
			output, _, err := runContext(
				t,
				root,
				"Source",
				"--related", "0",
				"--purpose", purposeName,
				"--format", "json",
			)
			if err != nil {
				t.Fatalf("context: %v", err)
			}
			bundle := decodeContextJSON(t, output)
			if bundle.Purpose != standardContextPurpose(t, purposeName) {
				t.Errorf("purpose = %+v, want %+v", bundle.Purpose, standardContextPurpose(t, purposeName))
			}
			if bundle.Related == nil || len(bundle.Related) != 0 {
				t.Errorf("related = %#v, want non-nil empty slice", bundle.Related)
			}
			if bundle.Manifest.Excerpts == nil || len(bundle.Manifest.Excerpts) != 0 {
				t.Errorf("manifest excerpts = %#v, want non-nil empty slice", bundle.Manifest.Excerpts)
			}
		})
	}
}

func TestContextValidatesFlagsBeforeOpeningVaultOrReadingState(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "related too low", args: []string{"Source", "--related", "-1"}, want: "--related must be between 0 and 10"},
		{name: "related too high", args: []string{"Source", "--related", "11"}, want: "--related must be between 0 and 10"},
		{name: "bytes too low", args: []string{"Source", "--max-bytes", "1023"}, want: "--max-bytes must be between 1024 and 1048576"},
		{name: "bytes too high", args: []string{"Source", "--max-bytes", "1048577"}, want: "--max-bytes must be between 1024 and 1048576"},
		{name: "unknown format", args: []string{"Source", "--format", "yaml"}, want: "--format must be markdown or json"},
		{name: "unknown purpose", args: []string{"Source", "--purpose", "brainstorm"}, want: "--purpose must be synthesis, contradictions, or questions"},
		{name: "agent json", args: []string{"Source", "--agent", "--format", "json"}, want: "--agent requires --format markdown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewContextCmd()
			missingVault := filepath.Join(t.TempDir(), "does-not-exist")
			ctx := context.WithValue(context.Background(), configKey, &config.Config{Vault: missingVault})
			cmd.SetContext(ctx)
			cmd.SetArgs(test.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if strings.Contains(err.Error(), "does-not-exist") {
				t.Fatalf("invalid flags opened the vault before failing: %v", err)
			}
		})
	}
}

func TestContextHonorsFeedbackSuppressionWithoutWritingState(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	initialOutput, _, err := runContext(t, root, "Source", "--format", "json")
	if err != nil {
		t.Fatalf("initial context: %v", err)
	}
	if !contextRelatedContains(decodeContextJSON(t, initialOutput), "Direct.md") {
		t.Fatalf("initial context does not include Direct.md: %s", initialOutput)
	}
	if _, _, err := runRelated(
		t,
		root,
		"related", "feedback", "Source", "Direct", "--state", "dismissed",
	); err != nil {
		t.Fatalf("dismiss feedback: %v", err)
	}
	statePath := filepath.Join(root, ".bnotes", "recall.json")
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state before context: %v", err)
	}

	suppressedOutput, _, err := runContext(t, root, "Source", "--format", "json")
	if err != nil {
		t.Fatalf("context with feedback: %v", err)
	}
	if contextRelatedContains(decodeContextJSON(t, suppressedOutput), "Direct.md") {
		t.Fatalf("dismissed Direct.md remained in context: %s", suppressedOutput)
	}
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after context: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("read-only context rewrote recall state\nbefore: %s\nafter: %s", before, after)
	}
}

func TestContextRejectsCorruptRecallStateWithoutChangingIt(t *testing.T) {
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

	if _, _, err := runContext(t, root, "Source"); !errors.Is(err, recall.ErrCorruptState) {
		t.Fatalf("context error = %v, want corrupt state", err)
	}
	gotState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read corrupt state: %v", err)
	}
	if !bytes.Equal(gotState, wantState) {
		t.Fatalf("corrupt state was overwritten: got %q want %q", gotState, wantState)
	}
}

func contextRelatedContains(bundle recall.ContextBundle, wanted string) bool {
	for _, related := range bundle.Related {
		if related.Identity.Path == wanted {
			return true
		}
	}
	return false
}

func TestRootRegistersContextCommand(t *testing.T) {
	command, _, err := NewRootCmd().Find([]string{"context"})
	if err != nil {
		t.Fatalf("find context command: %v", err)
	}
	if command == nil || command.Name() != "context" {
		t.Fatalf("context command not registered: %#v", command)
	}
}
