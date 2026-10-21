package noteops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sglavoie/brainnotes/internal/vault"
)

func TestSaveSourceRefusesAnIncoherentAttachmentList(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(AttachmentCommit) []AttachmentCommit
	}{
		{
			name: "two uploads claiming one destination",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				other := c
				other.UploadID = "bbbb1111"
				return []AttachmentCommit{c, other}
			},
		},
		{
			name: "one upload listed twice",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				return []AttachmentCommit{c, c}
			},
		},
		{
			name: "no upload id",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				c.UploadID = "  "
				return []AttachmentCommit{c}
			},
		},
		{
			name: "no digest",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				c.Digest = ""
				return []AttachmentCommit{c}
			},
		},
		{
			name: "a digest that is not one",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				c.Digest = "not-a-sha256"
				return []AttachmentCommit{c}
			},
		},
		{
			name: "no staged file",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				c.SourcePath = ""
				return []AttachmentCommit{c}
			},
		},
		{
			name: "a negative size",
			mutate: func(c AttachmentCommit) []AttachmentCommit {
				c.Size = -1
				return []AttachmentCommit{c}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, cfg := sourceTestVault(t)
			base := stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)
			_, err := SaveSource(root, cfg, SaveSourceRequest{
				RelPath:      "Alpha.md",
				Content:      "---\ntitle: Alpha\n---\n\nbody\n",
				BaseRevision: vault.HashBytes([]byte(sourceFixture)),
				Attachments:  tc.mutate(base),
			})
			if got := CodeOf(err); got != CodeInvalidRequest {
				t.Fatalf("SaveSource err = %v (code %s), want invalid_request", err, got)
			}
			assertSourceUnchanged(t, root)
			assertNoAttachment(t, root, "attachments/photo.png")
		})
	}
}

// The bound is about the transaction: every attachment is another locked path,
// another staged file and another snapshot, and a save is one interactive
// request.
func TestSaveSourceRefusesMoreAttachmentsThanOneTransactionShouldCarry(t *testing.T) {
	root, cfg := sourceTestVault(t)
	commits := make([]AttachmentCommit, 0, maxSaveAttachments+1)
	for i := range maxSaveAttachments + 1 {
		commits = append(commits, stageForCommit(t,
			"upload-"+string(rune('a'+i%26))+string(rune('a'+i/26)),
			filepath.ToSlash(filepath.Join("attachments", "photo-"+string(rune('a'+i%26))+string(rune('a'+i/26))+".png")),
			binaryPayload))
	}

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\nbody\n",
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  commits,
	})
	if got := CodeOf(err); got != CodeInvalidRequest {
		t.Fatalf("SaveSource err = %v (code %s), want invalid_request", err, got)
	}
	assertSourceUnchanged(t, root)
}

// The digest is the transaction's only way to tell the file it was promised from
// whatever is at that path when it copies. A staged file that changed underneath
// must fail the commit rather than land.
func TestSaveSourceRefusesAStagedFileThatNoLongerMatchesItsDigest(t *testing.T) {
	root, cfg := sourceTestVault(t)
	photo := stageForCommit(t, "aaaa0000", "attachments/photo.png", binaryPayload)
	if err := os.WriteFile(photo.SourcePath, []byte("substituted after staging"), 0o600); err != nil {
		t.Fatalf("rewrite the staged file: %v", err)
	}

	_, err := SaveSource(root, cfg, SaveSourceRequest{
		RelPath:      "Alpha.md",
		Content:      "---\ntitle: Alpha\n---\n\n![[attachments/photo.png]]\n",
		BaseRevision: vault.HashBytes([]byte(sourceFixture)),
		Attachments:  []AttachmentCommit{photo},
	})
	if err == nil {
		t.Fatal("SaveSource committed a staged file that no longer holds the bytes it was weighed at")
	}
	assertSourceUnchanged(t, root)
	assertNoAttachment(t, root, "attachments/photo.png")
}
