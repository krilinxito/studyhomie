package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.2.0", "v0.2.0", 0},
		{"v0.3.0", "v0.2.9", 1},
		{"0.10.0", "v0.9.0", 1},
		{"v1.0.0", "v1.0.0-rc.1", 1},
		{"v1.0.0-rc.2", "v1.0.0-rc.10", -1},
		{"v1.0.0-alpha", "v1.0.0-alpha.1", -1},
		{"v1.0.0-alpha.beta", "v1.0.0-alpha.1", 1},
		{"v1.0.0+build", "v1.0.0", 0},
		{"dev", "v0.1.0", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
	if Newer("v0.3.0", "dev") || Newer("dev", "v0.1.0") || !Newer("v0.2.1", "v0.2.0") {
		t.Error("Newer: development builds must never be updated")
	}
	for _, bad := range []string{"", "dev", "v1", "v1.2", "v1.2.x", "v1.2.3-"} {
		if Valid(bad) {
			t.Errorf("Valid(%q) = true", bad)
		}
	}
}

// fakeGitHub serves one release with an archive of this platform's kind holding bin.
type fakeGitHub struct {
	*httptest.Server
	release  Release
	files    map[string][]byte
	apiCalls atomic.Int32
}

func newFake(t *testing.T, tag, goos string, bin []byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{files: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/o/r/releases/latest" {
			f.apiCalls.Add(1)
			if f.release.Tag == "" {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(f.release)
			return
		}
		b, ok := f.files[strings.TrimPrefix(r.URL.Path, "/dl/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(f.Close)
	name := fmt.Sprintf("studyhomie_%s_%s_amd64", tag, goos)
	var archive []byte
	if goos == "windows" {
		name += ".zip"
		archive = zipOf(t, map[string][]byte{"studyhomie_x/README.md": []byte("hola"), "studyhomie_x/studyhomie.exe": bin})
	} else {
		name += ".tar.gz"
		archive = tgzOf(t, map[string][]byte{"studyhomie_x/README.md": []byte("hola"), "studyhomie_x/studyhomie": bin})
	}
	sum := sha256.Sum256(archive)
	f.files[name] = archive
	f.files[checksumsName] = []byte(fmt.Sprintf("%s  %s\n%s  otro.zip\n", hex.EncodeToString(sum[:]), name, strings.Repeat("0", 64)))
	f.release = Release{Tag: tag, Notes: "## Novedades\n- algo", Page: "https://example.test/r",
		Assets: []Asset{{Name: name, URL: f.URL + "/dl/" + name}, {Name: checksumsName, URL: f.URL + "/dl/" + checksumsName}}}
	return f
}

func zipOf(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}
	zw.Close()
	return buf.Bytes()
}

func tgzOf(t *testing.T, files map[string][]byte) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, b := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(b)), Typeflag: tar.TypeReg})
		tw.Write(b)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func newUpdater(t *testing.T, f *fakeGitHub, current, goos string) *Updater {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, binaryName(goos))
	if err := os.WriteFile(exe, []byte("viejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Updater{Current: current, Repo: "o/r", API: f.URL, Exe: exe, StatePath: filepath.Join(dir, "state", "update.json"),
		GOOS: goos, GOARCH: "amd64", Client: f.Client(), Now: time.Now}
}

func TestInstallNewer(t *testing.T) {
	for _, goos := range []string{"windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			f := newFake(t, "v0.3.0", goos, []byte("nuevo"))
			u := newUpdater(t, f, "v0.2.0", goos)
			st := u.Check(context.Background(), false)
			if !st.Available || st.Latest != "v0.3.0" || st.Notes == "" || st.Error != "" {
				t.Fatalf("status = %+v", st)
			}
			tag, err := u.Install(context.Background())
			if err != nil || tag != "v0.3.0" {
				t.Fatalf("Install = %q, %v", tag, err)
			}
			if b, _ := os.ReadFile(u.Exe); string(b) != "nuevo" {
				t.Fatalf("binary = %q", b)
			}
			if !u.Status().Staged {
				t.Fatal("not marked staged after installing")
			}
			// the state survives a restart of the app
			again := &Updater{Current: "v0.2.0", Repo: "o/r", API: f.URL, StatePath: u.StatePath, GOOS: goos, GOARCH: "amd64", Client: f.Client(), Now: time.Now}
			if s := again.Status(); !s.Staged || s.Latest != "v0.3.0" {
				t.Fatalf("reloaded status = %+v", s)
			}
			// running the new version: nothing to do
			now := &Updater{Current: "v0.3.0", Repo: "o/r", API: f.URL, StatePath: u.StatePath, GOOS: goos, GOARCH: "amd64", Client: f.Client(), Now: time.Now}
			if s := now.Status(); s.Available || s.Staged {
				t.Fatalf("status on the new version = %+v", s)
			}
		})
	}
}

func TestSameVersionAndDev(t *testing.T) {
	f := newFake(t, "v0.2.0", "linux", []byte("nuevo"))
	u := newUpdater(t, f, "v0.2.0", "linux")
	if st := u.Check(context.Background(), false); st.Available || st.Latest != "v0.2.0" {
		t.Fatalf("same version: %+v", st)
	}
	if _, err := u.Install(context.Background()); err == nil {
		t.Fatal("installed with nothing newer")
	}
	dev := newUpdater(t, f, "dev", "linux")
	if st := dev.Check(context.Background(), true); st.Enabled || st.Available {
		t.Fatalf("dev: %+v", st)
	}
	if f.apiCalls.Load() != 1 {
		t.Fatalf("a dev build asked GitHub: %d calls", f.apiCalls.Load())
	}
}

func TestCheckCaches(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	u := newUpdater(t, f, "v0.2.0", "linux")
	now := time.Now()
	u.Now = func() time.Time { return now }
	ctx := context.Background()
	u.Check(ctx, false)
	u.Check(ctx, false)
	if n := f.apiCalls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1 (cached)", n)
	}
	u.Check(ctx, true)
	now = now.Add(CheckEvery + time.Minute)
	u.Check(ctx, false)
	if n := f.apiCalls.Load(); n != 3 {
		t.Fatalf("calls = %d, want 3", n)
	}
}

func TestBadChecksum(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	name := f.release.Assets[0].Name
	f.files[name] = append([]byte{}, f.files[name]...)
	f.files[name][len(f.files[name])-1] ^= 0xff
	u := newUpdater(t, f, "v0.2.0", "linux")
	u.Check(context.Background(), false)
	_, err := u.Install(context.Background())
	if err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "viejo" {
		t.Fatalf("binary replaced by a corrupt download: %q", b)
	}
}

func TestMissingAssets(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	f.release.Assets = f.release.Assets[1:] // only checksums.txt
	u := newUpdater(t, f, "v0.2.0", "linux")
	st := u.Check(context.Background(), false)
	if st.Available || !strings.Contains(st.Error, "linux/amd64") {
		t.Fatalf("status = %+v", st)
	}

	g := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	g.release.Assets = g.release.Assets[:1] // no checksums.txt
	v := newUpdater(t, g, "v0.2.0", "linux")
	v.Check(context.Background(), false)
	if _, err := v.Install(context.Background()); err == nil || !strings.Contains(err.Error(), checksumsName) {
		t.Fatalf("installed without checksums: %v", err)
	}
}

func TestNoReleaseAndServerDown(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", nil)
	f.release.Tag = ""
	u := newUpdater(t, f, "v0.2.0", "linux")
	if st := u.Check(context.Background(), true); st.Available || st.Error != "" {
		t.Fatalf("no releases: %+v", st)
	}
	f.Close()
	if st := u.Check(context.Background(), true); st.Error == "" {
		t.Fatal("no error with GitHub unreachable")
	}
}

func TestSkip(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	u := newUpdater(t, f, "v0.2.0", "linux")
	u.Check(context.Background(), false)
	u.Skip("v0.3.0")
	if st := u.Status(); !st.Skipped || !st.Available {
		t.Fatalf("status = %+v", st)
	}
	f.release.Tag = "v0.3.1"
	if st := u.Check(context.Background(), true); st.Skipped {
		t.Fatal("a newer version stayed skipped")
	}
}

func TestReplaceAndCleanup(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "studyhomie.exe")
	os.WriteFile(exe, []byte("viejo"), 0o755)
	if err := Replace(exe, []byte("nuevo")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != "nuevo" {
		t.Fatalf("exe = %q", b)
	}
	Cleanup(exe)
	if _, err := os.Stat(exe + ".old"); !os.IsNotExist(err) {
		t.Fatal(".old left behind after Cleanup")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestMode(t *testing.T) {
	for in, want := range map[string]string{"": ModeAsk, "AUTO": ModeAuto, "never": ModeNever, "x": ModeAsk} {
		if got := Mode(in); got != want {
			t.Errorf("Mode(%q) = %q", in, got)
		}
	}
}

func TestManaged(t *testing.T) {
	cases := []struct{ exe, manager string }{
		{`C:\Users\ana\scoop\apps\studyhomie\0.5.0\studyhomie.exe`, "Scoop"},
		{`C:\ProgramData\chocolatey\lib\studyhomie\tools\studyhomie_v0.5.0_windows_amd64\studyhomie.exe`, "Chocolatey"},
		{"/opt/homebrew/Cellar/studyhomie/0.5.0/bin/studyhomie", "Homebrew"},
		{"/usr/local/Cellar/studyhomie/0.5.0/bin/studyhomie", "Homebrew"},
		{"/home/linuxbrew/.linuxbrew/Cellar/studyhomie/0.5.0/bin/studyhomie", "Homebrew"},
		{`C:\Users\ana\AppData\Roaming\npm\node_modules\studyhomie\vendor\studyhomie.exe`, "npm"},
		{"/usr/lib/node_modules/studyhomie/vendor/studyhomie", "npm"},
		{`C:\Users\ana\AppData\Local\Programs\StudyHomie\studyhomie.exe`, ""},
		{`C:\Users\ana\go\bin\studyhomie.exe`, ""},
		{"/home/ana/.local/bin/studyhomie", ""},
	}
	for _, c := range cases {
		m, cmd := Managed(c.exe)
		if m != c.manager || (m == "") != (cmd == "") {
			t.Errorf("Managed(%q) = %q, %q; want %q", c.exe, m, cmd, c.manager)
		}
	}
}

func TestManagedDoesNotInstall(t *testing.T) {
	f := newFake(t, "v0.3.0", "linux", []byte("nuevo"))
	u := newUpdater(t, f, "v0.2.0", "linux")
	u.Manager, u.Upgrade = "Homebrew", "brew upgrade studyhomie"
	st := u.Check(context.Background(), false)
	if !st.Available || st.Manager != "Homebrew" || st.Upgrade != "brew upgrade studyhomie" {
		t.Fatalf("status = %+v", st)
	}
	if _, err := u.Install(context.Background()); err == nil || !strings.Contains(err.Error(), "brew upgrade studyhomie") {
		t.Fatalf("Install = %v; want the brew command", err)
	}
	if b, _ := os.ReadFile(u.Exe); string(b) != "viejo" {
		t.Fatalf("binary was replaced: %q", b)
	}
}
