// Package selfupdate implements `multibird upgrade`: fetch the latest GitHub
// release, verify the sha256 from checksums.txt, and atomically replace the
// running binary. Thin by design — no delta updates, no channels, no
// background checks; goreleaser's release layout is the contract.
package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Repo is the GitHub repository releases are fetched from.
const Repo = "magnetrong/multibird"

// Release is the subset of the GitHub release API we use.
type Release struct {
	Tag    string            // e.g. "v0.3.0"
	Assets map[string]string // asset name -> browser download URL
}

// Version returns the release version without the leading "v".
func (r *Release) Version() string { return strings.TrimPrefix(r.Tag, "v") }

var httpClient = &http.Client{Timeout: 60 * time.Second}

// LatestRelease queries the GitHub API for the newest release.
func LatestRelease(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/repos/"+Repo+"/releases/latest", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("checking github.com/%s for releases: %w — check your network", Repo, err)
	}
	defer resp.Body.Close() //nolint:errcheck // read-path close
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("github.com/%s has no releases yet", Repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned %s for the latest release", resp.Status)
	}
	var body struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decoding GitHub release response: %w", err)
	}
	rel := &Release{Tag: body.TagName, Assets: map[string]string{}}
	for _, a := range body.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// AssetName is goreleaser's archive name_template — the release-layout
// contract this package depends on (.goreleaser.yml must match).
func AssetName(version, goos, goarch string) string {
	return fmt.Sprintf("multibird_%s_%s_%s.tar.gz", version, goos, goarch)
}

// ParseChecksums reads a goreleaser checksums.txt ("<sha256>  <name>" lines)
// into name -> hex digest.
func ParseChecksums(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && len(fields[0]) == 64 {
			out[fields[1]] = strings.ToLower(fields[0])
		}
	}
	return out
}

// Apply downloads the asset for goos/goarch, verifies it against
// checksums.txt, extracts the multibird binary, and atomically replaces
// binPath (same-directory rename). The caller decides which binary to
// replace (normally os.Executable()).
func Apply(ctx context.Context, rel *Release, goos, goarch, binPath string) error {
	name := AssetName(rel.Version(), goos, goarch)
	assetURL, ok := rel.Assets[name]
	if !ok {
		return fmt.Errorf("release %s has no asset %s — platform not published?", rel.Tag, name)
	}
	sumURL, ok := rel.Assets["checksums.txt"]
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt — refusing to install unverifiable binary", rel.Tag)
	}

	sums, err := fetch(ctx, sumURL, 1<<20)
	if err != nil {
		return fmt.Errorf("downloading checksums.txt: %w", err)
	}
	want, ok := ParseChecksums(string(sums))[name]
	if !ok {
		return fmt.Errorf("checksums.txt in %s has no entry for %s", rel.Tag, name)
	}

	archive, err := fetch(ctx, assetURL, 256<<20)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", name, err)
	}
	got := sha256.Sum256(archive)
	if hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("checksum mismatch for %s — corrupted download or tampered release, not installing", name)
	}

	bin, err := extractBinary(archive, "multibird")
	if err != nil {
		return fmt.Errorf("extracting %s: %w", name, err)
	}

	// Atomic same-directory replace; the running executable keeps working
	// from its unlinked inode.
	tmp := binPath + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil { //nolint:gosec // G306: it's an executable
		return fmt.Errorf("writing %s: %w — if multibird lives in a root-owned dir, re-run with sudo", tmp, err)
	}
	if err := os.Rename(tmp, binPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replacing %s: %w — if multibird lives in a root-owned dir, re-run with sudo", binPath, err)
	}
	return nil
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-path close
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// extractBinary pulls one file out of a .tar.gz archive.
func extractBinary(archive []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, err
	}
	defer gz.Close() //nolint:errcheck // read-path close
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("archive contains no %q binary", name)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 256<<20))
		}
	}
}
