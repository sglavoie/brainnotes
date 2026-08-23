package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
)

func TestSynthesisGenerateAPI_SendsStoredPacketAndConfiguredArgv(t *testing.T) {
	var gotArgv []string
	var gotStdin []byte
	gotDeadline := false

	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent", "--draft", "literal argument"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(ctx context.Context, argv []string, stdin []byte) ([]byte, error) {
				gotArgv = append([]string(nil), argv...)
				gotStdin = append([]byte(nil), stdin...)
				_, gotDeadline = ctx.Deadline()
				return []byte("# Draft\n\nA synthesized paragraph.\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	if !built.AgentEnabled {
		t.Fatal("agent_enabled = false with [agent].command configured")
	}

	form := url.Values{}
	form.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	decoded := decodeSynthesisGenerate(t, body)
	if !decoded.OK || !decoded.Enabled {
		t.Fatalf("ok/enabled = %v/%v, want true/true", decoded.OK, decoded.Enabled)
	}
	if decoded.ContextID != built.ContextID {
		t.Fatalf("context_id = %q, want %q", decoded.ContextID, built.ContextID)
	}
	if decoded.Output != "# Draft\n\nA synthesized paragraph.\n" {
		t.Fatalf("output = %q, want the command stdout verbatim", decoded.Output)
	}
	if !strings.Contains(decoded.RenderedHTML, "<h1") || !strings.Contains(decoded.RenderedHTML, "A synthesized paragraph.") {
		t.Fatalf("rendered_html did not render the candidate Markdown: %q", decoded.RenderedHTML)
	}

	requireKeyOrder(t, gotArgv, []string{"synth-agent", "--draft", "literal argument"}, "argv")
	if string(gotStdin) != built.Markdown {
		t.Fatal("agent stdin is not the stored packet bytes")
	}
	if !gotDeadline {
		t.Fatal("configured timeout did not reach the command context")
	}
}

// TestSynthesisGenerateAPI_ContextIsReusable proves one packet can be generated
// from more than once, which is what makes a retry cheap.
func TestSynthesisGenerateAPI_ContextIsReusable(t *testing.T) {
	runs := 0
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				runs++
				return []byte("Draft body.\n"), nil
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	for attempt := range 2 {
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
		if status != http.StatusOK {
			t.Fatalf("attempt %d status = %d, body = %s", attempt, status, body)
		}
	}
	if runs != 2 {
		t.Fatalf("agent ran %d times, want 2", runs)
	}
}

func TestSynthesisGenerateAPI_DisabledWithoutConfiguredAgent(t *testing.T) {
	ran := false
	ts, _, _ := newSynthesisServer(t, Options{
		RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
			ran = true
			return nil, nil
		},
	})

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	decoded := decodeSynthesisGenerate(t, body)
	if !decoded.OK || decoded.Enabled {
		t.Fatalf("ok/enabled = %v/%v, want true/false", decoded.OK, decoded.Enabled)
	}
	if !strings.Contains(decoded.Message, "[agent].command") {
		t.Fatalf("message = %q, want an explanation naming [agent].command", decoded.Message)
	}
	if decoded.Output != "" || decoded.RenderedHTML != "" {
		t.Fatal("disabled response carries candidate content")
	}
	if ran {
		t.Fatal("the command ran with no [agent].command configured")
	}
}

func TestSynthesisGenerateAPI_RejectsBadRequests(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				t.Error("the command ran for a rejected request")
				return nil, nil
			},
		},
	)

	t.Run("non-POST", func(t *testing.T) {
		resp, err := http.Get(ts.URL + synthesisGenerateAPIRoutePath) // #nosec G107 -- test-only local listener URL
		if err != nil {
			t.Fatalf("GET generate: %v", err)
		}
		body := readResponseBody(t, resp)
		if got := resp.Header.Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
		requireSynthesisError(t, resp.StatusCode, body, http.StatusMethodNotAllowed, noteops.CodeInvalidRequest)
	})

	t.Run("csrf", func(t *testing.T) {
		built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
		form := url.Values{}
		form.Set("context_id", built.ContextID)
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, "", form)
		requireSynthesisError(t, status, body, http.StatusForbidden, noteops.CodeForbidden)
	})

	t.Run("missing context id", func(t *testing.T) {
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, url.Values{})
		requireSynthesisError(t, status, body, http.StatusBadRequest, noteops.CodeInvalidRequest)
	})

	t.Run("unknown context id", func(t *testing.T) {
		form := url.Values{}
		form.Set("context_id", strings.Repeat("a", 64))
		status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
		requireSynthesisError(t, status, body, http.StatusNotFound, noteops.CodeNotFound)
	})
}

func TestSynthesisGenerateAPI_RejectsUnusableCandidates(t *testing.T) {
	for name, output := range map[string][]byte{
		"invalid utf-8": []byte("# Draft\n\xff\xfe not text\n"),
		"oversize":      make([]byte, synthesisCandidateMaxBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			ts, _, _ := newSynthesisServerWithConfig(
				t,
				synthesisAgentConfigTOML(`"synth-agent"`),
				synthesisTestNotes(),
				Options{
					RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
						return output, nil
					},
				},
			)

			built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
			form := url.Values{}
			form.Set("context_id", built.ContextID)

			status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)
		})
	}
}

// TestSynthesisGenerateAPI_SanitizesFailureDiagnostic keeps a failing command's
// own words out of the response as raw bytes: the browser sees one bounded,
// control-character-free line.
func TestSynthesisGenerateAPI_SanitizesFailureDiagnostic(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return nil, errors.New("boom\n\x00\x07multi\nline\tdiagnostic")
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)

	decoded := decodeSynthesisError(t, body)
	if !strings.Contains(decoded.Message, "boom multi line diagnostic") {
		t.Fatalf("message = %q, want the collapsed diagnostic", decoded.Message)
	}
	if strings.ContainsAny(decoded.Message, "\n\t\x00\x07") {
		t.Fatalf("message = %q, want no control characters", decoded.Message)
	}
}

func TestSynthesisGenerateAPI_TimeoutIsReported(t *testing.T) {
	ts, _, _ := newSynthesisServerWithConfig(
		t,
		synthesisTestConfigTOML+"\n[agent]\ncommand = [\"synth-agent\"]\ntimeout_seconds = 1\n",
		synthesisTestNotes(),
		Options{
			RunAgent: func(ctx context.Context, _ []string, _ []byte) ([]byte, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	)

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusInternalServerError, noteops.CodeInternal)
	if decoded := decodeSynthesisError(t, body); !strings.Contains(decoded.Message, "timed out") {
		t.Fatalf("message = %q, want a timeout explanation", decoded.Message)
	}
}

func TestSynthesisGenerateAPI_RefusesStaleContext(t *testing.T) {
	for name, disturb := range map[string]func(t *testing.T, root string){
		"edited supporter": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha now says something else entirely.\n\nAnd a second paragraph.\n")
		},
		"edited anchor": func(t *testing.T, root string) {
			writeTestFile(t, root, "Notes/Anchor.md", "---\ntitle: Anchor\n---\nAnchor now says something else entirely.\n\nAnd a second paragraph.\n")
		},
		"moved supporter": func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "Moved"), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.Rename(filepath.Join(root, "Notes", "Beta.md"), filepath.Join(root, "Moved", "Beta.md")); err != nil {
				t.Fatalf("rename: %v", err)
			}
		},
		"removed supporter": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, "Notes", "Beta.md")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		},
		"changed configuration": func(t *testing.T, root string) {
			if err := os.WriteFile(
				filepath.Join(root, config.FileName),
				[]byte(synthesisTestConfigTOML+"\n[frontmatter.properties.status]\ntype = \"string\"\n"),
				0o644,
			); err != nil {
				t.Fatalf("write config: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			ts, root, contextID, runs := newStaleSynthesisServer(t)
			disturb(t, root)

			form := url.Values{}
			form.Set("context_id", contextID)
			status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
			requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
			if *runs != 0 {
				t.Fatalf("the command ran %d times against a stale context", *runs)
			}
		})
	}
}

// TestSynthesisGenerateAPI_RefusesSourceMutatedDuringRun covers the second
// revalidation: a candidate that reasoned over bytes replaced while it ran
// describes a vault that no longer exists.
func TestSynthesisGenerateAPI_RefusesSourceMutatedDuringRun(t *testing.T) {
	var root string
	ts, _, vaultRoot := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				writeTestFile(t, root, "Notes/Alpha.md", "---\ntitle: Alpha\n---\nAlpha changed while the command ran.\n\nSecond paragraph.\n")
				return []byte("Draft body.\n"), nil
			},
		},
	)
	root = vaultRoot

	built := buildSynthesisContextRequest(t, ts.URL, defaultSynthesisContextForm())
	form := url.Values{}
	form.Set("context_id", built.ContextID)

	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, form)
	requireSynthesisError(t, status, body, http.StatusConflict, noteops.CodeConflict)
	if strings.Contains(body, "Draft body.") {
		t.Fatal("a candidate built from stale sources was returned")
	}
}

// TestSynthesisAPI_PersistsNothing pins the session's privacy contract: building
// a context and generating a candidate leave the vault byte-identical.
func TestSynthesisAPI_PersistsNothing(t *testing.T) {
	ts, _, root := newSynthesisServerWithConfig(
		t,
		synthesisAgentConfigTOML(`"synth-agent"`),
		synthesisTestNotes(),
		Options{
			RunAgent: func(context.Context, []string, []byte) ([]byte, error) {
				return []byte("Draft body mentioning quorum size.\n"), nil
			},
		},
	)

	before := vaultFileSet(t, root)

	form := defaultSynthesisContextForm()
	form.Set("prompt", "A secret prompt worth remembering.")
	built := buildSynthesisContextRequest(t, ts.URL, form)

	generate := url.Values{}
	generate.Set("context_id", built.ContextID)
	status, body := postSynthesisForm(t, ts.URL, synthesisGenerateAPIRoutePath, synthesisTestToken, generate)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}

	after := vaultFileSet(t, root)
	requireKeyOrder(t, after, before, "vault contents")
}
