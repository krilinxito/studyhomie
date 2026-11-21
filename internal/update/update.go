// Package update finds newer StudyHomie releases on GitHub and installs them over the running
// binary: it downloads the archive for this platform, checks its sha256 against the release's
// checksums.txt, swaps the executable and starts it again.
package update

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
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultRepo is where releases are published.
	DefaultRepo = "krilinxito/studyhomie"
	// DefaultAPI is GitHub's API; STUDYHOMIE_UPDATE_API replaces it (a local fake, in tests).
	DefaultAPI = "https://api.github.com"
	// CheckEvery is how long a check is trusted before GitHub is asked again. Unauthenticated
	// requests are limited to 60 an hour.
	CheckEvery = 6 * time.Hour
	// RestartedEnv marks a process started by Restart, so it does not open another browser tab.
	RestartedEnv = "STUDYHOMIE_RESTARTED"

	checksumsName = "checksums.txt"
	maxArchive    = 200 << 20
	maxBinary     = 300 << 20
	maxJSON       = 2 << 20
)

// Modes for the "updates" setting.
const (
	ModeAsk   = "ask"   // tell the user and let them decide (default)
	ModeAuto  = "auto"  // install in the background; it applies on the next start
	ModeNever = "never" // do not look
)

// Mode normalises a setting value; anything unknown asks.
func Mode(s string) string {
	switch s = strings.ToLower(strings.TrimSpace(s)); s {
	case ModeAuto, ModeNever:
		return s
	}
	return ModeAsk
}

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

type Release struct {
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Notes      string    `json:"body"`
	Page       string    `json:"html_url"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Assets     []Asset   `json:"assets"`
}

// state is what is remembered between runs.
type state struct {
	Checked time.Time `json:"checked"`
	Latest  *Release  `json:"latest,omitempty"`
	Skipped string    `json:"skipped,omitempty"` // a version the user chose to ignore
	Staged  string    `json:"staged,omitempty"`  // a version already written over the binary
}

// Status is what the app shows about updates.
type Status struct {
	Current   string    `json:"current"`
	Enabled   bool      `json:"enabled"` // false for development builds, which never update
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"` // newer than this one, with a download for this platform
	Notes     string    `json:"notes,omitempty"`
	Page      string    `json:"page,omitempty"`
	Published time.Time `json:"published,omitempty"`
	Skipped   bool      `json:"skipped"` // the user said to skip this version
	Staged    bool      `json:"staged"`  // already installed: it applies when the app restarts
	Checked   time.Time `json:"checked,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Manager is the package manager that installed this binary (Scoop, Homebrew…): it owns the
	// updates, so the app tells the user Upgrade, the command to run, instead of installing.
	Manager string `json:"manager,omitempty"`
	Upgrade string `json:"upgrade,omitempty"`
}

type Updater struct {
	Current   string // the running version
	Repo      string
	API       string
	Exe       string // the binary that gets replaced
	StatePath string // where the state is kept between runs; empty keeps it in memory
	GOOS      string
	GOARCH    string
	Client    *http.Client
	Now       func() time.Time
	Manager   string // see Managed
	Upgrade   string

	mu     sync.Mutex
	st     state
	loaded bool
	busy   bool
}

// New is an updater for the running binary, with its state in the user's config folder.
func New(current, exe string) *Updater {
	u := &Updater{
		Current: current, Repo: DefaultRepo, API: DefaultAPI, Exe: exe,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		Client: &http.Client{Timeout: 5 * time.Minute}, Now: time.Now,
	}
	if api := os.Getenv("STUDYHOMIE_UPDATE_API"); api != "" {
		u.API = strings.TrimRight(api, "/")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		u.StatePath = filepath.Join(dir, "studyhomie", "update.json")
	}
	u.Manager, u.Upgrade = Managed(exe)
	return u
}

// Managed tells whether exe was installed by a package manager, which then owns its updates:
// it returns the manager's name and the command that updates it, or "" when the binary is the
// user's own (go install, the install script, a downloaded zip).
func Managed(exe string) (manager, upgrade string) {
	p := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	switch {
	case strings.Contains(p, "/scoop/apps/"):
		return "Scoop", "scoop update studyhomie"
	case strings.Contains(p, "/chocolatey/lib/"):
		return "Chocolatey", "choco upgrade studyhomie"
	case strings.Contains(p, "/cellar/"), strings.Contains(p, "/homebrew/"), strings.Contains(p, "/linuxbrew/"):
		return "Homebrew", "brew upgrade studyhomie"
	case strings.Contains(p, "/node_modules/"):
		return "npm", "npm install -g studyhomie@latest"
	}
	return "", ""
}

// Enabled reports whether this build can update itself: development builds cannot.
func (u *Updater) Enabled() bool { return u != nil && Valid(u.Current) }

func (u *Updater) load() {
	if u.loaded {
		return
	}
	u.loaded = true
	if u.StatePath == "" {
		return
	}
	if b, err := os.ReadFile(u.StatePath); err == nil {
		json.Unmarshal(b, &u.st)
	}
}

func (u *Updater) save() {
	if u.StatePath == "" {
		return
	}
	b, err := json.MarshalIndent(u.st, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(u.StatePath), 0o755); err == nil {
		os.WriteFile(u.StatePath, b, 0o644)
	}
}

// Status reports what is known without asking GitHub.
func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.load()
	return u.status()
}

func (u *Updater) status() Status {
	s := Status{Current: u.Current, Enabled: Valid(u.Current), Checked: u.st.Checked, Manager: u.Manager, Upgrade: u.Upgrade}
	r := u.st.Latest
	if !s.Enabled || r == nil {
		return s
	}
	s.Latest, s.Notes, s.Page, s.Published = r.Tag, r.Notes, r.Page, r.Published
	if !Newer(r.Tag, u.Current) {
		return s
	}
	if _, ok := u.asset(r); !ok {
		s.Error = fmt.Sprintf("la versión %s no trae un archivo para %s/%s", r.Tag, u.GOOS, u.GOARCH)
		return s
	}
	s.Available = true
	s.Skipped = u.st.Skipped == r.Tag
	s.Staged = u.st.Staged == r.Tag
	return s
}

// Check asks GitHub for the latest release, unless the last answer is recent and force is false.
// A failed check keeps what was known and reports the error.
func (u *Updater) Check(ctx context.Context, force bool) Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.load()
	if !Valid(u.Current) || (!force && !u.st.Checked.IsZero() && u.Now().Sub(u.st.Checked) < CheckEvery) {
		return u.status()
	}
	r, err := u.latest(ctx)
	if err != nil {
		s := u.status()
		s.Error = "no pude buscar actualizaciones: " + err.Error()
		return s
	}
	u.st.Checked, u.st.Latest = u.Now(), r
	u.save()
	return u.status()
}

func (u *Updater) latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/repos/%s/releases/latest", u.API, u.Repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "studyhomie-updater/"+u.Current)
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil // no release published yet
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub respondió %s", res.Status)
	}
	var r Release
	if err := json.NewDecoder(io.LimitReader(res.Body, maxJSON)).Decode(&r); err != nil {
		return nil, fmt.Errorf("respuesta de GitHub ilegible: %w", err)
	}
	if r.Draft || r.Prerelease || !Valid(r.Tag) {
		return nil, nil
	}
	return &r, nil
}

// Skip silences a version until a newer one comes out.
func (u *Updater) Skip(version string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.load()
	u.st.Skipped = version
	u.save()
}

// asset is this platform's archive in a release.
func (u *Updater) asset(r *Release) (Asset, bool) {
	ext := ".tar.gz"
	if u.GOOS == "windows" {
		ext = ".zip"
	}
	suffix := "_" + u.GOOS + "_" + u.GOARCH + ext
	for _, a := range r.Assets {
		if strings.HasPrefix(a.Name, "studyhomie_") && strings.HasSuffix(a.Name, suffix) {
			return a, true
		}
	}
	return Asset{}, false
}

// ErrBusy means an install is already running.
var ErrBusy = errors.New("ya se está instalando una actualización")

// Install downloads the latest known release, verifies it and writes it over the binary.
// The running program is not touched: the new version runs after a restart.
func (u *Updater) Install(ctx context.Context) (string, error) {
	u.mu.Lock()
	u.load()
	st := u.status()
	if u.busy {
		u.mu.Unlock()
		return "", ErrBusy
	}
	if !st.Enabled {
		u.mu.Unlock()
		return "", errors.New("esta es una compilación de desarrollo: no se actualiza sola")
	}
	if u.Manager != "" {
		u.mu.Unlock()
		return "", fmt.Errorf("instalaste StudyHomie con %s: actualízalo con «%s»", u.Manager, u.Upgrade)
	}
	if st.Staged {
		u.mu.Unlock()
		return st.Latest, nil
	}
	if !st.Available {
		u.mu.Unlock()
		if st.Error != "" {
			return "", errors.New(st.Error)
		}
		return "", errors.New("no hay una versión nueva")
	}
	rel := *u.st.Latest
	u.busy = true
	u.mu.Unlock()

	err := u.install(ctx, &rel)

	u.mu.Lock()
	defer u.mu.Unlock()
	u.busy = false
	if err != nil {
		return "", err
	}
	u.st.Staged = rel.Tag
	u.save()
	return rel.Tag, nil
}

func (u *Updater) install(ctx context.Context, rel *Release) error {
	if u.Exe == "" {
		return errors.New("no sé dónde está el programa")
	}
	a, _ := u.asset(rel)
	var sums Asset
	for _, x := range rel.Assets {
		if x.Name == checksumsName {
			sums = x
		}
	}
	if sums.URL == "" {
		return fmt.Errorf("la versión %s no trae %s: no la instalo sin poder verificarla", rel.Tag, checksumsName)
	}
	list, err := u.download(ctx, sums.URL, maxJSON)
	if err != nil {
		return fmt.Errorf("no pude descargar %s: %w", checksumsName, err)
	}
	want, ok := checksum(list, a.Name)
	if !ok {
		return fmt.Errorf("%s no menciona %s", checksumsName, a.Name)
	}
	archive, err := u.download(ctx, a.URL, maxArchive)
	if err != nil {
		return fmt.Errorf("no pude descargar %s: %w", a.Name, err)
	}
	if sum := sha256.Sum256(archive); hex.EncodeToString(sum[:]) != want {
		return fmt.Errorf("%s llegó dañado o alterado (el sha256 no coincide): no lo instalo", a.Name)
	}
	bin, err := extract(archive, a.Name, binaryName(u.GOOS))
	if err != nil {
		return err
	}
	if err := Replace(u.Exe, bin); err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("no tengo permiso para escribir en %s. Descarga la versión %s a mano desde %s", filepath.Dir(u.Exe), rel.Tag, rel.Page)
		}
		return fmt.Errorf("no pude reemplazar el programa: %w", err)
	}
	return nil
}

func (u *Updater) download(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "studyhomie-updater/"+u.Current)
	res, err := u.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("respuesta %s", res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("el archivo es demasiado grande")
	}
	return b, nil
}

// checksum finds a file's sha256 in a sha256sum listing ("<hex>  <name>").
func checksum(list []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(list))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), true
		}
	}
	return "", false
}

func binaryName(goos string) string {
	if goos == "windows" {
		return "studyhomie.exe"
	}
	return "studyhomie"
}

// extract takes the program out of a release archive (.zip or .tar.gz).
func extract(archive []byte, name, bin string) ([]byte, error) {
	missing := fmt.Errorf("%s no contiene %s", name, bin)
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, fmt.Errorf("%s no es un zip válido: %w", name, err)
		}
		for _, f := range zr.File {
			if f.FileInfo().IsDir() || path.Base(f.Name) != bin {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return readLimited(rc)
		}
		return nil, missing
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("%s no es un .tar.gz válido: %w", name, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, missing
		}
		if err != nil {
			return nil, fmt.Errorf("%s está dañado: %w", name, err)
		}
		if h.Typeflag == tar.TypeReg && path.Base(h.Name) == bin {
			return readLimited(tr)
		}
	}
}

func readLimited(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBinary+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBinary {
		return nil, errors.New("el programa descargado es demasiado grande")
	}
	if len(b) == 0 {
		return nil, errors.New("el programa descargado está vacío")
	}
	return b, nil
}

// Replace writes bin over the executable at exe. The new file is written next to it first, so a
// failure leaves the old one in place. Windows does not let a running .exe be overwritten but does
// let it be renamed: it moves to <exe>.old, which Cleanup removes on the next start. Elsewhere the
// rename over the old file is atomic.
func Replace(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".studyhomie-new-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(bin)
	cerr := tmp.Close()
	if werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o755)
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return werr
	}
	if runtime.GOOS != "windows" {
		if err := os.Rename(tmp.Name(), exe); err != nil {
			os.Remove(tmp.Name())
			return err
		}
		return nil
	}
	old := exe + ".old"
	os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		os.Rename(old, exe)
		os.Remove(tmp.Name())
		return err
	}
	return nil
}

// Cleanup removes what an earlier update left behind. Best effort: the old .exe may still be
// running (Windows keeps the parent of a restart alive) and is removed on a later start.
func Cleanup(exe string) {
	if exe != "" {
		os.Remove(exe + ".old")
	}
}

// Executable is the path of the running program, with symlinks resolved, or "" if unknown.
func Executable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return exe
}
