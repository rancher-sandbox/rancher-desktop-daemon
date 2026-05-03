// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package kuberlr

import (
	"context"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/blang/semver/v4"
	"github.com/sirupsen/logrus"

	"github.com/rancher-sandbox/rancher-desktop-daemon/pkg/version"
)

// defaultKubeMirror is the upstream Kubernetes release mirror. SIG Release
// publishes dl.k8s.io as the canonical CDN endpoint.
const defaultKubeMirror = "https://dl.k8s.io"

// envKubeMirror lets tests (and operators) point the resolver at an
// alternate mirror. Useful for offline mirrors and for the BATS cache
// lifecycle test, which serves a fake binary from a local HTTP server.
const envKubeMirror = "RDD_KUBECTL_MIRROR"

// downloadTimeout caps each mirror request. Five minutes turns a hung
// mirror into a bounded failure and still covers a ~50 MB kubectl
// binary on slow links (~170 kB/s).
const downloadTimeout = 5 * time.Minute

// httpClient enforces downloadTimeout on every kuberlr request.
// http.DefaultClient sets no Timeout, and the inherited cobra context
// carries no deadline. If the request context later carries a shorter
// deadline, that deadline wins.
var httpClient = &http.Client{Timeout: downloadTimeout}

// userAgent identifies rdd-kuberlr traffic to mirrors and proxies.
// Corporate proxies and air-gapped mirrors regularly rate-limit or
// denylist the default Go client UA (`Go-http-client/1.1`); naming
// the client also helps SIG Release correlate kuberlr-style traffic
// against dl.k8s.io.
var userAgent = "rdd-kuberlr/" + version.Version

// mirrorURL returns the mirror base URL the resolver downloads from.
//
// TODO(offline): pair this with a "may we download?" toggle so air-gapped
// users can pre-populate CacheDir() and forbid network fetches.
func mirrorURL() string {
	if v := os.Getenv(envKubeMirror); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultKubeMirror
}

// ensureCached returns the path to a cached kubectl matching want. If no
// matching binary exists on disk, ensureCached downloads one from the
// upstream mirror and verifies its sha512 before publishing it atomically.
func ensureCached(ctx context.Context, want semver.Version) (string, error) {
	path := cachedPath(want)
	if _, err := os.Stat(path); err == nil {
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	logrus.Infof("downloading kubectl v%d.%d.%d from %s", want.Major, want.Minor, want.Patch, mirrorURL())
	if err := download(ctx, want, path); err != nil {
		return "", err
	}
	return path, nil
}

// cachedPath returns the absolute path of the cached kubectl for v.
func cachedPath(v semver.Version) string {
	name := fmt.Sprintf("kubectl-v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(CacheDir(), name)
}

// download fetches kubectl v from the upstream mirror, verifies its sha512
// against the mirror's published checksum, and renames the verified file to
// dst. A failure leaves no partial file behind for the next call to mistake
// for a valid binary.
func download(ctx context.Context, v semver.Version, dst string) error {
	base := fmt.Sprintf("%s/release/v%d.%d.%d/bin/%s/%s/kubectl", mirrorURL(), v.Major, v.Minor, v.Patch, runtime.GOOS, runtime.GOARCH)
	if runtime.GOOS == "windows" {
		base += ".exe"
	}
	want, err := fetchSha512(ctx, base+".sha512")
	if err != nil {
		return fmt.Errorf("fetching checksum: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".kubectl-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	h := sha512.New()
	if err := streamGet(ctx, base, io.MultiWriter(tmp, h), maxKubectlBytes); err != nil {
		tmp.Close()
		return fmt.Errorf("downloading %s: %w", base, err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("checksum mismatch for %s: want %s, got %s", base, want, got)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return err
	}
	return os.Rename(tmpName, dst)
}

// Body-size caps for streamGet. maxSha512Bytes covers the longest
// `sha512sum`-format line (128 hex chars + two spaces + a generous
// filename); maxKubectlBytes covers the kubectl binary with headroom
// (current kubectl is ~50 MB). A misconfigured mirror serving the
// binary at the .sha512 URL hits the small cap first and fails with
// a clear error instead of streaming megabytes into a strings.Builder.
//
// If kubectl ever exceeds maxKubectlBytes, io.LimitReader truncates
// silently and the digest covers a partial body, so the caller sees a
// "checksum mismatch" error rather than a size-cap message. Bump the
// cap when kubectl outgrows it.
const (
	maxSha512Bytes  = 4 << 10   // 4 KiB
	maxKubectlBytes = 256 << 20 // 256 MiB
)

// fetchSha512 downloads the sha512 hex digest at url. dl.k8s.io serves
// the bare digest, but a mirror populated with `sha512sum` writes the
// digest, two spaces, and the filename — fields() takes the digest and
// drops the trailing filename so both formats verify. fetchSha512
// rejects non-128-hex tokens so a misconfigured mirror surfaces a
// "malformed checksum" error instead of the misleading "checksum
// mismatch" download's digest comparison would produce.
func fetchSha512(ctx context.Context, url string) (string, error) {
	var sb strings.Builder
	if err := streamGet(ctx, url, &sb, maxSha512Bytes); err != nil {
		return "", err
	}
	fields := strings.Fields(sb.String())
	if len(fields) == 0 {
		return "", fmt.Errorf("empty checksum response from %s", url)
	}
	// Normalize to lowercase to match download's hex.EncodeToString
	// output. PowerShell Get-FileHash emits uppercase hex; without
	// this, the comparison rejects valid digests as mismatched.
	digest := strings.ToLower(fields[0])
	if len(digest) != sha512.Size*2 {
		return "", fmt.Errorf("malformed checksum from %s: %d chars, want %d", url, len(digest), sha512.Size*2)
	}
	for _, c := range digest {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", fmt.Errorf("malformed checksum from %s: non-hex character %q", url, c)
		}
	}
	return digest, nil
}

// streamGet performs a GET on url and copies the response body into w,
// capped at maxBytes. The cap turns a malicious or misconfigured mirror
// into a bounded failure: the underlying io.LimitReader stops reading
// after maxBytes regardless of the server's intent. streamGet returns
// an error on any non-200 status.
func streamGet(ctx context.Context, url string, w io.Writer, maxBytes int64) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, maxBytes))
	return err
}
