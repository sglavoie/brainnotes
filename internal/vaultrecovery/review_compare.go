package vaultrecovery

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sglavoie/sglavoie_brainnotes/internal/vaultsync"
)

func compareSequences(local, peer *Inventory) []string {
	var regressions []string
	if seen := peer.Applied[local.DeviceID]; seen > local.ExportSequence {
		regressions = append(regressions, fmt.Sprintf("local origin %s restored to sequence %d after peer observed %d", local.DeviceID, local.ExportSequence, seen))
	}
	if seen := local.Applied[peer.DeviceID]; seen > peer.ExportSequence {
		regressions = append(regressions, fmt.Sprintf("peer origin %s is at sequence %d after local observed %d", peer.DeviceID, peer.ExportSequence, seen))
	}
	return regressions
}

func compareFiles(local, peer []File) []Difference {
	left, right := filesByPath(local), filesByPath(peer)
	paths := unionPaths(left, right)
	var differences []Difference
	for _, path := range paths {
		if difference, found := compareFile(path, left, right); found {
			differences = append(differences, difference)
		}
	}
	return differences
}

func filesByPath(files []File) map[string]File {
	indexed := make(map[string]File, len(files))
	for _, file := range files {
		indexed[file.Path] = file
	}
	return indexed
}

func unionPaths(left, right map[string]File) []string {
	paths := make([]string, 0, len(left)+len(right))
	seen := map[string]struct{}{}
	for path := range left {
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	for path := range right {
		if _, ok := seen[path]; !ok {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func compareFile(path string, left, right map[string]File) (Difference, bool) {
	local, localOK := left[path]
	peer, peerOK := right[path]
	switch {
	case !localOK:
		return Difference{Path: path, Local: "missing", Peer: peer.SHA256}, true
	case !peerOK:
		return Difference{Path: path, Local: local.SHA256, Peer: "missing"}, true
	case local.SHA256 != peer.SHA256 || local.Size != peer.Size:
		return Difference{Path: path, Local: local.SHA256, Peer: peer.SHA256}, true
	default:
		return Difference{}, false
	}
}

func portableFiles(root string) ([]File, error) {
	var files []File
	err := filepath.WalkDir(root, inventoryWalker(root, &files))
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

func inventoryWalker(root string, files *[]File) fs.WalkDirFunc {
	return func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return inventoryEntry(path, filepath.ToSlash(rel), entry, files)
	}
}

func inventoryEntry(path, rel string, entry fs.DirEntry, files *[]File) error {
	class := vaultsync.Classify(rel)
	if entry.IsDir() {
		if class != vaultsync.ClassPortable {
			return filepath.SkipDir
		}
		return nil
	}
	if class != vaultsync.ClassPortable {
		return nil
	}
	info, err := entry.Info()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("vault recovery: portable path %q is not a regular file", rel)
	}
	digest, err := hashFile(path)
	if err != nil {
		return err
	}
	*files = append(*files, File{Path: rel, Size: info.Size(), SHA256: digest})
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validSHA256(value string) bool {
	return len(value) == 64 && strings.Trim(value, "0123456789abcdef") == ""
}
