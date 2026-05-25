//go:build unix

package vault

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// TestSaveWritesNewFileMode verifies that a fresh save respects the process
// umask instead of leaking os.CreateTemp's 0o600 default.
func TestSaveWritesNewFileMode(t *testing.T) {
	cases := []struct {
		name string
		mask int
		want os.FileMode
	}{
		{name: "shared read umask", mask: 0o022, want: 0o644},
		{name: "private umask", mask: 0o077, want: 0o600},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			oldUmask := syscall.Umask(tc.mask)
			t.Cleanup(func() { syscall.Umask(oldUmask) })

			v := newTestVault(t, nil, map[string]string{
				"a.md": "---\ntitle: A\n---\nbody\n",
			})
			n, err := v.ResolveByTitle("a", "")
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if err := n.Load(); err != nil {
				t.Fatalf("load: %v", err)
			}
			newPath := filepath.Join(v.Root, "brand-new.md")
			n.Path = newPath
			if err := n.Save(&v.Cfg.Frontmatter); err != nil {
				t.Fatalf("save: %v", err)
			}
			info, err := os.Stat(newPath)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if got := info.Mode().Perm(); got != tc.want {
				t.Errorf("new file mode: got %o, want %o", got, tc.want)
			}
		})
	}
}
