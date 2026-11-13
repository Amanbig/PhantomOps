package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"PhantomOps/internal/version"
)

// --- pure helpers ------------------------------------------------------------

func TestIsNewer(t *testing.T) {
	cases := []struct {
		remote, local string
		want          bool
	}{
		{"v0.3.0", "v0.2.2", true},
		{"v0.2.3", "v0.2.2", true},
		{"v1.0.0", "v0.9.9", true},
		{"v0.10.0", "v0.9.0", true}, // numeric, not lexical
		{"v0.2.2", "v0.2.2", false},
		{"v0.2.1", "v0.2.2", false},
		{"0.3.0", "v0.2.2", true}, // tag with or without the v
		{"V0.3.0", "0.2.2", true},
		{"v0.3.0-rc1", "v0.2.9", true}, // pre-release suffix ignored
		{"v0.3.0+build5", "v0.3.0", false},
		{"garbage", "v0.2.2", false},
		{"v0.3.0", "dev", true}, // "dev" parses as 0.0.0
	}
	for _, c := range cases {
		if got := IsNewer(c.remote, c.local); got != c.want {
			t.Errorf("IsNewer(%q,%q) = %v, want %v", c.remote, c.local, got, c.want)
		}
	}
}

func TestAssetTypeAndPreferred(t *testing.T) {
	types := map[string]string{
		"phantomops-linux-amd64.tar.gz": "tar.gz", "phantomops-linux-amd64.DEB": "deb", "x.rpm": "rpm",
		"phantomops-windows-amd64.zip": "zip", "phantomops-windows-amd64-installer.exe": "exe",
		"phantomops-darwin-universal.dmg": "dmg", "SHA256SUMS": "", "notes.txt": "",
	}
	for name, want := range types {
		if got := assetType(name); got != want {
			t.Errorf("assetType(%q) = %q, want %q", name, got, want)
		}
	}
	if p := preferred("linux", "deb"); p[0] != "linux-amd64.deb" {
		t.Errorf("deb install should prefer the .deb: %v", p)
	}
	if p := preferred("linux", "standalone"); len(p) != 1 || p[0] != "linux-amd64.tar.gz" {
		t.Errorf("standalone linux = %v", p)
	}
	if p := preferred("windows", "nsis"); p[0] != "windows-amd64-installer.exe" {
		t.Errorf("nsis = %v", p)
	}
	if p := preferred("plan9", "x"); p != nil {
		t.Errorf("unknown OS = %v", p)
	}
}

func TestOSAssetsFiltersAndMarksTheDefault(t *testing.T) {
	assets := []ghAsset{
		{Name: "phantomops-linux-amd64.deb", URL: "u1", Size: 1},
		{Name: "phantomops-linux-amd64.tar.gz", URL: "u2", Size: 2},
		{Name: "phantomops-windows-amd64.zip", URL: "u3"},
		{Name: "SHA256SUMS", URL: "u4"},
		{Name: "phantomops-linux-source.txt", URL: "u5"},
	}
	got := osAssets(assets, "linux", "deb")
	if len(got) != 2 {
		t.Fatalf("assets = %+v", got)
	}
	for _, a := range got {
		if a.IsDefault != (a.Type == "deb") {
			t.Errorf("default flag wrong for %+v", a)
		}
	}
	if got := osAssets(assets, "linux", "standalone"); !got[1].IsDefault || got[0].IsDefault {
		t.Errorf("standalone should default to the tarball: %+v", got)
	}
}

func TestLookupChecksum(t *testing.T) {
	good := strings.Repeat("ab", 32)
	sums := []byte(good + "  phantomops-linux-amd64.tar.gz\n" +
		strings.ToUpper(strings.Repeat("cd", 32)) + " *phantomops-windows-amd64.zip\n" +
		strings.Repeat("ef", 31) + "  too-short\n" + // 62 hex chars
		strings.Repeat("zz", 32) + "  not-hex\n" +
		strings.Repeat("12", 32) + "  extra phantomops-two-fields\n")
	if got, err := lookupChecksum(sums, "phantomops-linux-amd64.tar.gz"); err != nil || got != good {
		t.Errorf("plain entry: %q %v", got, err)
	}
	if got, err := lookupChecksum(sums, "phantomops-windows-amd64.zip"); err != nil || got != strings.Repeat("cd", 32) {
		t.Errorf("binary-mode entry must be accepted and lowercased: %q %v", got, err)
	}
	for _, name := range []string{"too-short", "not-hex", "missing", "phantomops-linux-amd64", "linux-amd64.tar.gz", "phantomops-two-fields"} {
		if _, err := lookupChecksum(sums, name); err == nil {
			t.Errorf("%q must not resolve to a checksum", name)
		}
	}
}

// --- archives ------------------------------------------------------------------

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if strings.HasSuffix(name, "/") {
			_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755})
			continue
		}
		_ = tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o755, Size: int64(len(body))})
		_, _ = tw.Write([]byte(body))
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	got, err := extractBinary(tarGz(t, map[string]string{
		"README.md": "docs", "phantomops-linux/": "", "phantomops-linux/phantomops": "NEW-BINARY",
	}), "phantomops-linux-amd64.tar.gz")
	if err != nil || string(got) != "NEW-BINARY" {
		t.Errorf("tar.gz: %q %v", got, err)
	}

	got, err = extractBinary(zipOf(t, map[string]string{
		"LICENSE": "x", "PhantomOps.app/Contents/MacOS/PhantomOps": "MAC-BINARY",
	}), "phantomops-darwin-universal.zip")
	if err != nil || string(got) != "MAC-BINARY" {
		t.Errorf("zip: %q %v", got, err)
	}

	got, err = extractBinary(zipOf(t, map[string]string{"PhantomOps.exe": "WIN-BINARY"}), "phantomops-windows-amd64.zip")
	if runtime.GOOS != "darwin" && (err != nil || string(got) != "WIN-BINARY") {
		t.Errorf("windows zip: %q %v", got, err)
	}
}

func TestExtractBinaryFailures(t *testing.T) {
	if _, err := extractBinary(tarGz(t, map[string]string{"README.md": "docs", "other-tool": "x"}), "a.tar.gz"); err == nil ||
		!strings.Contains(err.Error(), "not found") {
		t.Errorf("archive without the program: %v", err)
	}
	if _, err := extractBinary([]byte("not gzip"), "a.tar.gz"); err == nil {
		t.Error("corrupt tar.gz accepted")
	}
	if _, err := extractBinary([]byte("not zip"), "a.zip"); err == nil {
		t.Error("corrupt zip accepted")
	}
	if _, err := extractBinary([]byte("x"), "a.deb"); err == nil || !strings.Contains(err.Error(), "not an archive") {
		t.Errorf("non-archive: %v", err)
	}
}

// --- Check and Install against a fake GitHub -------------------------------------

type fakeGitHub struct {
	srv      *httptest.Server
	tag      string
	draft    bool
	prerel   bool
	status   int               // API status override
	files    map[string][]byte // served under /dl/<tag>/<name>
	listed   []string          // names included in the release JSON (default: all files)
	rewrite  map[string]string // name -> URL override in the release JSON
	mu       sync.Mutex
	hits     map[string]int
	bigSums  bool
	tamperAs map[string][]byte // served content differs from the listed checksum
}

func newFakeGitHub(t *testing.T, tag string) *fakeGitHub {
	t.Helper()
	g := &fakeGitHub{tag: tag, files: map[string][]byte{}, hits: map[string]int{}, rewrite: map[string]string{}}
	g.srv = httptest.NewServer(http.HandlerFunc(g.serve))
	oldAPI, oldDL := apiBase, downloadBase
	apiBase, downloadBase = g.srv.URL, g.srv.URL+"/dl/"
	t.Cleanup(func() { g.srv.Close(); apiBase, downloadBase = oldAPI, oldDL })
	return g
}

func (g *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.hits[r.URL.Path]++
	g.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/releases/latest") {
		if g.status != 0 {
			http.Error(w, "x", g.status)
			return
		}
		names := g.listed
		if names == nil {
			for n := range g.files {
				names = append(names, n)
			}
		}
		var assets []map[string]any
		for _, n := range names {
			u := g.srv.URL + "/dl/" + g.tag + "/" + n
			if o, ok := g.rewrite[n]; ok {
				u = o
			}
			assets = append(assets, map[string]any{"name": n, "browser_download_url": u, "size": len(g.files[n])})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": g.tag, "name": "Release " + g.tag, "body": "notes",
			"html_url": "https://example.test/r", "draft": g.draft, "prerelease": g.prerel, "assets": assets})
		return
	}
	if name, ok := strings.CutPrefix(r.URL.Path, "/dl/"+g.tag+"/"); ok {
		if g.bigSums && name == checksumFile {
			_, _ = w.Write(bytes.Repeat([]byte("a"), 2<<20))
			return
		}
		if b, ok := g.tamperAs[name]; ok {
			_, _ = w.Write(b)
			return
		}
		if b, ok := g.files[name]; ok {
			_, _ = w.Write(b)
			return
		}
	}
	http.NotFound(w, r)
}

func (g *fakeGitHub) downloads(name string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hits["/dl/"+g.tag+"/"+name]
}

// release publishes one tarball plus a matching SHA256SUMS.
func (g *fakeGitHub) release(t *testing.T, binary string) string {
	t.Helper()
	name := "phantomops-" + runtime.GOOS + "-amd64.tar.gz"
	data := tarGz(t, map[string]string{"phantomops": binary})
	sum := sha256.Sum256(data)
	g.files[name] = data
	g.files[checksumFile] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	return name
}

func asRelease(t *testing.T, v string) {
	t.Helper()
	old := version.Version
	version.Version = v
	t.Cleanup(func() { version.Version = old })
}

func newUpdater(t *testing.T) (*Updater, string) {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "phantomops")
	if err := os.WriteFile(exe, []byte("OLD-BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}
	u := New()
	u.targetOverride, u.installType = exe, "standalone"
	return u, exe
}

func TestCheckReportsAnAvailableVerifiableUpdate(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	g.release(t, "NEW-BINARY")
	u, _ := newUpdater(t)
	info, err := u.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || !info.CanInstall || info.LatestVersion != "v0.3.0" || info.CurrentVersion != "v0.2.2" || info.ReleaseNotes != "notes" {
		t.Errorf("info = %+v", info)
	}
	if len(info.Assets) != 1 || !info.Assets[0].IsDefault {
		t.Errorf("assets = %+v", info.Assets)
	}
}

func TestCheckStates(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(g *fakeGitHub)
		cur    string
		verify func(t *testing.T, i Info, err error)
	}{
		{"no releases yet", func(g *fakeGitHub) { g.status = 404 }, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err != nil || !i.NoReleases || i.Available {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"rate limited", func(g *fakeGitHub) { g.status = 403 }, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err == nil || !strings.Contains(err.Error(), "rate limit") {
				t.Errorf("err = %v", err)
			}
		}},
		{"server error", func(g *fakeGitHub) { g.status = 500 }, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err == nil {
				t.Error("expected an error")
			}
		}},
		{"draft is ignored", func(g *fakeGitHub) { g.release(t, "x"); g.draft = true }, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err != nil || !i.NoReleases || i.Available {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"prerelease is ignored", func(g *fakeGitHub) { g.release(t, "x"); g.prerel = true }, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err != nil || !i.NoReleases || i.Available {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"already latest", func(g *fakeGitHub) { g.release(t, "x") }, "v0.3.0", func(t *testing.T, i Info, err error) {
			if err != nil || i.Available || i.CanInstall || !strings.Contains(i.Reason, "latest") {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"development build never auto-updates", func(g *fakeGitHub) { g.release(t, "x") }, "dev", func(t *testing.T, i Info, err error) {
			if err != nil || i.Available || i.CanInstall || !strings.Contains(i.Reason, "development build") {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"no checksum file means no install", func(g *fakeGitHub) {
			g.release(t, "x")
			g.listed = []string{"phantomops-" + runtime.GOOS + "-amd64.tar.gz"}
		}, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err != nil || !i.Available || i.CanInstall || !strings.Contains(i.Reason, "cannot be verified") {
				t.Errorf("%+v %v", i, err)
			}
		}},
		{"no download for this platform", func(g *fakeGitHub) {
			g.files["phantomops-plan9-amd64.tar.gz"] = []byte("x")
			g.files[checksumFile] = []byte("x")
		}, "v0.2.2", func(t *testing.T, i Info, err error) {
			if err != nil || i.CanInstall || !strings.Contains(i.Reason, "no download for") {
				t.Errorf("%+v %v", i, err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			asRelease(t, c.cur)
			g := newFakeGitHub(t, "v0.3.0")
			c.setup(g)
			u, _ := newUpdater(t)
			info, err := u.Check(context.Background())
			c.verify(t, info, err)
		})
	}
}

func TestInstallReplacesTheProgramAfterVerifying(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("cannot replace a running file on Windows")
	}
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	u, exe := newUpdater(t)
	var states []string
	u.OnProgress = func(p Progress) { states = append(states, p.Status) }
	if _, err := u.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := u.Install(context.Background(), name); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "NEW-BINARY" {
		t.Errorf("program = %q", b)
	}
	if p := u.CurrentProgress(); p.Status != "ready" || !strings.Contains(p.Message, "v0.3.0") {
		t.Errorf("progress = %+v", p)
	}
	for _, want := range []string{"downloading", "verifying", "installing", "ready"} {
		found := false
		for _, s := range states {
			found = found || s == want
		}
		if !found {
			t.Errorf("missing progress state %q in %v", want, states)
		}
	}
}

func TestInstallRefusesATamperedDownloadAndLeavesTheProgramAlone(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	g.tamperAs = map[string][]byte{name: tarGz(t, map[string]string{"phantomops": "EVIL-BINARY"})}
	u, exe := newUpdater(t)
	_, _ = u.Check(context.Background())
	err := u.Install(context.Background(), name)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD-BINARY" {
		t.Errorf("a failed verification must not touch the program: %q", b)
	}
	if p := u.CurrentProgress(); p.Status != "error" {
		t.Errorf("progress = %+v", p)
	}
	// And the updater is usable again afterwards.
	g.tamperAs = nil
	if runtime.GOOS != "windows" {
		if err := u.Install(context.Background(), name); err != nil {
			t.Errorf("retry after failure: %v", err)
		}
	}
}

func TestInstallRefusesWhenTheChecksumFileHasNoEntryForTheAsset(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	g.files[checksumFile] = []byte(strings.Repeat("ab", 32) + "  some-other-file\n")
	u, exe := newUpdater(t)
	_, _ = u.Check(context.Background())
	err := u.Install(context.Background(), name)
	if err == nil || !strings.Contains(err.Error(), "unverifiable") {
		t.Fatalf("err = %v", err)
	}
	if g.downloads(name) != 0 {
		t.Error("the asset must not even be downloaded when it cannot be verified")
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD-BINARY" {
		t.Error("program was modified")
	}
}

func TestInstallRefusals(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	u, exe := newUpdater(t)

	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "check for updates first") {
		t.Errorf("install before check: %v", err)
	}
	_, _ = u.Check(context.Background())

	// A foreign-OS asset that is otherwise perfectly valid (real archive,
	// correct checksum): only the OS check may stop it.
	other := "phantomops-plan9-amd64.tar.gz"
	g.files[other] = tarGz(t, map[string]string{"phantomops": "FOREIGN-BINARY"})
	sum := sha256.Sum256(g.files[other])
	g.files[checksumFile] = append(g.files[checksumFile], []byte(hex.EncodeToString(sum[:])+"  "+other+"\n")...)
	g.listed = nil
	_, _ = u.Check(context.Background())
	for _, bad := range []string{other, "SHA256SUMS", "phantomops-" + runtime.GOOS + "-amd64.tar.gz.sig", "../../etc/passwd", ""} {
		if err := u.Install(context.Background(), bad); err == nil {
			t.Errorf("asset %q was accepted", bad)
		}
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD-BINARY" {
		t.Error("program was modified")
	}
	if g.downloads(other) != 0 {
		t.Error("a foreign-OS asset must be refused before it is downloaded")
	}

	asRelease(t, "dev")
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "development builds") {
		t.Errorf("dev build: %v", err)
	}
	asRelease(t, "v0.3.0")
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "not newer") {
		t.Errorf("same version: %v", err)
	}
}

func TestInstallOnlyDownloadsFromTheReleaseLocation(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	evil := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("evil")) }))
	defer evil.Close()
	g.rewrite[name] = evil.URL + "/" + name
	u, exe := newUpdater(t)
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "unexpected download location") {
		t.Errorf("err = %v", err)
	}
	g.rewrite = map[string]string{checksumFile: "https://elsewhere.example/SHA256SUMS"}
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "unexpected download location") {
		t.Errorf("checksum file from elsewhere: %v", err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "OLD-BINARY" {
		t.Error("program was modified")
	}
}

func TestInstallRefusesAReleaseWithoutChecksums(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	g.listed = []string{name}
	u, _ := newUpdater(t)
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "unverifiable") {
		t.Errorf("err = %v", err)
	}
}

func TestDownloadLimits(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	g.bigSums = true
	u, _ := newUpdater(t)
	_, _ = u.Check(context.Background())
	if err := u.Install(context.Background(), name); err == nil ||
		(!strings.Contains(err.Error(), "too large") && !strings.Contains(err.Error(), "larger than allowed")) {
		t.Errorf("an oversized checksum file must be refused: %v", err)
	}
}

func TestOnlyOneInstallAtATime(t *testing.T) {
	asRelease(t, "v0.2.2")
	g := newFakeGitHub(t, "v0.3.0")
	name := g.release(t, "NEW-BINARY")
	u, _ := newUpdater(t)
	_, _ = u.Check(context.Background())
	u.mu.Lock()
	u.busy = true
	u.mu.Unlock()
	if err := u.Install(context.Background(), name); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Errorf("err = %v", err)
	}
	if g.downloads(name) != 0 {
		t.Error("a second install must not start downloading")
	}
}

func TestInstallTypeOverrideAndProgressDefaults(t *testing.T) {
	u := New()
	if p := u.CurrentProgress(); p.Status != "idle" {
		t.Errorf("initial progress = %+v", p)
	}
	u.installType = "rpm"
	if u.DetectInstallType() != "rpm" {
		t.Error("override ignored")
	}
}
