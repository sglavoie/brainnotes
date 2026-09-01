package cssbuild

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot is the checkout this package lives in, which is what the drift test
// needs to build the real stylesheet.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

// TestCommittedStylesheetMatchesSources is the drift check. The bundle is
// committed so Go builds need no asset step, which means the only thing keeping
// it honest is a test that rebuilds it and compares. It needs no toolchain, so
// `go test ./...` catches a stale stylesheet even where Node is absent.
func TestCommittedStylesheetMatchesSources(t *testing.T) {
	root := repoRoot(t)

	want, err := Build(root)
	if err != nil {
		t.Fatalf("build stylesheet: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(OutputFile)))
	if err != nil {
		t.Fatalf("read committed stylesheet: %v", err)
	}

	if string(got) != string(want) {
		t.Fatalf("%s is not what %s/%s currently produces; run `just web-build` (or `go run ./internal/web/cssbuild/gen`)", OutputFile, SourceDir, ManifestFile)
	}
}

// TestCascadeAnchorsKeepTheirPositions pins the two ends of the cascade. Every
// part reads the custom properties tokens.css declares, and responsive.css only
// narrows components by coming after them, so an import reordered past either
// end is a silent visual regression rather than a build failure.
func TestCascadeAnchorsKeepTheirPositions(t *testing.T) {
	parts, err := manifestParts(filepath.Join(repoRoot(t), filepath.FromSlash(SourceDir)))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	if parts[0] != "tokens.css" {
		t.Errorf("first import = %q, want tokens.css", parts[0])
	}
	if last := parts[len(parts)-1]; last != "responsive.css" {
		t.Errorf("last import = %q, want responsive.css", last)
	}
}

func writeManifest(t *testing.T, body string, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ManifestFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for name, content := range extra {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func TestManifestParsesImportsAndSkipsComments(t *testing.T) {
	dir := writeManifest(t, "/*\n * order matters\n */\n@import \"a.css\";\n\n@import \"b.css\";\n", nil)

	parts, err := manifestParts(dir)
	if err != nil {
		t.Fatalf("manifestParts: %v", err)
	}
	if strings.Join(parts, ",") != "a.css,b.css" {
		t.Fatalf("parts = %v, want [a.css b.css]", parts)
	}
}

func TestManifestRejectsMalformedLines(t *testing.T) {
	cases := map[string]string{
		"bare rule":         ".site-nav { color: red; }\n",
		"nested path":       "@import \"nested/a.css\";\n",
		"parent traversal":  "@import \"../a.css\";\n",
		"missing semicolon": "@import \"a.css\"\n",
		"duplicate":         "@import \"a.css\";\n@import \"a.css\";\n",
		"no imports":        "/* nothing here */\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := manifestParts(writeManifest(t, body, nil)); err == nil {
				t.Fatalf("manifestParts accepted %q", body)
			}
		})
	}
}

// TestBuildRejectsAnUnimportedPart covers the quiet failure: a part added to the
// directory but never imported still builds a valid stylesheet, so the only
// symptom is a page rendering unstyled.
func TestBuildRejectsAnUnimportedPart(t *testing.T) {
	dir := writeManifest(t, "@import \"a.css\";\n", map[string]string{
		"a.css":      "a { color: red; }\n",
		"orphan.css": "b { color: blue; }\n",
	})

	err := checkEveryPartImported(dir, []string{"a.css"})
	if err == nil {
		t.Fatal("checkEveryPartImported accepted an unimported part")
	}
	if !strings.Contains(err.Error(), "orphan.css") {
		t.Fatalf("error = %q, want it to name orphan.css", err)
	}
}

func TestBuildConcatenatesPartsInManifestOrder(t *testing.T) {
	dir := writeManifest(t, "@import \"second.css\";\n@import \"first.css\";\n", map[string]string{
		"second.css": "b { color: blue; }\n",
		"first.css":  "a { color: red; }\n",
	})
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(SourceDir)), 0o755); err != nil {
		t.Fatalf("mkdir source dir: %v", err)
	}
	for _, name := range []string{ManifestFile, "second.css", "first.css"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(SourceDir), name), body, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	out, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	body := string(out)
	if !strings.HasPrefix(body, banner) {
		t.Fatal("build output is missing its generated-file banner")
	}
	if strings.Index(body, "b { color: blue; }") > strings.Index(body, "a { color: red; }") {
		t.Fatalf("parts were not concatenated in manifest order:\n%s", body)
	}
}

// TestWriteBundleSkipsAnUnchangedFile keeps `bn serve` from touching the
// artifact's mtime on every start.
func TestWriteBundleSkipsAnUnchangedFile(t *testing.T) {
	root := t.TempDir()
	sourceDir := filepath.Join(root, filepath.FromSlash(SourceDir))
	outputDir := filepath.Join(root, filepath.Dir(filepath.FromSlash(OutputFile)))
	for _, dir := range []string{sourceDir, outputDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceDir, ManifestFile), []byte("@import \"a.css\";\n"), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "a.css"), []byte("a { color: red; }\n"), 0o644); err != nil {
		t.Fatalf("write part: %v", err)
	}

	changed, err := WriteBundle(root)
	if err != nil {
		t.Fatalf("WriteBundle: %v", err)
	}
	if !changed {
		t.Fatal("first WriteBundle reported no change")
	}

	changed, err = WriteBundle(root)
	if err != nil {
		t.Fatalf("second WriteBundle: %v", err)
	}
	if changed {
		t.Fatal("second WriteBundle rewrote an identical stylesheet")
	}
}
