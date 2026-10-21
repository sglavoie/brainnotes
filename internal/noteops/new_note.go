package noteops

import (
	"strings"

	"github.com/sglavoie/brainnotes/internal/wikilink"
)

// NewNoteSource prepares a title-only note without requiring an existing note
// as context. SaveSource still enforces the vault's destination policy.
func NewNoteSource(title, folder string) (SaveSourceRequest, error) {
	title = stripMarkdownExtension(strings.TrimSpace(title))
	if err := validateNoteTitle("new note:", title); err != nil {
		return SaveSourceRequest{}, err
	}
	if strings.ContainsRune(title, '\\') {
		return SaveSourceRequest{}, Errorf(CodeInvalidRequest, "new note: title may not contain a path separator")
	}
	folder = strings.TrimSpace(strings.ReplaceAll(folder, `\`, "/"))
	if folder != "" {
		clean, err := CleanRelPath(folder)
		if err != nil {
			return SaveSourceRequest{}, err
		}
		if clean != folder || targetLooksAbsolute(folder) || containsControlRune(folder) {
			return SaveSourceRequest{}, Errorf(CodeInvalidRequest, "new note: use a vault-relative folder without traversal")
		}
		if _, reserved := wikilink.FirstReservedTargetRune(folder); reserved {
			return SaveSourceRequest{}, Errorf(CodeInvalidRequest, "new note: folder contains wikilink syntax characters")
		}
		folder += "/"
	}
	content, err := stubFrontmatterContent(title)
	if err != nil {
		return SaveSourceRequest{}, err
	}
	return SaveSourceRequest{RelPath: folder + title + ".md", Content: string(content), Create: true, ExclusiveCreate: true}, nil
}
