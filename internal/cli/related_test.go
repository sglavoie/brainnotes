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

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func newRelatedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"Source.md": "---\ntags: [retrieval]\ntype: note\n---\n" +
			"Contextual retrieval connects graph signals and semantic notes. [[Direct]]\n",
		"Direct.md": "---\ntags: [retrieval]\ntype: note\n---\n" +
			"Graph retrieval connects semantic context. [[Bridge]]\n",
		"Bridge.md": "---\ntags: [retrieval]\ntype: note\n---\n" +
			"A bridge for contextual graph retrieval and semantic notes.\n",
		"Dossier/Référence.md": "---\ntags: [retrieval]\ntype: reference\n---\n" +
			"Référence about semantic retrieval context.\n",
		"Unrelated.md": "---\ntags: [cooking]\ntype: recipe\n---\n" +
			"Whisk eggs and fold in herbs.\n",
	}
	for relative, body := range files {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	markVaultRoot(t, root, "")
	return root
}

func runRelated(t *testing.T, vaultRoot string, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"--vault", vaultRoot}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func runRelatedWithConfig(t *testing.T, cfg *config.Config, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRelatedCmd()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetContext(context.WithValue(context.Background(), configKey, cfg))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func decodeRelatedJSON(t *testing.T, output string) relatedJSONResponse {
	t.Helper()
	var response relatedJSONResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("decode related JSON: %v\n%s", err, output)
	}
	return response
}

func TestRelatedJSONIsStableAndContainsRankingIdentity(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	first, stderr, err := runRelated(t, root, "related", "Source", "--json", "--limit", "3")
	if err != nil {
		t.Fatalf("related --json: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	second, _, err := runRelated(t, root, "related", "Source.md", "--json", "--limit", "3")
	if err != nil {
		t.Fatalf("second related --json: %v", err)
	}
	if first != second {
		t.Fatalf("stable JSON changed across equivalent queries:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if strings.Contains(strings.ToLower(first), "timestamp") || strings.Contains(first, "created_at") {
		t.Fatalf("JSON must not contain timestamps:\n%s", first)
	}

	response := decodeRelatedJSON(t, first)
	if response.Source.Path != "Source.md" || response.Source.Title != "Source" {
		t.Fatalf("source = %+v, want Source.md/Source", response.Source)
	}
	if response.Mode != recall.ModeBalanced {
		t.Fatalf("mode = %q, want balanced", response.Mode)
	}
	if response.RankingVersion != recall.RankingVersion {
		t.Fatalf("ranking_version = %d, want %d", response.RankingVersion, recall.RankingVersion)
	}
	if response.Weights.Text != recall.TextScoreWeight ||
		response.Weights.Graph != recall.GraphScoreWeight ||
		response.Weights.Metadata != recall.MetadataScoreWeight ||
		response.Weights.Recall != recall.RecallScoreWeight {
		t.Fatalf("weights = %+v, want fixed ranking weights", response.Weights)
	}
	if len(response.Results) != 3 {
		t.Fatalf("results = %d, want limit 3: %+v", len(response.Results), response.Results)
	}
	for index, result := range response.Results {
		if result.Rank != index+1 {
			t.Fatalf("result %d rank = %d", index, result.Rank)
		}
		if result.Path == "" || result.Title == "" || len(result.Reasons) == 0 || len(result.Reasons) > 3 {
			t.Fatalf("result %d missing stable detail: %+v", index, result)
		}
		if index > 0 && response.Results[index-1].Score < result.Score {
			t.Fatalf("results not in descending score order: %+v", response.Results)
		}
	}
}

func TestRelatedHumanAndPathOutputsPreserveRankingOrder(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	jsonOutput, _, err := runRelated(t, root, "related", "Source", "--json", "--limit", "3")
	if err != nil {
		t.Fatalf("related --json: %v", err)
	}
	response := decodeRelatedJSON(t, jsonOutput)

	human, _, err := runRelated(t, root, "related", "Source", "--limit", "3")
	if err != nil {
		t.Fatalf("related human: %v", err)
	}
	for index, result := range response.Results {
		want := string(rune('1'+index)) + ". ["
		if !strings.Contains(human, want) || !strings.Contains(human, result.Path) {
			t.Fatalf("human output missing rank/path for result %d:\n%s", index+1, human)
		}
	}

	pathsOutput, _, err := runRelated(t, root, "related", filepath.Join(root, "Source.md"), "--paths", "--limit", "3")
	if err != nil {
		t.Fatalf("related --paths: %v", err)
	}
	paths := strings.Split(strings.TrimSuffix(pathsOutput, "\n"), "\n")
	if len(paths) != len(response.Results) {
		t.Fatalf("path count = %d, want %d: %q", len(paths), len(response.Results), pathsOutput)
	}
	for index, result := range response.Results {
		want := filepath.Join(root, filepath.FromSlash(result.Path))
		if paths[index] != want || !filepath.IsAbs(paths[index]) {
			t.Fatalf("path %d = %q, want absolute %q", index, paths[index], want)
		}
	}

	nullOutput, _, err := runRelated(t, root, "related", "Source", "--paths", "--null", "--limit", "3")
	if err != nil {
		t.Fatalf("related --paths --null: %v", err)
	}
	if strings.Contains(nullOutput, "\n") || !strings.HasSuffix(nullOutput, "\x00") {
		t.Fatalf("NUL output has wrong separators: %q", nullOutput)
	}
	nullPaths := strings.Split(strings.TrimSuffix(nullOutput, "\x00"), "\x00")
	if len(nullPaths) != len(paths) {
		t.Fatalf("NUL path count = %d, want %d", len(nullPaths), len(paths))
	}
	for index := range paths {
		if nullPaths[index] != paths[index] {
			t.Fatalf("NUL path %d = %q, want %q", index, nullPaths[index], paths[index])
		}
	}
}

func TestRelatedResolvesTitleRelativeAbsoluteAndUnicodeTargets(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	tests := []struct {
		name      string
		argument  string
		wantPath  string
		wantTitle string
	}{
		{name: "title", argument: "Source", wantPath: "Source.md", wantTitle: "Source"},
		{name: "relative path", argument: "Source.md", wantPath: "Source.md", wantTitle: "Source"},
		{name: "absolute path", argument: filepath.Join(root, "Source.md"), wantPath: "Source.md", wantTitle: "Source"},
		{name: "Unicode title", argument: "Référence", wantPath: "Dossier/Référence.md", wantTitle: "Référence"},
		{name: "Unicode path", argument: "Dossier/Référence.md", wantPath: "Dossier/Référence.md", wantTitle: "Référence"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output, _, err := runRelated(t, root, "related", test.argument, "--json")
			if err != nil {
				t.Fatalf("related %q: %v", test.argument, err)
			}
			response := decodeRelatedJSON(t, output)
			if response.Source.Path != test.wantPath || response.Source.Title != test.wantTitle {
				t.Fatalf("source = %+v, want path=%q title=%q", response.Source, test.wantPath, test.wantTitle)
			}
		})
	}
}

func TestRelatedValidatesFlagsBeforeOpeningVault(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "JSON and paths", args: []string{"Source", "--json", "--paths"}, want: "--json cannot be used with --paths"},
		{name: "JSON and null", args: []string{"Source", "--json", "--null"}, want: "--json cannot be used with --null"},
		{name: "null requires paths", args: []string{"Source", "--null"}, want: "--null requires --paths"},
		{name: "limit too low", args: []string{"Source", "--limit", "0"}, want: "--limit must be between 1 and 100"},
		{name: "limit too high", args: []string{"Source", "--limit", "101"}, want: "--limit must be between 1 and 100"},
		{name: "unknown mode", args: []string{"Source", "--mode", "surprise"}, want: "--mode must be balanced, rediscover, or connect"},
		{name: "open requires interactive", args: []string{"Source", "--open"}, want: "--open requires --interactive"},
		{name: "interactive and JSON", args: []string{"Source", "--interactive", "--json"}, want: "--interactive cannot be used with --json"},
		{name: "interactive and paths", args: []string{"Source", "--interactive", "--paths"}, want: "--interactive cannot be used with --paths"},
		{name: "interactive and null", args: []string{"Source", "--interactive", "--null"}, want: "--interactive cannot be used with --null"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := NewRelatedCmd()
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

func TestRelatedInteractiveUsesRankedRowsAndPrintsSelection(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)

	jsonOutput, _, err := runRelated(t, root, "related", "Source", "--json", "--limit", "3")
	if err != nil {
		t.Fatalf("related --json: %v", err)
	}
	response := decodeRelatedJSON(t, jsonOutput)

	cfg, err := config.Load(config.LoadOptions{VaultFlag: root})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Fzf.Args = []string{"--height", "70%"}

	originalPicker := relatedPicker
	originalOpenExec := relatedOpenExec
	t.Cleanup(func() {
		relatedPicker = originalPicker
		relatedOpenExec = originalOpenExec
	})

	var gotRows []string
	var gotArgs []string
	relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
		gotRows = append([]string(nil), rows...)
		gotArgs = append([]string(nil), extraArgs...)
		return []string{rows[1]}, nil
	}
	relatedOpenExec = func(editorSpec, file string) error {
		return errors.New("editor must not run without --open")
	}

	stdout, stderr, err := runRelatedWithConfig(t, cfg, "Source", "--interactive", "--limit", "3")
	if err != nil {
		t.Fatalf("related --interactive: %v", err)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if len(gotRows) != len(response.Results) {
		t.Fatalf("fzf row count = %d, want ranked result count %d", len(gotRows), len(response.Results))
	}
	for index, row := range gotRows {
		fields := strings.SplitN(row, "\t", 3)
		if len(fields) != 3 {
			t.Fatalf("row %d = %q, want hidden path plus two visible fields", index, row)
		}
		wantAbsolute := filepath.Join(root, filepath.FromSlash(response.Results[index].Path))
		if fields[0] != wantAbsolute {
			t.Fatalf("row %d hidden path = %q, want %q", index, fields[0], wantAbsolute)
		}
		relationship := string(response.Results[index].Relationship)
		if relationship == "" {
			relationship = "none"
		}
		wantIdentity := fmt.Sprintf("%d. [%s] %s", index+1, relationship, response.Results[index].Path)
		if fields[1] != wantIdentity {
			t.Fatalf("row %d visible identity = %q, want %q", index, fields[1], wantIdentity)
		}
		if len(response.Results[index].Reasons) > 0 && !strings.Contains(fields[2], response.Results[index].Reasons[0].Detail) {
			t.Fatalf("row %d reasons = %q, want first ranked reason %q", index, fields[2], response.Results[index].Reasons[0].Detail)
		}
	}

	wantSelected := filepath.Join(root, filepath.FromSlash(response.Results[1].Path)) + "\n"
	if stdout != wantSelected {
		t.Fatalf("stdout = %q, want selected ranked path %q", stdout, wantSelected)
	}
	if preview, ok := fzfArgValue(gotArgs, "--preview"); !ok || preview != "cat {1}" {
		t.Fatalf("related preview = %q (present=%v), want current note preview", preview, ok)
	}
	if withNth, ok := fzfArgValue(gotArgs, "--with-nth"); !ok || withNth != "{2}\t{3}" {
		t.Fatalf("related display fields = %q (present=%v), want relationship/path and reasons", withNth, ok)
	}
	if !slicesContainSequence(gotArgs, "--height", "70%") {
		t.Fatalf("configured fzf args were not preserved: %v", gotArgs)
	}
}

func TestRelatedInteractiveOpenUsesConfiguredEditor(t *testing.T) {
	isolateBnEnv(t)
	root := newRelatedFixture(t)
	cfg, err := config.Load(config.LoadOptions{VaultFlag: root})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Open.Editor = "code --wait"

	originalPicker := relatedPicker
	originalOpenExec := relatedOpenExec
	t.Cleanup(func() {
		relatedPicker = originalPicker
		relatedOpenExec = originalOpenExec
	})

	var selectedRow string
	relatedPicker = func(rows []string, extraArgs []string) ([]string, error) {
		selectedRow = rows[0]
		return []string{selectedRow}, nil
	}
	var gotEditor string
	var gotFile string
	relatedOpenExec = func(editorSpec, file string) error {
		gotEditor = editorSpec
		gotFile = file
		return nil
	}

	stdout, stderr, err := runRelatedWithConfig(t, cfg, "Source", "-i", "--open")
	if err != nil {
		t.Fatalf("related -i --open: %v", err)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("open output: stdout=%q stderr=%q, want empty", stdout, stderr)
	}
	if gotEditor != cfg.Open.Editor {
		t.Fatalf("editor = %q, want configured %q", gotEditor, cfg.Open.Editor)
	}
	wantFile := strings.SplitN(selectedRow, "\t", 2)[0]
	if gotFile != wantFile || !filepath.IsAbs(gotFile) {
		t.Fatalf("opened file = %q, want selected absolute path %q", gotFile, wantFile)
	}
}
