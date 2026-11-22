package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/update"
)

// fakeReleases serves a v0.3.0 release for linux/amd64 the way GitHub does.
func fakeReleases(t *testing.T) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	bin := []byte("studyhomie v0.3.0")
	tw.WriteHeader(&tar.Header{Name: "x/studyhomie", Mode: 0o755, Size: int64(len(bin)), Typeflag: tar.TypeReg})
	tw.Write(bin)
	tw.Close()
	gz.Close()
	archive := buf.Bytes()
	sum := sha256.Sum256(archive)
	const name = "studyhomie_v0.3.0_linux_amd64.tar.gz"
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			json.NewEncoder(w).Encode(update.Release{Tag: "v0.3.0", Notes: "- algo nuevo", Page: "https://example.test",
				Assets: []update.Asset{{Name: name, URL: ts.URL + "/" + name}, {Name: "checksums.txt", URL: ts.URL + "/checksums.txt"}}})
		case "/" + name:
			w.Write(archive)
		case "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(sum[:]), name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func withUpdater(t *testing.T, s *Server, gh *httptest.Server, current string) string {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "studyhomie")
	os.WriteFile(exe, []byte("old"), 0o755)
	s.Updater = &update.Updater{Current: current, Repo: "o/r", API: gh.URL, Exe: exe, StatePath: filepath.Join(dir, "update.json"),
		GOOS: "linux", GOARCH: "amd64", Client: gh.Client(), Now: time.Now}
	return exe
}

func TestUpdateDevBuildIsOff(t *testing.T) {
	_, ts := newTestServer(t, &scripted{})
	res, body := do(t, ts, "GET", "/api/update", nil, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"enabled":false`) || !strings.Contains(string(body), `"current":"dev"`) {
		t.Fatalf("%d %s", res.StatusCode, body)
	}
	if res, _ := do(t, ts, "POST", "/api/update", map[string]any{}, nil); res.StatusCode != 400 {
		t.Fatalf("install on a dev build: %d", res.StatusCode)
	}
}

func TestUpdateFlow(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	s.ConfigPath = filepath.Join(t.TempDir(), "studyhomie.toml")
	exe := withUpdater(t, s, fakeReleases(t), "v0.2.0")
	restarted := make(chan struct{}, 1)
	s.Restart = func() { restarted <- struct{}{} }

	var st updateStatus
	_, body := do(t, ts, "GET", "/api/update", nil, nil)
	json.Unmarshal(body, &st)
	if !st.Available || st.Latest != "v0.3.0" || st.Mode != "ask" || !st.CanRestart || st.Notes == "" {
		t.Fatalf("status: %s", body)
	}

	// skipping silences it
	_, body = do(t, ts, "POST", "/api/update/skip", map[string]string{"version": "v0.3.0"}, nil)
	if !strings.Contains(string(body), `"skipped":true`) {
		t.Fatalf("skip: %s", body)
	}

	// the mode is saved to the config file
	if res, _ := do(t, ts, "POST", "/api/update/mode", map[string]string{"mode": "bogus"}, nil); res.StatusCode != 400 {
		t.Fatalf("bad mode: %d", res.StatusCode)
	}
	if res, body := do(t, ts, "POST", "/api/update/mode", map[string]string{"mode": "never"}, nil); res.StatusCode != 200 {
		t.Fatalf("mode: %d %s", res.StatusCode, body)
	}
	if cfg, _ := config.Load(s.ConfigPath); cfg.Updates != "never" {
		t.Fatalf("saved updates = %q", cfg.Updates)
	}

	// while a flow runs, installing needs confirmation
	s.mu.Lock()
	s.runs[999] = &Run{ID: 999} // working: not done, not waiting
	s.mu.Unlock()
	res, body := do(t, ts, "POST", "/api/update", map[string]any{}, nil)
	if res.StatusCode != http.StatusConflict || !strings.Contains(string(body), `"busy":true`) {
		t.Fatalf("busy: %d %s", res.StatusCode, body)
	}
	if b, _ := os.ReadFile(exe); string(b) != "old" {
		t.Fatal("installed despite the running flow")
	}

	res, body = do(t, ts, "POST", "/api/update", map[string]any{"force": true}, nil)
	if res.StatusCode != 200 || !strings.Contains(string(body), `"restarting":true`) {
		t.Fatalf("install: %d %s", res.StatusCode, body)
	}
	if b, _ := os.ReadFile(exe); string(b) != "studyhomie v0.3.0" {
		t.Fatalf("binary = %q", b)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("no restart after installing")
	}
}

func TestAutoModeInstallsInTheBackground(t *testing.T) {
	s, _ := newTestServer(t, &scripted{})
	s.Cfg.Updates = "auto"
	exe := withUpdater(t, s, fakeReleases(t), "v0.2.0")
	s.autoUpdate(t.Context())
	if b, _ := os.ReadFile(exe); string(b) != "studyhomie v0.3.0" {
		t.Fatalf("binary = %q", b)
	}
	if st := s.Updater.Status(); !st.Staged {
		t.Fatalf("not staged: %+v", st)
	}

	// asking (the default) never installs on its own
	s2, _ := newTestServer(t, &scripted{})
	exe2 := withUpdater(t, s2, fakeReleases(t), "v0.2.0")
	s2.autoUpdate(t.Context())
	if b, _ := os.ReadFile(exe2); string(b) != "old" {
		t.Fatal("installed without asking")
	}
}
