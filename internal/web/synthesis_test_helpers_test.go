package web

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/index"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

const synthesisTestToken = "csrf-token"

const synthesisTestConfigTOML = `[frontmatter.properties.tags]
type = "list"
items = "string"
`

// synthesisTestNotes gives every note two prose paragraphs so each one can
// contribute the mandatory exact excerpt a curated packet requires.
func synthesisTestNotes() map[string]string {
	return map[string]string{
		"Notes/Anchor.md": "---\ntags:\n    - consensus\ntitle: Anchor\n---\nAnchor weighs quorum size against write latency in a replicated log.\n\nA second anchor paragraph revisits leader election under partition.\n",
		"Notes/Alpha.md":  "---\ntags:\n    - consensus\ntitle: Alpha\n---\nAlpha measures quorum size against consensus latency across three clusters.\n\nAlpha closes on leader election failure modes during a partition.\n",
		"Notes/Beta.md":   "---\ntags:\n    - consensus\ntitle: Beta\n---\nBeta compares consensus protocols by their quorum requirements.\n\nBeta ends with open questions about replicated log latency.\n",
		"Other/Gamma.md":  "---\ntitle: Gamma\n---\nGamma is unrelated prose about soil composition in raised garden beds.\n\nGamma also notes the drainage layer depth that worked best.\n",
	}
}

const (
	synthesisAnchorKey = "notes/anchor"
	synthesisAlphaKey  = "notes/alpha"
	synthesisBetaKey   = "notes/beta"
	synthesisGammaKey  = "other/gamma"
)

// newSynthesisServer builds a server whose configuration really lives on disk,
// which the synthesis freshness check needs: it digests brainnotes.toml rather
// than trusting the published reload bookkeeping.
func newSynthesisServer(t *testing.T, opts Options) (*httptest.Server, *Server, string) {
	t.Helper()
	return newSynthesisServerWithConfig(t, synthesisTestConfigTOML, synthesisTestNotes(), opts)
}

func newSynthesisServerWithConfig(
	t *testing.T,
	source string,
	files map[string]string,
	opts Options,
) (*httptest.Server, *Server, string) {
	t.Helper()

	root := t.TempDir()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeTestFile(t, root, name, files[name])
	}
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(source), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := config.Decode(root, config.VaultOriginFlag, []byte(source))
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	disabled := false
	cfg.Serve.Addr = "127.0.0.1:0"
	cfg.Serve.OpenBrowser = &disabled
	v, err := vault.Open(cfg)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}
	if opts.CSRFToken == "" {
		opts.CSRFToken = synthesisTestToken
	}
	srv, err := NewServer(cfg, v, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv, root
}

// postSynthesisForm sends a form-encoded body with the CSRF header set. An empty
// token omits the header entirely, which is how the refusal tests are reached.
func postSynthesisForm(t *testing.T, baseURL, route, token string, form url.Values) (int, string) {
	t.Helper()

	req, err := http.NewRequest(http.MethodPost, baseURL+route, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token != "" {
		req.Header.Set(csrfHeaderName, token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", route, err)
	}
	return resp.StatusCode, readResponseBody(t, resp)
}

// buildSynthesisContextRequest posts one context request and fails the test
// unless it succeeds, so the many tests that only need a live context_id do not
// repeat the assertion.
func buildSynthesisContextRequest(t *testing.T, baseURL string, form url.Values) synthesisContextResponse {
	t.Helper()

	status, body := postSynthesisForm(t, baseURL, synthesisContextAPIRoutePath, synthesisTestToken, form)
	if status != http.StatusOK {
		t.Fatalf("context status = %d, body = %s", status, body)
	}
	return decodeSynthesisContext(t, body)
}

func defaultSynthesisContextForm() url.Values {
	form := url.Values{}
	form.Set("source", synthesisAnchorKey)
	form.Add("candidate", synthesisAlphaKey)
	form.Add("candidate", synthesisBetaKey)
	return form
}

func decodeSynthesisContext(t *testing.T, body string) synthesisContextResponse {
	t.Helper()

	var decoded synthesisContextResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode context response %q: %v", body, err)
	}
	return decoded
}

func decodeSynthesisGenerate(t *testing.T, body string) synthesisGenerateResponse {
	t.Helper()

	var decoded synthesisGenerateResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode generate response %q: %v", body, err)
	}
	return decoded
}

func decodeSynthesisError(t *testing.T, body string) noteops.ErrorResult {
	t.Helper()

	var decoded noteops.ErrorResult
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode error response %q: %v", body, err)
	}
	return decoded
}

// requireSynthesisError asserts both halves of the management envelope at once,
// because a wrong status with the right code (or the reverse) is a real
// regression the browser would act on.
func requireSynthesisError(t *testing.T, status int, body string, wantStatus int, wantCode noteops.ErrorCode) {
	t.Helper()

	if status != wantStatus {
		t.Fatalf("status = %d, want %d (body = %s)", status, wantStatus, body)
	}
	decoded := decodeSynthesisError(t, body)
	if decoded.OK {
		t.Fatalf("ok = true, want an error envelope (body = %s)", body)
	}
	if decoded.Code != wantCode {
		t.Fatalf("code = %q, want %q (body = %s)", decoded.Code, wantCode, body)
	}
}

func synthesisAgentConfigTOML(argv string) string {
	return synthesisTestConfigTOML + "\n[agent]\ncommand = [" + argv + "]\ntimeout_seconds = 30\n"
}

func supporterKeys(t *testing.T, supporters []synthesisNoteIdentity) []string {
	t.Helper()

	keys := make([]string, len(supporters))
	for index := range supporters {
		keys[index] = supporters[index].Key
	}
	return keys
}

func requireKeyOrder(t *testing.T, got, want []string, label string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s = %v, want %v", label, got, want)
		}
	}
}

// newStaleSynthesisServer builds a configured-agent server and one live context,
// returning everything a staleness case needs to disturb the vault between the
// build and the run.
func newStaleSynthesisServer(t *testing.T) (*httptest.Server, string, string, *int) {
	t.Helper()

	runs := 0
	ts, _, root := newSynthesisServerWithConfig(
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
	return ts, root, built.ContextID, &runs
}

// vaultFileSet returns every file under root as "<relpath>:<size>", sorted, so a
// test can assert the whole vault is untouched.
func vaultFileSet(t *testing.T, root string) []string {
	t.Helper()

	var entries []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// The note cache is a pure function of the vault: it holds no user
		// intent, `rm -rf .bnotes/index` is a supported repair, and every
		// snapshot load writes it. Counting it here would make "this endpoint
		// persisted nothing" mean "nothing was read either", which is not the
		// claim these tests exist to make.
		if filepath.ToSlash(rel) == index.RelDir {
			return fs.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, filepath.ToSlash(rel)+":"+strconv.FormatInt(info.Size(), 10))
		return nil
	})
	if err != nil {
		t.Fatalf("walk vault: %v", err)
	}
	sort.Strings(entries)
	return entries
}
