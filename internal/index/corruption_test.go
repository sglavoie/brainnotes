package index

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vault"
)

// The index holds zero user intent — every byte in it is a pure function of the
// vault — so unlike internal/recall's state store it never fails closed on a
// state file it cannot trust. It discards it and rebuilds. This file is the
// evidence for that: every way the file can be wrong resolves to the same
// observable outcome, a correct answer with no error surfaced to the caller.

// corruptionFixture is small on purpose: these tests are about the state file,
// not about the walk, which parity_test.go already covers.
var corruptionFixture = map[string]string{
	"Alpha.md":       "---\nstatus: wip\n---\nalpha\n",
	"Beta.md":        "---\nstatus: done\n---\nbeta\n",
	"Notes/Gamma.md": "---\nstatus: wip\n---\ngamma\n",
}

var corruptionWant = []string{"Alpha.md", "Beta.md", "Notes/Gamma.md"}

func TestCorruptStateRebuildsSilently(t *testing.T) {
	cases := []struct {
		name string
		// corrupt replaces the healthy state file. Returning false skips the
		// case on a platform where the corruption cannot be staged.
		corrupt func(t *testing.T, path string) bool
	}{
		{
			name: "truncated gob",
			corrupt: func(t *testing.T, path string) bool {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read state: %v", err)
				}
				if len(data) < 8 {
					t.Fatalf("state file is only %d bytes; nothing to truncate", len(data))
				}
				writeState(t, path, data[:len(data)/2])
				return true
			},
		},
		{
			name: "zero bytes",
			corrupt: func(t *testing.T, path string) bool {
				writeState(t, path, nil)
				return true
			},
		},
		{
			name: "garbage bytes",
			corrupt: func(t *testing.T, path string) bool {
				writeState(t, path, []byte("this is not a gob stream at all"))
				return true
			},
		},
		{
			name: "wrong version",
			corrupt: func(t *testing.T, path string) bool {
				writeState(t, path, encodeState(t, state{
					Version: formatVersion + 1,
					Root:    filepath.Dir(filepath.Dir(filepath.Dir(path))),
				}))
				return true
			},
		},
		{
			name: "wrong root",
			corrupt: func(t *testing.T, path string) bool {
				// A vault that was copied or moved: same relative paths, a root
				// this state file never described.
				writeState(t, path, encodeState(t, state{
					Version: formatVersion,
					Root:    filepath.Join(t.TempDir(), "somewhere-else"),
				}))
				return true
			},
		},
		{
			name: "entry contradicting its own hash",
			corrupt: func(t *testing.T, path string) bool {
				root := filepath.Dir(filepath.Dir(filepath.Dir(path)))
				writeState(t, path, encodeState(t, state{
					Version: formatVersion,
					Root:    root,
					Entries: []entry{{
						Rel:  "Alpha.md",
						Size: 5,
						// Bit rot: bytes that do not hash to the recorded value.
						Hash: "0000000000000000000000000000000000000000000000000000000000000000",
						Data: []byte("alpha"),
					}},
				}))
				return true
			},
		},
		{
			name: "entry escaping the vault root",
			corrupt: func(t *testing.T, path string) bool {
				root := filepath.Dir(filepath.Dir(filepath.Dir(path)))
				data := []byte("alpha")
				writeState(t, path, encodeState(t, state{
					Version: formatVersion,
					Root:    root,
					Entries: []entry{{
						Rel:  "../outside.md",
						Size: int64(len(data)),
						Hash: vault.HashBytes(data),
						Data: data,
					}},
				}))
				return true
			},
		},
		{
			name: "mode 0000",
			corrupt: func(t *testing.T, path string) bool {
				if runtime.GOOS == "windows" {
					t.Skip("file modes do not deny reads on windows")
				}
				if os.Geteuid() == 0 {
					t.Skip("root bypasses file permissions")
				}
				if err := os.Chmod(path, 0o000); err != nil {
					t.Fatalf("chmod state: %v", err)
				}
				return true
			},
		},
		{
			name: "symlink",
			corrupt: func(t *testing.T, path string) bool {
				target := filepath.Join(filepath.Dir(path), "elsewhere.gob")
				if err := os.WriteFile(target, []byte("not the index"), 0o644); err != nil {
					t.Fatalf("write symlink target: %v", err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatalf("remove state: %v", err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				return true
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := testVault(t, corruptionFixture)
			backdate(t, v.Root, time.Minute)

			// Warm the index so there is a healthy state file to damage.
			warm, _ := newTestIndex(t, v)
			mustLoad(t, warm)
			statePath := filepath.Join(Dir(v.Root), stateFileName)
			if _, err := os.Stat(statePath); err != nil {
				t.Fatalf("precondition: no state file to corrupt: %v", err)
			}

			if !tc.corrupt(t, statePath) {
				return
			}

			ix, logs := newTestIndex(t, v)
			result, err := ix.Load()
			if err != nil {
				t.Fatalf("a corrupt state file must never become a caller error: %v", err)
			}
			if got := relPaths(t, v.Root, result.Notes); !equalStrings(got, corruptionWant) {
				t.Errorf("notes = %v, want %v", got, corruptionWant)
			}
			if !result.Changed {
				t.Error("a rebuilt index is cold and must report Changed")
			}
			if len(*logs) != 1 {
				t.Errorf("want exactly one logged failure class, got %v", *logs)
			}

			// The rebuild must also repair the file, so the damage costs one
			// cold start rather than every start from here on. The mode-0000
			// case is the exception the design accepts: the write fails and is
			// ignored, which is the read-only-vault rule.
			if tc.name == "mode 0000" {
				return
			}
			next, nextLogs := newTestIndex(t, v)
			healed := mustLoad(t, next)
			if healed.Changed {
				t.Error("the load after a rebuild must start warm from the repaired state file")
			}
			if got := relPaths(t, v.Root, healed.Notes); !equalStrings(got, corruptionWant) {
				t.Errorf("healed notes = %v, want %v", got, corruptionWant)
			}
			if len(*nextLogs) != 0 {
				t.Errorf("the repaired state file still logged %v", *nextLogs)
			}
		})
	}
}

// TestRepeatedCorruptionLogsOnce pins the once-per-failure-class-per-process
// rule. Without it a vault whose .bnotes is unwritable would log on every
// single Load, which is how a degradation notice turns into noise nobody reads.
func TestRepeatedCorruptionLogsOnce(t *testing.T) {
	v := testVault(t, corruptionFixture)
	backdate(t, v.Root, time.Minute)

	warm, _ := newTestIndex(t, v)
	mustLoad(t, warm)
	statePath := filepath.Join(Dir(v.Root), stateFileName)
	writeState(t, statePath, []byte("garbage"))

	ix, logs := newTestIndex(t, v)
	for i := 0; i < 3; i++ {
		ix.Reset()
		writeState(t, statePath, []byte("garbage"))
		if _, err := ix.Load(); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}
	if len(*logs) != 1 {
		t.Errorf("want one log line across repeated failures, got %v", *logs)
	}
}

// TestMissingStateIsNotAFailure separates the ordinary cold start from a
// corrupt file: a vault that has never been indexed must rebuild without
// telling anyone anything is wrong.
func TestMissingStateIsNotAFailure(t *testing.T) {
	v := testVault(t, corruptionFixture)
	ix, logs := newTestIndex(t, v)

	result := mustLoad(t, ix)
	if got := relPaths(t, v.Root, result.Notes); !equalStrings(got, corruptionWant) {
		t.Errorf("notes = %v, want %v", got, corruptionWant)
	}
	if len(*logs) != 0 {
		t.Errorf("a first run logged %v", *logs)
	}
}

// TestStateDirectoryReplacedByAFileIsSurvivable covers the other half of the
// non-regular-file case: the index directory itself is not a directory.
func TestStateDirectoryReplacedByAFileIsSurvivable(t *testing.T) {
	v := testVault(t, corruptionFixture)
	if err := os.MkdirAll(filepath.Join(v.Root, ".bnotes"), 0o755); err != nil {
		t.Fatalf("mkdir .bnotes: %v", err)
	}
	if err := os.WriteFile(Dir(v.Root), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}

	ix, _ := newTestIndex(t, v)
	result, err := ix.Load()
	if err != nil {
		t.Fatalf("an unusable state directory must not become a caller error: %v", err)
	}
	if got := relPaths(t, v.Root, result.Notes); !equalStrings(got, corruptionWant) {
		t.Errorf("notes = %v, want %v", got, corruptionWant)
	}
}

func writeState(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write state %s: %v", path, err)
	}
}

func encodeState(t *testing.T, s state) []byte {
	t.Helper()

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(s); err != nil {
		t.Fatalf("encode state: %v", err)
	}
	return buf.Bytes()
}
