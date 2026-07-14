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

	"github.com/sglavoie/sglavoie_brainnotes/internal/agent"
	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/recall"
)

func writeRecallIntegrationFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for relative, body := range files {
		fullPath := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relative, err)
		}
		if err := os.WriteFile(fullPath, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
}

func snapshotRecallMarkdown(t *testing.T, root string) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(relative)] = string(body)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot Markdown: %v", err)
	}
	return snapshot
}

func TestRecallWorkflowEndToEnd(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	writeRecallIntegrationFiles(t, root, map[string]string{
		"Source.md": "---\ncreated: 2020-01-02\ntags: [retrieval, systems]\ntopics: [context]\n---\n# Contextual recall\n\nContextual retrieval combines graph and semantic signals. [[Direct]]\n",
		"Direct.md": "---\ncreated: 2021-02-03\ntags: [retrieval, systems]\ntopics: [context]\n---\n# Direct evidence\n\nGraph retrieval connects semantic context and source evidence. [[Bridge]]\n",
		"Bridge.md": "---\ncreated: 2019-03-04\ntags: [retrieval]\ntopics: [context]\n---\n# Bridge evidence\n\nA bridge joins contextual graph retrieval with research notes.\n",
		"Other.md":  "---\ntags: [cooking]\n---\n# Other\n\nWhisk eggs and fold in herbs.\n",
	})
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Agent.Command = []string{"research-agent", "--stdin"}
	cfg.Agent.TimeoutSeconds = 5

	location := time.FixedZone("recall-integration", -6*60*60)
	now := time.Date(2026, time.July, 13, 15, 30, 0, 0, location)
	originalNow := relatedNow
	originalPicker := relatedPicker
	relatedNow = func() time.Time { return now }
	t.Cleanup(func() {
		relatedNow = originalNow
		relatedPicker = originalPicker
	})

	markdownBefore := snapshotRecallMarkdown(t, root)
	baselineOutput, stderr, err := runRelatedWithConfig(t, cfg, "Source", "--json")
	if err != nil {
		t.Fatalf("initial related query: %v", err)
	}
	if stderr != "" {
		t.Fatalf("initial related stderr = %q, want empty", stderr)
	}
	baseline := decodeRelatedJSON(t, baselineOutput)
	if len(baseline.Results) < 2 || baseline.Results[0].Path != "Direct.md" {
		t.Fatalf("initial ranked results = %+v, want Direct.md first and multiple candidates", baseline.Results)
	}

	relatedPicker = func(rows []string, _ []string) ([]string, error) {
		wanted := filepath.Join(root, "Direct.md") + "\t"
		for _, row := range rows {
			if strings.HasPrefix(row, wanted) {
				return []string{row}, nil
			}
		}
		return nil, errors.New("Direct.md was not offered to the picker")
	}
	selected, stderr, err := runRelatedWithConfig(t, cfg, "Source", "--interactive")
	if err != nil {
		t.Fatalf("interactive related query: %v", err)
	}
	if stderr != "" || selected != filepath.Join(root, "Direct.md")+"\n" {
		t.Fatalf("interactive output: stdout=%q stderr=%q", selected, stderr)
	}

	statePath := filepath.Join(root, ".bnotes", "recall.json")
	state, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read selection state: %v", err)
	}
	if !bytes.Contains(state, []byte(`"last_opened_at"`)) {
		t.Fatalf("selection state does not record last_opened_at: %s", state)
	}
	if bytes.Contains(state, []byte("Contextual retrieval combines")) {
		t.Fatalf("recall state leaked note body text: %s", state)
	}

	if _, _, err := runRelatedWithConfig(t, cfg, "feedback", "Source", "Direct", "--state", "useful"); err != nil {
		t.Fatalf("useful feedback: %v", err)
	}
	state, err = os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read useful state: %v", err)
	}
	if !bytes.Contains(state, []byte(`"rating": "useful"`)) {
		t.Fatalf("useful feedback missing from state: %s", state)
	}

	if _, _, err := runRelatedWithConfig(t, cfg, "feedback", "Source", "Direct", "--state", "dismissed"); err != nil {
		t.Fatalf("dismiss feedback: %v", err)
	}
	dismissedOutput, _, err := runRelatedWithConfig(t, cfg, "Source", "--json")
	if err != nil {
		t.Fatalf("query dismissed state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, dismissedOutput), "Direct.md"); ok {
		t.Fatalf("dismissed Direct.md remained in related results: %s", dismissedOutput)
	}

	if _, _, err := runRelatedWithConfig(
		t,
		cfg,
		"feedback", "Source", "Direct", "--state", "later", "--until", "2026-08-01",
	); err != nil {
		t.Fatalf("later feedback: %v", err)
	}
	snoozedOutput, _, err := runRelatedWithConfig(t, cfg, "Source", "--json")
	if err != nil {
		t.Fatalf("query snoozed state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, snoozedOutput), "Direct.md"); ok {
		t.Fatalf("snoozed Direct.md remained in related results: %s", snoozedOutput)
	}

	if _, _, err := runRelatedWithConfig(t, cfg, "feedback", "Source", "Direct", "--state", "clear"); err != nil {
		t.Fatalf("clear feedback: %v", err)
	}
	clearedOutput, _, err := runRelatedWithConfig(t, cfg, "Source", "--json")
	if err != nil {
		t.Fatalf("query cleared state: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, clearedOutput), "Direct.md"); !ok {
		t.Fatalf("cleared Direct.md did not return to related results: %s", clearedOutput)
	}

	preview, stderr, err := runContextWithAgentRunner(
		t,
		cfg,
		agent.Run,
		"Source", "--related", "2", "--purpose", "questions",
	)
	if err != nil {
		t.Fatalf("context preview: %v", err)
	}
	if stderr != "" || !strings.Contains(preview, "Direct.md") || !strings.Contains(preview, "<!-- BEGIN SOURCE BODY -->") {
		t.Fatalf("context preview missing workflow evidence: stderr=%q\n%s", stderr, preview)
	}

	var gotArgv []string
	var gotStdin []byte
	const agentOutput = "# Research brief\nInspect the ranked evidence.\n"
	brief, stderr, err := runContextWithAgentRunner(
		t,
		cfg,
		func(_ context.Context, argv []string, stdin []byte) ([]byte, error) {
			gotArgv = append([]string(nil), argv...)
			gotStdin = append([]byte(nil), stdin...)
			return []byte(agentOutput), nil
		},
		"Source", "--related", "2", "--purpose", "questions", "--agent",
	)
	if err != nil {
		t.Fatalf("context agent: %v", err)
	}
	if stderr != "" || brief != agentOutput {
		t.Fatalf("agent output: stdout=%q stderr=%q", brief, stderr)
	}
	if !bytes.Equal(gotStdin, []byte(preview)) {
		t.Fatal("agent stdin was not byte-identical to the inspectable context preview")
	}
	if strings.Join(gotArgv, "\x00") != strings.Join(cfg.Agent.Command, "\x00") {
		t.Fatalf("agent argv = %#v, want %#v", gotArgv, cfg.Agent.Command)
	}

	if got := snapshotRecallMarkdown(t, root); !mapsEqual(got, markdownBefore) {
		t.Fatalf("recall workflow modified Markdown\nbefore=%v\nafter=%v", markdownBefore, got)
	}
	if _, err := os.Stat(filepath.Join(root, ".bnotes", "journal")); !os.IsNotExist(err) {
		t.Fatalf("recall workflow created an undo journal: %v", err)
	}
}

func TestRecallCLIHardeningInputAndCorpusEdges(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	const nfdName = "Cafe\u0301.md"
	const sourceBody = "# Café\r\n\r\nUnicode contextual retrieval without a created date. [[Peer]]\r\n"
	writeRecallIntegrationFiles(t, root, map[string]string{
		"One/Duplicate.md": "Duplicate stem in folder one.\n",
		"Two/Duplicate.md": "Duplicate stem in folder two.\n",
		nfdName:            "---\r\ntags: [retrieval]\r\n---\r\n" + sourceBody,
		"Peer.md":          "---\ntags: [retrieval]\n---\nUnicode contextual retrieval peer without a created date. [[Café]]\n",
		"Archive/Bad.md":   "---\nmalformed: [\n---\nignored default folder body\n",
		"Skipped/Bad.md":   "---\nmalformed: [\n---\nignored configured folder body\n",
	})
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Ignore = append(cfg.Ignore, "Skipped")
	cfg.Related.MetadataProperties = []string{}

	_, _, err = runRelatedWithConfig(t, cfg, "Duplicate", "--json")
	if err == nil || !strings.Contains(err.Error(), `"Duplicate" is ambiguous`) ||
		!strings.Contains(err.Error(), "One/Duplicate.md") || !strings.Contains(err.Error(), "Two/Duplicate.md") {
		t.Fatalf("duplicate-stem ambiguity error = %v", err)
	}
	explicitOutput, _, err := runRelatedWithConfig(t, cfg, "One/Duplicate.md", "--json")
	if err != nil {
		t.Fatalf("explicit duplicate path: %v", err)
	}
	if got := decodeRelatedJSON(t, explicitOutput).Source.Path; got != "One/Duplicate.md" {
		t.Fatalf("explicit duplicate source path = %q, want One/Duplicate.md", got)
	}

	unicodeOutput, _, err := runRelatedWithConfig(t, cfg, "Café", "--json")
	if err != nil {
		t.Fatalf("NFC query for NFD filename: %v", err)
	}
	unicodeResponse := decodeRelatedJSON(t, unicodeOutput)
	if unicodeResponse.Source.Path != "Café.md" || unicodeResponse.Source.Title != "Café" {
		t.Fatalf("normalized Unicode source = %+v, want Café.md/Café", unicodeResponse.Source)
	}
	peer, ok := relatedResultByPath(unicodeResponse, "Peer.md")
	if !ok {
		t.Fatalf("missing-date peer was not ranked: %+v", unicodeResponse.Results)
	}
	if peer.Signals.Metadata != 0 || peer.Signals.Recall != 0 {
		t.Fatalf("explicitly disabled metadata/missing-date recall signals = %+v, want zero", peer.Signals)
	}
	if strings.Contains(unicodeOutput, "Archive/Bad.md") || strings.Contains(unicodeOutput, "Skipped/Bad.md") {
		t.Fatalf("ignored folders leaked into related output: %s", unicodeOutput)
	}

	contextOutput, _, err := runContextWithAgentRunner(
		t,
		cfg,
		agent.Run,
		"Café", "--format", "json", "--related", "1",
	)
	if err != nil {
		t.Fatalf("context for CRLF/NFD source: %v", err)
	}
	bundle := decodeContextJSON(t, contextOutput)
	if bundle.Source.Body != sourceBody {
		t.Fatalf("context source body did not preserve CRLF bytes: got %q want %q", bundle.Source.Body, sourceBody)
	}
	if len(bundle.Related) != 1 || bundle.Related[0].Identity.Path != "Peer.md" {
		t.Fatalf("context related selection = %+v, want Peer.md", bundle.Related)
	}
}

func TestRecallCLIHardeningRemovedCorruptAndEmptyState(t *testing.T) {
	isolateBnEnv(t)
	root := t.TempDir()
	writeRecallIntegrationFiles(t, root, map[string]string{
		"Source.md":   "Recall source links [[Removed]] and [[Survivor]].\n",
		"Removed.md":  "Recall candidate that will be removed.\n",
		"Survivor.md": "Recall candidate that remains.\n",
	})
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if _, _, err := runRelatedWithConfig(t, cfg, "feedback", "Source", "Removed", "--state", "useful"); err != nil {
		t.Fatalf("seed removed-note feedback: %v", err)
	}
	statePath := filepath.Join(root, ".bnotes", "recall.json")
	beforeRemovalQuery, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read seeded state: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "Removed.md")); err != nil {
		t.Fatalf("remove candidate: %v", err)
	}

	output, _, err := runRelatedWithConfig(t, cfg, "Source", "--json")
	if err != nil {
		t.Fatalf("read-only query with removed state record: %v", err)
	}
	if _, ok := relatedResultByPath(decodeRelatedJSON(t, output), "Removed.md"); ok {
		t.Fatalf("removed note remained in results: %s", output)
	}
	afterRemovalQuery, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read state after removed-note query: %v", err)
	}
	if !bytes.Equal(afterRemovalQuery, beforeRemovalQuery) {
		t.Fatal("read-only query pruned stale state instead of leaving recovery to an explicit write")
	}

	if _, _, err := runRelatedWithConfig(t, cfg, "feedback", "Source", "Survivor", "--state", "useful"); err != nil {
		t.Fatalf("survivor feedback: %v", err)
	}
	prunedState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read pruned state: %v", err)
	}
	if bytes.Contains(prunedState, []byte("Removed.md")) || !bytes.Contains(prunedState, []byte("Survivor.md")) {
		t.Fatalf("explicit write did not prune removed note: %s", prunedState)
	}

	corruptState := []byte("{not json\n")
	if err := os.WriteFile(statePath, corruptState, 0o644); err != nil {
		t.Fatalf("write corrupt state: %v", err)
	}
	if _, _, err := runRelatedWithConfig(t, cfg, "Source", "--json"); !errors.Is(err, recall.ErrCorruptState) {
		t.Fatalf("corrupt-state query error = %v, want ErrCorruptState", err)
	}
	unchangedCorruptState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read corrupt state: %v", err)
	}
	if !bytes.Equal(unchangedCorruptState, corruptState) {
		t.Fatalf("corrupt state was overwritten: got %q want %q", unchangedCorruptState, corruptState)
	}

	emptyRoot := t.TempDir()
	emptyCfg, err := config.Load(emptyRoot, "")
	if err != nil {
		t.Fatalf("load empty-vault config: %v", err)
	}
	if _, _, err := runRelatedWithConfig(t, emptyCfg, "Missing", "--json"); err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("empty-corpus related error = %v, want deterministic error naming the missing target", err)
	}
	if _, _, err := runContextWithAgentRunner(t, emptyCfg, agent.Run, "Missing"); err == nil || !strings.Contains(err.Error(), "Missing") {
		t.Fatalf("empty-corpus context error = %v, want deterministic error naming the missing target", err)
	}
	if _, err := os.Stat(filepath.Join(emptyRoot, ".bnotes")); !os.IsNotExist(err) {
		t.Fatalf("empty-corpus reads created state: %v", err)
	}
}

func TestRecallCommandsAppearInHelpAndCompletion(t *testing.T) {
	isolateBnEnv(t)

	root := NewRootCmd()
	rootHelp := &bytes.Buffer{}
	root.SetOut(rootHelp)
	root.SetErr(rootHelp)
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("root help: %v", err)
	}
	for _, command := range []string{"related", "context"} {
		if !strings.Contains(rootHelp.String(), command) {
			t.Errorf("root help does not expose %q:\n%s", command, rootHelp.String())
		}
	}

	for _, test := range []struct {
		command string
		flags   []string
	}{
		{command: "related", flags: []string{"--mode", "--limit", "--paths", "--json", "--interactive", "--open"}},
		{command: "context", flags: []string{"--related", "--max-bytes", "--format", "--purpose", "--agent"}},
	} {
		cmd := NewRootCmd()
		help := &bytes.Buffer{}
		cmd.SetOut(help)
		cmd.SetErr(help)
		cmd.SetArgs([]string{test.command, "--help"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%s help: %v", test.command, err)
		}
		for _, flag := range test.flags {
			if !strings.Contains(help.String(), flag) {
				t.Errorf("%s help does not expose %s:\n%s", test.command, flag, help.String())
			}
		}
	}

	completionScript := &bytes.Buffer{}
	if err := NewRootCmd().GenZshCompletion(completionScript); err != nil {
		t.Fatalf("generate zsh completion: %v", err)
	}
	if !strings.Contains(completionScript.String(), "__complete") {
		t.Fatal("zsh completion script does not delegate to Cobra's runtime command completion")
	}

	completionRoot := NewRootCmd()
	completion := &bytes.Buffer{}
	completionRoot.SetOut(completion)
	completionRoot.SetErr(completion)
	completionRoot.SetArgs([]string{"__complete", ""})
	if err := completionRoot.Execute(); err != nil {
		t.Fatalf("request root command completions: %v", err)
	}
	for _, command := range []string{"related", "context"} {
		if !strings.Contains(completion.String(), command) {
			t.Errorf("runtime shell completion does not expose %q:\n%s", command, completion.String())
		}
	}
}
