package syncthingbin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// downloadTimeout bounds the fetch. It is generous because the archive is around
// twenty megabytes and a developer on a slow link is not a failure, and it is
// bounded at all because a hung provisioning step would otherwise hold the gate
// open until somebody noticed.
const downloadTimeout = 5 * time.Minute

// Resolve returns the path of the pinned Syncthing binary, fetching it once if
// this machine has never had it.
//
// The order is: an operator-supplied binary, then the cache, then the network.
// Every branch runs the candidate and requires it to report Version, so no branch
// returns something unexamined — but the guarantees are not equal, deliberately.
// A binary this package installed is also weighed against the digest it recorded
// at install time, which is the archive checksum carried forward; a binary named
// by EnvBinary has no such record and cannot get one, because a differently-built
// release of the right version is exactly what that variable is for.
func Resolve() (string, error) {
	if supplied := strings.TrimSpace(os.Getenv(EnvBinary)); supplied != "" {
		if err := checkVersion(supplied); err != nil {
			return "", fmt.Errorf("syncthingbin: %s=%q: %w", EnvBinary, supplied, err)
		}
		return supplied, nil
	}
	root, err := CacheRoot()
	if err != nil {
		return "", err
	}
	installed := filepath.Join(root, Version, "syncthing")
	if err := verifyCached(installed); err == nil {
		return installed, nil
	}
	if err := install(installed); err != nil {
		return "", err
	}
	if err := verifyCached(installed); err != nil {
		return "", fmt.Errorf("syncthingbin: freshly installed %q: %w", installed, err)
	}
	return installed, nil
}

// userCache is os.UserCacheDir() as it was when this process started, and the
// reason CacheRoot does not simply ask.
//
// os.UserCacheDir reads HOME (and XDG_CACHE_HOME on linux), and
// internal/vaultfixture's Isolate rewrites HOME with t.Setenv before any scenario
// runs. A cache located after that rewrite would sit inside the fixture, be
// deleted with it, and make every test in a tagged run download the release
// again — the exact cost this cache exists to remove. Reading it at
// initialization means the answer is this machine's, whoever asks and whenever.
var userCache, userCacheErr = os.UserCacheDir()

// CacheRoot is where a downloaded binary is kept between runs.
//
// It is never inside a vault and never inside a temporary fixture directory: the
// download is shared across runs precisely so that the tests do not have to care
// about the network, and a cache that the isolation teardown deleted would defeat
// that on every single run.
func CacheRoot() (string, error) {
	if override := strings.TrimSpace(os.Getenv(EnvCache)); override != "" {
		return override, nil
	}
	if userCacheErr != nil {
		return "", fmt.Errorf("syncthingbin: locate cache directory (set %s): %w", EnvCache, userCacheErr)
	}
	return filepath.Join(userCache, "brainnotes", "syncthing"), nil
}

// digestPath names the record of what was verified, beside the binary it
// describes.
func digestPath(binary string) string { return binary + ".sha256" }

// verifyCached re-establishes the download's guarantee on a binary this machine
// already has.
//
// The recorded checksums are of release *archives*, so a cached binary cannot be
// compared against them directly; what install records instead is the digest of
// the bytes it extracted from an archive that did match. Re-reading it here is
// what makes a cache hit as trustworthy as the download was, rather than trusting
// whatever now sits at that path and answers --version with the right string. A
// missing or mismatched record is not an error to report — it sends Resolve down
// the install path, which is the same answer as never having had the binary.
func verifyCached(path string) error {
	recorded, err := os.ReadFile(digestPath(path))
	if err != nil {
		return err
	}
	want := strings.TrimSpace(string(recorded))
	got, err := fileDigest(path)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%q hashes to %s, want the %s recorded beside it", path, got, want)
	}
	return checkVersion(path)
}

// fileDigest hashes a file in a stream, since the cached binary is tens of
// megabytes and this runs on every resolution.
func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	sum := sha256.New()
	if _, err := io.Copy(sum, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// checkVersion runs the candidate and requires that it introduce itself as the
// pinned release.
//
// Running it is the point. A file of the right name and the right size is not
// evidence of anything, and a half-extracted or architecture-mismatched binary
// fails here with the operating system's own message rather than three seconds
// later inside a test that was trying to describe a transport.
func checkVersion(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		return fmt.Errorf("%q is not an executable file", path)
	}
	out, err := exec.Command(path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("run %q --version: %w: %s", path, err, strings.TrimSpace(string(out)))
	}
	want := "syncthing " + Version + " "
	if !strings.HasPrefix(string(out), want) {
		return fmt.Errorf("reports %q, want %s", strings.TrimSpace(string(out)), Version)
	}
	return nil
}

// install downloads, verifies and unpacks the pinned release into dst.
//
//bn:direct-write the destination is the cache directory this package owns, and the binary is written under a temporary name and renamed only after its archive matched the recorded checksum
func install(dst string) error {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	rel, known := releases[platform]
	if !known {
		return fmt.Errorf("syncthingbin: no verified %s archive for %s; set %s to a %s binary", Version, platform, EnvBinary, Version)
	}
	archive, err := fetch(downloadBase + Version + "/" + rel.asset)
	if err != nil {
		return err
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != rel.sha256 {
		return fmt.Errorf("syncthingbin: %s hashes to %s, want %s", rel.asset, hex.EncodeToString(sum[:]), rel.sha256)
	}
	binary, err := unpack(rel.asset, archive)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("syncthingbin: create cache directory: %w", err)
	}
	if err := place(dst, binary); err != nil {
		return err
	}
	return record(dst, binary)
}

// record writes down the digest of the bytes that were just verified, which is
// what a later run compares its cache against. It is written after the binary so
// that an interrupted install leaves a binary with no record — unverifiable, and
// therefore reinstalled — rather than a record for bytes that are not there.
//
//bn:direct-write see install; this is the provenance half of that write, into the same cache directory this package owns
func record(dst string, binary []byte) error {
	sum := sha256.Sum256(binary)
	digest := hex.EncodeToString(sum[:]) + "\n"
	if err := os.WriteFile(digestPath(dst), []byte(digest), 0o644); err != nil {
		return fmt.Errorf("syncthingbin: record the verified digest of %q: %w", dst, err)
	}
	return nil
}

// place writes the bytes beside dst and renames them onto it, so that a run
// interrupted mid-write leaves no file that the next run would try to execute.
//
//bn:direct-write see install; this is the rename half of that write
func place(dst string, binary []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(dst), ".syncthing-*")
	if err != nil {
		return fmt.Errorf("syncthingbin: stage binary: %w", err)
	}
	staged := temp.Name()
	defer func() { _ = os.Remove(staged) }()
	if _, err := temp.Write(binary); err != nil {
		_ = temp.Close()
		return fmt.Errorf("syncthingbin: write %q: %w", staged, err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("syncthingbin: close %q: %w", staged, err)
	}
	if err := os.Chmod(staged, 0o755); err != nil {
		return fmt.Errorf("syncthingbin: make %q executable: %w", staged, err)
	}
	if err := os.Rename(staged, dst); err != nil {
		return fmt.Errorf("syncthingbin: install %q: %w", dst, err)
	}
	return nil
}

// fetch reads a release archive into memory. The archives are tens of megabytes
// and are hashed as a whole before anything is unpacked, so holding one is
// simpler than streaming it and rereading it.
func fetch(url string) ([]byte, error) {
	client := &http.Client{Timeout: downloadTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("syncthingbin: download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("syncthingbin: download %s: %s", url, resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("syncthingbin: read %s: %w", url, err)
	}
	return body, nil
}

// errNoBinary says an archive did not contain the file the whole download was
// for, which means the release layout changed and the pin needs a human.
var errNoBinary = errors.New("archive contains no syncthing binary at its top level")
