package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/vault"
)

// errVaultBusy: something is being written, and switching vaults would pull the folder from under it.
var errVaultBusy = errors.New(i18n.N("hay algo en curso: espera a que termine o detenlo antes de cambiar de vault"))

type vaultInfo struct {
	Name    string `json:"name"`
	Path    string `json:"path"` // as written in the config
	Abs     string `json:"abs"`
	Current bool   `json:"current"`
	Exists  bool   `json:"exists"`
	Guides  int    `json:"guides"`
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

func samePath(a, b string) bool { return strings.EqualFold(absPath(a), absPath(b)) }

func countMD(dir string) int {
	entries, _ := os.ReadDir(dir)
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
			n++
		}
	}
	return n
}

// vaults lists the known vaults and sums up the one in use.
func (s *Server) vaults(w http.ResponseWriter, r *http.Request) {
	_, cfg, _ := s.backend()
	v := s.vault()
	var list []vaultInfo
	for _, p := range cfg.VaultList() {
		abs := absPath(p)
		info, err := os.Stat(abs)
		list = append(list, vaultInfo{
			Name: filepath.Base(abs), Path: p, Abs: abs, Current: samePath(abs, v.Root),
			Exists: err == nil && info.IsDir(), Guides: countMD(filepath.Join(abs, vault.GuidesDir)),
		})
	}
	writeJSON(w, 200, map[string]any{"vaults": list, "root": absPath(v.Root), "stats": v.Stat(), "canPick": runtime.GOOS == "windows" || s.PickFolder != nil})
}

// switchVault puts another vault in use: its notes, its chats, its map. A chat that is only
// waiting for a message is put aside (it can be continued later in its own vault).
func (s *Server) switchVault(path string) error {
	if samePath(path, s.vault().Root) {
		return nil
	}
	v, err := vault.Open(path) // a new folder gets the vault layout
	if err != nil {
		return err
	}
	if s.working() {
		return errVaultBusy
	}
	s.pauseAll()
	s.mu.Lock()
	if s.main != nil && !s.main.isDone() {
		if s.main.waiting() != "chat" {
			s.mu.Unlock()
			return errVaultBusy
		}
		s.main.park()
	}
	s.vmu.Lock()
	s.V = v
	s.vmu.Unlock()
	_, _ = v.MigrateSources()
	go purgeTrash(v)
	s.runs, s.main, s.seq, s.store, s.practices = map[int]*Run{}, nil, 0, nil, map[string]*practiceRec{}
	s.pauseStalePractices()
	if st, err := newStore(s.sessionsDir()); err == nil {
		s.store = st
		s.loadSessions()
	}
	s.mu.Unlock()
	return s.rememberVaults(func(cfg *config.Config) {
		cfg.Vaults = append(cfg.VaultList(), path)
		cfg.Vault = path
		cfg.Vaults = cfg.VaultList()
	})
}

// rememberVaults changes the vault list and saves it in the config file.
func (s *Server) rememberVaults(fn func(*config.Config)) error {
	s.pmu.Lock()
	cfg := s.Cfg
	fn(&cfg)
	s.Cfg = cfg
	s.pmu.Unlock()
	if s.ConfigPath != "" {
		return config.Write(s.ConfigPath, cfg)
	}
	return nil
}

// addVault opens (or creates) a vault at a folder and puts it in use.
func (s *Server) addVault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	p := strings.TrimSpace(strings.Trim(strings.TrimSpace(body.Path), `"`))
	if p == "" || !filepath.IsAbs(p) && !samePath(p, "vault") {
		fail(w, 400, "escribe la ruta completa de la carpeta (por ejemplo D:\\Universidad\\Calculo)")
		return
	}
	p = filepath.Clean(p)
	if info, err := os.Stat(p); err == nil && !info.IsDir() {
		fail(w, 400, "esa ruta es un archivo, no una carpeta")
		return
	}
	if err := s.switchVault(p); err != nil {
		code := 500
		if errors.Is(err, errVaultBusy) {
			code = 409
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "root": absPath(p)})
}

// useVault puts a known vault in use.
func (s *Server) useVault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	_, cfg, _ := s.backend()
	known := false
	for _, p := range cfg.VaultList() {
		known = known || samePath(p, body.Path)
	}
	if !known {
		fail(w, 404, "ese vault no está en tu lista")
		return
	}
	if err := s.switchVault(body.Path); err != nil {
		code := 500
		if errors.Is(err, errVaultBusy) {
			code = 409
		}
		fail(w, code, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// forgetVault takes a vault off the list. Its folder and notes stay where they are.
func (s *Server) forgetVault(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if samePath(body.Path, s.vault().Root) {
		fail(w, 400, "es el vault en uso: cambia a otro antes de quitarlo de la lista")
		return
	}
	err := s.rememberVaults(func(cfg *config.Config) {
		var keep []string
		for _, p := range cfg.VaultList() {
			if !samePath(p, body.Path) {
				keep = append(keep, p)
			}
		}
		cfg.Vaults = keep
	})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// pickFolder shows the system's folder picker on this computer and answers with the folder chosen
// ("" when cancelled).
func (s *Server) pickFolder(w http.ResponseWriter, r *http.Request) {
	pick := s.PickFolder
	if pick == nil {
		if runtime.GOOS != "windows" {
			fail(w, 501, "el selector de carpetas solo está en Windows: escribe la ruta")
			return
		}
		pick = windowsFolderPicker
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	p, err := pick(ctx)
	if err != nil {
		fail(w, 500, "no pude abrir el selector: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"path": p})
}

// windowsFolderPicker runs the folder dialog of Windows through PowerShell, on top of other windows.
func windowsFolderPicker(ctx context.Context) (string, error) {
	script := `[Console]::OutputEncoding = [Text.Encoding]::UTF8
Add-Type -AssemblyName System.Windows.Forms
$owner = New-Object System.Windows.Forms.Form -Property @{ TopMost = $true; ShowInTaskbar = $false }
$d = New-Object System.Windows.Forms.FolderBrowserDialog
$d.Description = 'Elige la carpeta del vault (o crea una nueva)'
$d.ShowNewFolderButton = $true
if ($d.ShowDialog($owner) -eq [System.Windows.Forms.DialogResult]::OK) { $d.SelectedPath }`
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-STA", "-Command", script).Output()
	return strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff")), err
}

// openOutside opens the vault, or one of its notes, in the file explorer or in Obsidian.
func (s *Server) openOutside(w http.ResponseWriter, r *http.Request) {
	var body struct {
		App  string `json:"app"` // folder | obsidian
		Dir  string `json:"dir"`
		Slug string `json:"slug"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	v := s.vault()
	target := absPath(v.Root)
	if body.Slug != "" {
		raw, _, err := v.ReadRaw(body.Dir, body.Slug)
		if err != nil || raw == "" {
			fail(w, 404, "no existe esa nota")
			return
		}
		target = filepath.Join(target, body.Dir, body.Slug+".md")
	}
	open := s.Open
	if open == nil {
		open = openWithSystem
	}
	var err error
	switch body.App {
	case "folder":
		if body.Slug != "" {
			err = open("explorer-select", target)
		} else {
			err = open("folder", target)
		}
	case "obsidian":
		err = open("url", "obsidian://open?path="+strings.ReplaceAll(url.QueryEscape(target), "+", "%20"))
	default:
		fail(w, 400, "app desconocida")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// openWithSystem hands a folder, a file to point at, or a URL to the operating system.
func openWithSystem(kind, target string) error {
	switch runtime.GOOS {
	case "windows":
		switch kind {
		case "folder":
			return exec.Command("explorer.exe", target).Start()
		case "explorer-select":
			return exec.Command("explorer.exe", "/select,"+target).Start()
		}
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", target).Start()
	case "darwin":
		if kind == "explorer-select" {
			return exec.Command("open", "-R", target).Start()
		}
		return exec.Command("open", target).Start()
	}
	if kind == "explorer-select" {
		target = filepath.Dir(target)
	}
	return exec.Command("xdg-open", target).Start()
}

func (s *Server) vaultFiles(w http.ResponseWriter, r *http.Request) {
	files, err := s.vault().Files()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if files == nil {
		files = []vault.File{}
	}
	writeJSON(w, 200, files)
}

func (s *Server) vaultSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := s.vault().Search(r.URL.Query().Get("q"), 60)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if hits == nil {
		hits = []vault.Hit{}
	}
	writeJSON(w, 200, hits)
}

func (s *Server) readRaw(w http.ResponseWriter, r *http.Request) {
	text, mod, err := s.vault().ReadRaw(r.PathValue("dir"), r.PathValue("slug"))
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	writeJSON(w, 200, map[string]any{"text": text, "updated": mod})
}

func (s *Server) writeRaw(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text string `json:"text"`
		// Since is the time the editor read the file: a newer file means it changed meanwhile
		// (the app rewrote it, or you edited it in Obsidian), and saving would lose that.
		Since time.Time `json:"since"`
		Force bool      `json:"force"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	if !readJSON(w, r, &body) {
		return
	}
	v := s.vault()
	dir, slug := r.PathValue("dir"), r.PathValue("slug")
	_, mod, err := v.ReadRaw(dir, slug)
	if err != nil {
		fail(w, 404, "no existe esa nota")
		return
	}
	if !body.Force && !body.Since.IsZero() && mod.After(body.Since.Add(time.Second)) {
		fail(w, 409, "la nota cambió mientras la editabas")
		return
	}
	if err := v.WriteRaw(dir, slug, body.Text); err != nil {
		fail(w, 400, err.Error())
		return
	}
	_, mod, _ = v.ReadRaw(dir, slug)
	writeJSON(w, 200, map[string]any{"ok": true, "updated": mod})
}
