// Package updater checks GitHub Releases for a newer PhantomOps and installs
// it in place.
//
// Safety model:
//   - Only assets of the release fetched by Check can be installed; the UI
//     names an asset, it never supplies a URL.
//   - Every download is verified against the release's SHA256SUMS file before
//     anything is written. A release without checksums cannot be installed.
//   - Development builds (version "dev") are never replaced.
//   - Privileged steps (system packages, root-owned binaries) go through
//     pkexec with plain argument lists, never a shell string.
package updater

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
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
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/minio/selfupdate"

	"PhantomOps/internal/version"
)

const (
	owner        = "Amanbig"
	repo         = "PhantomOps"
	assetPrefix  = "phantomops-"
	checksumFile = "SHA256SUMS"
	maxAssetSize = 400 << 20
	userAgent    = "PhantomOps-Updater"
)

// Overridable in tests.
var (
	apiBase      = "https://api.github.com"
	downloadBase = "https://github.com/" + owner + "/" + repo + "/releases/download/"
)

// Asset is one downloadable file of a release.
type Asset struct {
	Name      string `json:"name"`
	Type      string `json:"type"` // deb, rpm, tar.gz, zip, exe, dmg
	Size      int64  `json:"size"`
	IsDefault bool   `json:"isDefault"`
	url       string
}

// Info describes the latest release relative to this build.
type Info struct {
	Available      bool    `json:"available"`
	CurrentVersion string  `json:"currentVersion"`
	LatestVersion  string  `json:"latestVersion"`
	ReleaseName    string  `json:"releaseName"`
	ReleaseNotes   string  `json:"releaseNotes"`
	PublishedAt    string  `json:"publishedAt"`
	ReleaseURL     string  `json:"releaseUrl"`
	Platform       string  `json:"platform"`
	InstallType    string  `json:"installType"` // deb, rpm, standalone, nsis, portable, app
	NeedsElevation bool    `json:"needsElevation"`
	CanInstall     bool    `json:"canInstall"`
	Reason         string  `json:"reason"` // why CanInstall is false
	Assets         []Asset `json:"assets"`
	NoReleases     bool    `json:"noReleases"`
}

// Progress reports the state of an install.
type Progress struct {
	Status     string `json:"status"` // idle, downloading, verifying, installing, ready, error
	Percentage int    `json:"percentage"`
	ReadBytes  int64  `json:"readBytes"`
	TotalBytes int64  `json:"totalBytes"`
	Message    string `json:"message"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt string    `json:"published_at"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	Assets      []ghAsset `json:"assets"`
}

// Updater is safe for concurrent use.
type Updater struct {
	http *http.Client

	mu       sync.Mutex
	latest   *ghRelease
	progress Progress
	busy     bool
	target   string // executable that was replaced (for Restart)

	// OnProgress, if set, is called on every progress change.
	OnProgress func(Progress)

	// for tests
	targetOverride string
	installType    string
}

// New creates an Updater.
func New() *Updater {
	return &Updater{http: &http.Client{Timeout: 5 * time.Minute}, progress: Progress{Status: "idle"}}
}

func (u *Updater) setProgress(p Progress) {
	u.mu.Lock()
	u.progress = p
	cb := u.OnProgress
	u.mu.Unlock()
	if cb != nil {
		cb(p)
	}
}

// CurrentProgress returns the latest install progress.
func (u *Updater) CurrentProgress() Progress {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.progress
}

// --- install type & target -------------------------------------------------

func (u *Updater) executable() (string, error) {
	if u.targetOverride != "" {
		return u.targetOverride, nil
	}
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	return p, nil
}

// DetectInstallType reports how this copy of PhantomOps was installed.
func (u *Updater) DetectInstallType() string {
	if u.installType != "" {
		return u.installType
	}
	exe, _ := u.executable()
	switch runtime.GOOS {
	case "linux":
		if exe == "/usr/bin/phantomops" {
			if _, err := exec.LookPath("dpkg"); err == nil && exec.Command("dpkg", "-s", "phantomops").Run() == nil {
				return "deb"
			}
			if _, err := exec.LookPath("rpm"); err == nil && exec.Command("rpm", "-q", "phantomops").Run() == nil {
				return "rpm"
			}
		}
		return "standalone"
	case "windows":
		if strings.Contains(strings.ToLower(exe), `\program files`) {
			return "nsis"
		}
		return "portable"
	case "darwin":
		if strings.Contains(exe, ".app/") {
			return "app"
		}
		return "portable"
	}
	return "standalone"
}

func writable(path string) bool {
	f, err := os.CreateTemp(filepath.Dir(path), ".phantomops-perm-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

// --- version comparison ------------------------------------------------------

// IsNewer reports whether remote is a higher semantic version than local.
func IsNewer(remote, local string) bool {
	r, l := parseSemver(remote), parseSemver(local)
	for i := 0; i < 3; i++ {
		if r[i] != l[i] {
			return r[i] > l[i]
		}
	}
	return false
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(v), "v"), "V")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	for i, p := range strings.SplitN(v, ".", 3) {
		n, _ := strconv.Atoi(p)
		out[i] = n
	}
	return out
}

// --- assets ----------------------------------------------------------------

func assetType(name string) string {
	l := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".deb", ".rpm", ".zip", ".exe", ".dmg"} {
		if strings.HasSuffix(l, ext) {
			return strings.TrimPrefix(ext, ".")
		}
	}
	return ""
}

// preferred lists asset name suffixes in order of preference.
func preferred(goos, installType string) []string {
	switch goos {
	case "linux":
		switch installType {
		case "deb":
			return []string{"linux-amd64.deb", "linux-amd64.tar.gz"}
		case "rpm":
			return []string{"linux-amd64.rpm", "linux-amd64.tar.gz"}
		}
		return []string{"linux-amd64.tar.gz"}
	case "windows":
		if installType == "nsis" {
			return []string{"windows-amd64-installer.exe", "windows-amd64.zip"}
		}
		return []string{"windows-amd64.zip"}
	case "darwin":
		return []string{"darwin-universal.zip", "darwin-universal.dmg"}
	}
	return nil
}

func osAssets(assets []ghAsset, goos, installType string) []Asset {
	var out []Asset
	for _, a := range assets {
		l := strings.ToLower(a.Name)
		if !strings.HasPrefix(l, assetPrefix+goos+"-") || assetType(a.Name) == "" {
			continue
		}
		out = append(out, Asset{Name: a.Name, Type: assetType(a.Name), Size: a.Size, url: a.URL})
	}
	for _, suffix := range preferred(goos, installType) {
		for i := range out {
			if strings.HasSuffix(strings.ToLower(out[i].Name), suffix) {
				out[i].IsDefault = true
				return out
			}
		}
	}
	return out
}

// --- check -------------------------------------------------------------------

// Check fetches the latest release from GitHub.
func (u *Updater) Check(ctx context.Context) (Info, error) {
	cur := version.Version
	it := u.DetectInstallType()
	info := Info{CurrentVersion: cur, LatestVersion: cur, Platform: runtime.GOOS + "/" + runtime.GOARCH, InstallType: it}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/repos/%s/%s/releases/latest", apiBase, owner, repo), nil)
	if err != nil {
		return info, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	c := &http.Client{Timeout: 15 * time.Second}
	resp, err := c.Do(req)
	if err != nil {
		return info, fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		info.NoReleases = true
		info.Reason = "No release has been published yet."
		return info, nil
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return info, errors.New("GitHub rate limit reached; try again later")
	case resp.StatusCode != http.StatusOK:
		return info, fmt.Errorf("GitHub returned %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return info, fmt.Errorf("unreadable release data: %w", err)
	}
	if rel.Draft || rel.Prerelease || rel.TagName == "" {
		info.NoReleases = true
		info.Reason = "No stable release has been published yet."
		return info, nil
	}

	u.mu.Lock()
	u.latest = &rel
	u.mu.Unlock()

	info.LatestVersion, info.ReleaseName, info.ReleaseNotes = rel.TagName, rel.Name, rel.Body
	info.PublishedAt, info.ReleaseURL = rel.PublishedAt, rel.HTMLURL
	info.Assets = osAssets(rel.Assets, runtime.GOOS, it)
	info.Available = version.IsRelease() && IsNewer(rel.TagName, cur)

	exe, _ := u.executable()
	info.NeedsElevation = it == "deb" || it == "rpm" || it == "nsis" || (exe != "" && !writable(exe))

	switch {
	case !version.IsRelease():
		info.Reason = "This is a development build. Install a release to use automatic updates."
	case !info.Available:
		info.Reason = "You are on the latest version."
	case len(info.Assets) == 0:
		info.Reason = "This release has no download for " + info.Platform + "."
	case !hasAsset(rel.Assets, checksumFile):
		info.Reason = "This release has no " + checksumFile + " file, so downloads cannot be verified."
	default:
		info.CanInstall = true
	}
	return info, nil
}

func hasAsset(as []ghAsset, name string) bool {
	for _, a := range as {
		if a.Name == name {
			return true
		}
	}
	return false
}

// --- install -------------------------------------------------------------------

// Install downloads, verifies and installs the named asset of the release
// returned by the last Check.
func (u *Updater) Install(ctx context.Context, assetName string) (err error) {
	if !version.IsRelease() {
		return errors.New("development builds cannot be updated in place; install a release")
	}
	u.mu.Lock()
	if u.busy {
		u.mu.Unlock()
		return errors.New("an update is already in progress")
	}
	rel := u.latest
	u.busy = true
	u.mu.Unlock()
	defer func() {
		u.mu.Lock()
		u.busy = false
		u.mu.Unlock()
		if err != nil {
			u.setProgress(Progress{Status: "error", Message: err.Error()})
		}
	}()
	if rel == nil {
		return errors.New("check for updates first")
	}
	if !IsNewer(rel.TagName, version.Version) {
		return errors.New("the latest release is not newer than this version")
	}

	var asset, sums *ghAsset
	for i := range rel.Assets {
		switch rel.Assets[i].Name {
		case assetName:
			asset = &rel.Assets[i]
		case checksumFile:
			sums = &rel.Assets[i]
		}
	}
	if asset == nil || !strings.HasPrefix(strings.ToLower(asset.Name), assetPrefix+runtime.GOOS+"-") || assetType(asset.Name) == "" {
		return fmt.Errorf("%q is not a %s download of %s", assetName, runtime.GOOS, rel.TagName)
	}
	if sums == nil {
		return errors.New("the release has no " + checksumFile + "; refusing to install an unverifiable download")
	}
	for _, a := range []*ghAsset{asset, sums} {
		if !strings.HasPrefix(a.URL, downloadBase+rel.TagName+"/") {
			return fmt.Errorf("unexpected download location for %s", a.Name)
		}
	}

	u.setProgress(Progress{Status: "downloading", Message: "Fetching checksums…"})
	sumData, err := u.download(ctx, sums.URL, 1<<20, nil)
	if err != nil {
		return fmt.Errorf("download %s: %w", checksumFile, err)
	}
	want, err := lookupChecksum(sumData, asset.Name)
	if err != nil {
		return err
	}

	data, err := u.download(ctx, asset.URL, maxAssetSize, func(read, total int64) {
		pct := 0
		if total > 0 {
			pct = int(read * 100 / total)
		}
		u.setProgress(Progress{Status: "downloading", Percentage: pct, ReadBytes: read, TotalBytes: total, Message: "Downloading " + asset.Name})
	})
	if err != nil {
		return fmt.Errorf("download %s: %w", asset.Name, err)
	}

	u.setProgress(Progress{Status: "verifying", Percentage: 100, Message: "Verifying checksum…"})
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("checksum mismatch for %s: the download is corrupted or was tampered with (expected %s…, got %s…)", asset.Name, want[:12], got[:12])
	}

	u.setProgress(Progress{Status: "installing", Percentage: 100, Message: "Installing…"})
	exe, err := u.executable()
	if err != nil {
		return err
	}
	if err := u.apply(asset.Name, data, exe); err != nil {
		return err
	}
	u.mu.Lock()
	u.target = exe
	u.mu.Unlock()
	msg := "Update installed. Restart PhantomOps to use " + rel.TagName + "."
	if assetType(asset.Name) == "exe" || assetType(asset.Name) == "dmg" {
		msg = "The installer has been opened. Follow its steps, then restart PhantomOps."
	}
	u.setProgress(Progress{Status: "ready", Percentage: 100, Message: msg})
	return nil
}

func (u *Updater) download(ctx context.Context, url string, limit int64, onProgress func(read, total int64)) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := u.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("server returned %s", resp.Status)
	}
	if resp.ContentLength > limit {
		return nil, fmt.Errorf("file is too large (%d MB)", resp.ContentLength>>20)
	}
	var buf bytes.Buffer
	chunk := make([]byte, 64<<10)
	var read int64
	for {
		n, rerr := resp.Body.Read(chunk)
		if n > 0 {
			read += int64(n)
			if read > limit {
				return nil, errors.New("file is larger than allowed")
			}
			buf.Write(chunk[:n])
			if onProgress != nil {
				onProgress(read, resp.ContentLength)
			}
		}
		if rerr == io.EOF {
			return buf.Bytes(), nil
		}
		if rerr != nil {
			return nil, rerr
		}
	}
}

// lookupChecksum finds name in a sha256sum-format file ("<hex>  <name>").
func lookupChecksum(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0]), nil
			}
		}
	}
	return "", fmt.Errorf("%s has no entry for %s; refusing to install an unverifiable download", checksumFile, name)
}

// --- apply ---------------------------------------------------------------------

func (u *Updater) apply(name string, data []byte, exe string) error {
	switch assetType(name) {
	case "deb", "rpm":
		pkg, err := writeTemp(name, data, 0o644)
		if err != nil {
			return err
		}
		if assetType(name) == "deb" {
			return pkexec("dpkg", "-i", pkg)
		}
		return pkexec("rpm", "-Uvh", "--replacepkgs", pkg)
	case "exe":
		inst, err := writeTemp(name, data, 0o755)
		if err != nil {
			return err
		}
		return exec.Command(inst).Start()
	case "dmg":
		img, err := writeTemp(name, data, 0o644)
		if err != nil {
			return err
		}
		return exec.Command("open", img).Start()
	}

	bin, err := extractBinary(data, name)
	if err != nil {
		return err
	}
	if writable(exe) {
		if err := selfupdate.Apply(bytes.NewReader(bin), selfupdate.Options{TargetPath: exe}); err != nil {
			if rerr := selfupdate.RollbackError(err); rerr != nil {
				return fmt.Errorf("update failed and the previous version could not be restored: %v (original error: %w)", rerr, err)
			}
			return fmt.Errorf("update failed (the previous version is still in place): %w", err)
		}
		return nil
	}
	tmp, err := writeTemp("phantomops-new", bin, 0o755)
	if err != nil {
		return err
	}
	return pkexec("install", "-m", "0755", tmp, exe)
}

func writeTemp(name string, data []byte, mode os.FileMode) (string, error) {
	dir, err := os.MkdirTemp("", "phantomops-update-")
	if err != nil {
		return "", err
	}
	p := filepath.Join(dir, filepath.Base(name))
	if err := os.WriteFile(p, data, mode); err != nil {
		return "", err
	}
	return p, nil
}

// pkexec runs a privileged command with an explicit argument list.
func pkexec(args ...string) error {
	if _, err := exec.LookPath("pkexec"); err != nil {
		return fmt.Errorf("administrator rights are needed and pkexec is not available; run manually: sudo %s", strings.Join(args, " "))
	}
	out, err := exec.Command("pkexec", args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "Not authorized") || strings.Contains(msg, "dismissed") || strings.Contains(err.Error(), "126") {
			return errors.New("authorization was cancelled")
		}
		return fmt.Errorf("%s failed: %s (run manually: sudo %s)", args[0], msg, strings.Join(args, " "))
	}
	return nil
}

var binaryNames = map[string]bool{"PhantomOps": true, "PhantomOps.exe": true, "phantomops": true}

// extractBinary returns the PhantomOps executable from a .tar.gz or .zip.
func extractBinary(data []byte, name string) ([]byte, error) {
	switch assetType(name) {
	case "tar.gz":
		gz, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Typeflag == tar.TypeReg && binaryNames[filepath.Base(h.Name)] {
				return io.ReadAll(io.LimitReader(tr, maxAssetSize))
			}
		}
	case "zip":
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			base := filepath.Base(f.Name)
			// macOS bundle: PhantomOps.app/Contents/MacOS/PhantomOps
			if binaryNames[base] && !f.FileInfo().IsDir() && (runtime.GOOS != "darwin" || strings.Contains(f.Name, "Contents/MacOS/")) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxAssetSize))
			}
		}
	default:
		return nil, fmt.Errorf("%s is not an archive", name)
	}
	return nil, fmt.Errorf("the PhantomOps executable was not found inside %s", name)
}

// --- restart -------------------------------------------------------------------

// Restart starts the updated executable and exits this process.
func (u *Updater) Restart() error {
	u.mu.Lock()
	exe := u.target
	u.mu.Unlock()
	if exe == "" {
		var err error
		if exe, err = u.executable(); err != nil {
			return err
		}
	}
	if _, err := os.Stat(exe); err != nil {
		return fmt.Errorf("cannot find the updated program at %s: %w", exe, err)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start the updated version: %w", err)
	}
	os.Exit(0)
	return nil
}
