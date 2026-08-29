package vault

// SyncDir flushes a directory's entries so a rename or a creation that changed
// them survives power loss. POSIX guarantees rename atomicity within a running
// system but not across a crash until the directory itself is synced, which is
// what makes it the last step of every durable write in the vault.
//
// It is exported so packages that keep their own operational state outside the
// vault tree — internal/vaultgit writes below `.git` — reach the same primitive
// instead of duplicating its per-platform build tags. On Windows it is a no-op,
// for the reason stated in dirsync_windows.go.
func SyncDir(dir string) error { return syncDir(dir) }
