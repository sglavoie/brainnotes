package web

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/config"
	"github.com/sglavoie/sglavoie_brainnotes/internal/noteops"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultgit"
	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

// templateDestination is this surface's whole path policy, and it is deliberately
// its own rather than noteops.resolveSourceDestination's.
//
// A note destination is refused by vault.WalkExclusion precisely when it is
// inside the templates directory, because a note written there would exist and be
// invisible to every query. That refusal is correct and stays correct: the fix is
// not to weaken it for this surface but to state the narrower policy a template
// needs, which is a flat file directly inside the configured directory.
//
// Four refusals, and each of them is the reason a template editor can be trusted
// with a client-supplied name:
//
//   - The name is a bare filename. No separator, no NUL, no "." or "..", and no
//     leading dot — so it cannot climb out of the directory, cannot address a
//     nested path inside it, and cannot create a hidden file that vault.Walk and
//     template.Find would both then ignore.
//   - The extension is the configured default or ".md". template.Find looks a
//     template up by the configured extension, so a file with any other suffix is
//     one `bn new` could never resolve; writing it would be this surface
//     inventing a kind of file the vault has no use for.
//   - The resolved path is inside the vault and resolves to exactly where it was
//     asked for, so a symlink in the templates directory cannot redirect the
//     write to a target the policy never judged.
//   - The configured directory is itself checked, so a vault whose templates.dir
//     is a hidden or ignored path is refused here instead of silently writing
//     files outside the synchronization scope.
func templateDestination(root string, cfg *config.Config, name string) (noteops.PathResolution, string, error) {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	name, err = checkTemplateName(cfg, name)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	rel := dir + "/" + name
	dest, err := noteops.PathForRel(root, rel)
	if err != nil {
		return noteops.PathResolution{}, "", err
	}
	if dest.RelPath != rel {
		return noteops.PathResolution{}, "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %s must not resolve through a symlink", rel)
	}
	return dest, name, nil
}

// checkTemplateName refuses every name that is not a flat file this vault could
// render a note from.
func checkTemplateName(cfg *config.Config, name string) (string, error) {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "", noteops.Errorf(noteops.CodeInvalidRequest, "templates: name is required")
	case strings.ContainsAny(name, `/\`):
		return "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %q must not contain path separators", name)
	case strings.ContainsRune(name, 0):
		return "", noteops.Errorf(noteops.CodeForbidden, "templates: name must not contain NUL bytes")
	case strings.HasPrefix(name, "."):
		// A hidden file is pruned by vault.Walk and skipped by template.Find, so it
		// would be a template nothing in the vault can use.
		return "", noteops.Errorf(noteops.CodeForbidden,
			"templates: %q must not start with a dot", name)
	case name != path.Clean(name):
		return "", noteops.Errorf(noteops.CodeForbidden, "templates: %q is not a plain file name", name)
	case !templateExtensionAllowed(cfg, name):
		return "", noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: %q must end in %s", name, templateExtensionList(cfg))
	}
	return name, nil
}

// templateExtensionAllowed reports whether name carries a suffix `bn new` could
// resolve. ".md" is accepted beside the configured extension because a vault that
// configures something else still holds the Markdown templates it had before.
func templateExtensionAllowed(cfg *config.Config, name string) bool {
	ext := path.Ext(name)
	if ext == "" || ext == name {
		return false
	}
	for _, allowed := range templateExtensions(cfg) {
		if strings.EqualFold(ext, allowed) {
			return true
		}
	}
	return false
}

func templateExtensions(cfg *config.Config) []string {
	configured := ".md"
	if cfg != nil && strings.TrimSpace(cfg.Templates.DefaultExtension) != "" {
		configured = cfg.Templates.DefaultExtension
	}
	if strings.EqualFold(configured, ".md") {
		return []string{".md"}
	}
	return []string{configured, ".md"}
}

func templateExtensionList(cfg *config.Config) string {
	return strings.Join(templateExtensions(cfg), " or ")
}

// templateDirRel returns the configured templates directory as a clean
// vault-relative slash path, refusing one this surface must not write into.
//
// config.applyDefaults already guarantees it is relative, cleaned and non-empty,
// so what is left to check is the pair of properties a template file depends on:
// it must not be hidden or ignored, because vault.Walk would prune it — which is
// what the caller *wants* for a template — while vaultgit and vaultsync would
// judge a hidden component out of scope, which would leave the file with no undo
// history and no way to reach a peer.
func templateDirRel(cfg *config.Config) (string, error) {
	dir := ""
	if cfg != nil {
		dir = strings.Trim(strings.TrimSpace(filepath.ToSlash(cfg.Templates.Dir)), "/")
	}
	if dir == "" {
		dir = "Templates"
	}
	if dir != path.Clean(dir) || noteops.RelEscapesRoot(dir) {
		return "", templateDirUnusableError(cfg, "it is not a vault-relative directory")
	}
	for _, part := range strings.Split(dir, "/") {
		if strings.HasPrefix(part, ".") {
			return "", templateDirUnusableError(cfg, "a hidden path is outside the undo and synchronization scopes")
		}
	}
	return dir, nil
}

func templateDirUnusableError(cfg *config.Config, reason string) error {
	dir := ""
	if cfg != nil {
		dir = cfg.Templates.Dir
	}
	return noteops.Errorf(noteops.CodeConflict,
		"templates: this vault's templates.dir (%q) cannot be edited here because %s", dir, reason)
}

// templateDirStatusOf answers the two questions this surface is accountable for,
// by asking the same predicates the rest of the vault does.
func templateDirStatusOf(cfg *config.Config) templateDirStatus {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return templateDirStatus{}
	}
	probe := dir + "/probe.md"
	_, excluded := vault.WalkExclusion(cfg, probe)
	return templateDirStatus{
		ExcludedFromNotes: excluded,
		Portable:          templatePathTravels(probe),
	}
}

// templatePathTravels reports whether a file at rel is covered by both the
// content history and the synchronization scope — undoable here, and portable to
// an enrolled peer.
//
// It asks the two packages that own those policies rather than restating them.
// They are independent — vaultgit keeps an undo journal this machine alone
// reads, vaultsync decides what may leave it — and a template has to be in both
// for "edited, undone, and synchronized" to be true of it.
func templatePathTravels(rel string) bool {
	return vaultgit.InScope(rel) && vaultsync.Portable(rel)
}

// listTemplates lists the files in the configured directory that this surface
// can edit. A directory that does not exist yet is an empty list rather than an
// error: the first save creates it.
func listTemplates(root string, cfg *config.Config) ([]templateSummary, error) {
	dir, err := templateDirRel(cfg)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		if os.IsNotExist(err) {
			return []templateSummary{}, nil
		}
		return nil, noteops.WrapError(noteops.CodeInternal, "templates: read "+dir, err)
	}

	out := make([]templateSummary, 0, len(entries))
	for _, entry := range entries {
		if summary, listed := templateRow(root, dir, cfg, entry); listed {
			out = append(out, summary)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// templateRow describes one directory entry as a row, or reports that it is not
// one.
//
// Three entries are skipped rather than refused, because the listing describes
// what this surface can edit and none of them can be: a name the policy resolves
// to something other than itself — every read and write here addresses the
// resolved name, so such a row's size and revision would describe somebody
// else's bytes — a symlink, and a directory.
func templateRow(root, dir string, cfg *config.Config, entry os.DirEntry) (templateSummary, bool) {
	name := entry.Name()
	if resolved, err := checkTemplateName(cfg, name); err != nil || resolved != name {
		return templateSummary{}, false
	}
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() {
		return templateSummary{}, false
	}
	summary := templateSummary{Name: name, RelPath: dir + "/" + name, Size: info.Size()}
	if data, exists, err := readTemplateBytes(filepath.Join(root, filepath.FromSlash(summary.RelPath))); err == nil && exists {
		summary.Revision = vault.HashBytes(data)
	}
	return summary, true
}

// readTemplate serves one template's exact bytes and the revision an edit has to
// echo back.
func readTemplate(root string, cfg *config.Config, name string) (templateSummary, error) {
	dest, resolved, err := templateDestination(root, cfg, name)
	if err != nil {
		return templateSummary{}, err
	}
	data, exists, err := readTemplateBytes(dest.AbsPath)
	if err != nil {
		return templateSummary{}, err
	}
	if !exists {
		return templateSummary{}, noteops.Errorf(noteops.CodeNotFound, "templates: %s does not exist", dest.RelPath)
	}
	return templateSummary{
		Name:     resolved,
		RelPath:  dest.RelPath,
		Size:     int64(len(data)),
		Revision: vault.HashBytes(data),
		Content:  string(data),
	}, nil
}

// readTemplateBytes reads a template verbatim, reporting absence rather than
// failing on it. Anything that is not a regular file is refused: a directory or a
// symlink under the templates directory is not something this surface may edit,
// and treating one as content would hand back bytes from wherever it points.
func readTemplateBytes(absPath string) ([]byte, bool, error) {
	info, err := os.Lstat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, noteops.WrapError(noteops.CodeInternal, "templates: stat template", err)
	}
	if !info.Mode().IsRegular() {
		return nil, false, noteops.Errorf(noteops.CodeForbidden, "templates: %s is not a regular file", filepath.Base(absPath))
	}
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, false, noteops.WrapError(noteops.CodeInternal, "templates: read template", err)
	}
	if len(data) > maxTemplateBytes {
		return nil, false, noteops.Errorf(noteops.CodeInvalidRequest,
			"templates: %s is larger than the %d byte maximum this API serves", filepath.Base(absPath), maxTemplateBytes)
	}
	return data, true, nil
}
