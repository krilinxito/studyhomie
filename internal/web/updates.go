package web

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/i18n"
	"github.com/krilinxito/studyhomie/internal/update"
)

// updateStatus is the update state plus the setting that decides what to do with it.
type updateStatus struct {
	update.Status
	Mode       string `json:"mode"`
	CanRestart bool   `json:"canRestart"`
}

func (s *Server) updateStatus(st update.Status) updateStatus {
	_, cfg, _ := s.backend()
	return updateStatus{Status: st, Mode: update.Mode(cfg.Updates), CanRestart: s.Restart != nil}
}

// getUpdate reports whether there is a newer version. It asks GitHub at most every few hours,
// never when updates are off, and right away with ?force=1 (the "Buscar ahora" button).
func (s *Server) getUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.Updater.Enabled() {
		writeJSON(w, 200, updateStatus{Status: update.Status{Current: s.version()}, Mode: update.ModeNever})
		return
	}
	_, cfg, _ := s.backend()
	force := r.URL.Query().Get("force") == "1"
	var st update.Status
	if update.Mode(cfg.Updates) == update.ModeNever && !force {
		st = s.Updater.Status()
	} else {
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		st = s.Updater.Check(ctx, force)
	}
	writeJSON(w, 200, s.updateStatus(st))
}

func (s *Server) version() string {
	if s.Updater != nil {
		return s.Updater.Current
	}
	return "dev"
}

// busy reports whether a study flow is running, which a restart would cut short.
func (s *Server) busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active != nil && !s.active.isDone()
}

// installUpdate downloads, verifies and installs the new version, then restarts the app.
// While something is running it refuses unless the browser confirms with force.
func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Force bool `json:"force"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !s.Updater.Enabled() {
		fail(w, 400, "esta es una compilación de desarrollo: no se actualiza sola")
		return
	}
	if s.busy() && !body.Force {
		writeJSON(w, http.StatusConflict, map[string]any{"error": i18n.N("Hay algo en curso; si actualizas ahora se interrumpe."), "busy": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	tag, err := s.Updater.Install(ctx)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, update.ErrBusy) {
			status = http.StatusConflict
		}
		fail(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "version": tag, "restarting": s.restartSoon()})
}

// restartNow restarts into a version that was already installed (automatic mode).
func (s *Server) restartNow(w http.ResponseWriter, r *http.Request) {
	drain(r)
	writeJSON(w, 200, map[string]any{"ok": true, "restarting": s.restartSoon()})
}

// restartSoon asks main to restart once the response has gone out.
func (s *Server) restartSoon() bool {
	if s.Restart == nil {
		return false
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		s.Restart()
	}()
	return true
}

func (s *Server) skipUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version string `json:"version"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !s.Updater.Enabled() {
		fail(w, 400, "esta es una compilación de desarrollo")
		return
	}
	s.Updater.Skip(body.Version)
	writeJSON(w, 200, s.updateStatus(s.Updater.Status()))
}

// setUpdateMode saves "ask", "auto" or "never" to studyhomie.toml.
func (s *Server) setUpdateMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Mode != update.ModeAsk && body.Mode != update.ModeAuto && body.Mode != update.ModeNever {
		fail(w, 400, "modo no válido")
		return
	}
	s.pmu.Lock()
	cfg := s.Cfg
	cfg.Updates = body.Mode
	if s.ConfigPath != "" {
		if err := config.Write(s.ConfigPath, cfg); err != nil {
			s.pmu.Unlock()
			fail(w, 500, "no pude guardar "+s.ConfigPath+": "+err.Error())
			return
		}
	}
	s.Cfg = cfg
	s.pmu.Unlock()
	if body.Mode == update.ModeAuto {
		go s.autoUpdate(context.Background())
	}
	writeJSON(w, 200, s.updateStatus(s.Updater.Status()))
}

// WatchUpdates looks for new versions while the app runs: at start and then every hour (the
// updater only asks GitHub when its last answer is older than update.CheckEvery). In automatic
// mode it installs them; they apply the next time the app starts.
func (s *Server) WatchUpdates(ctx context.Context) {
	if !s.Updater.Enabled() {
		return
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.autoUpdate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (s *Server) autoUpdate(ctx context.Context) {
	_, cfg, _ := s.backend()
	mode := update.Mode(cfg.Updates)
	if mode == update.ModeNever {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	st := s.Updater.Check(cctx, false)
	cancel()
	if mode != update.ModeAuto || !st.Available || st.Staged || st.Skipped {
		return
	}
	ictx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	s.Updater.Install(ictx) // a failure shows up as "available" again; the next round retries
}
