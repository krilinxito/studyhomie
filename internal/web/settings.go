package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/krilinxito/studyhomie/internal/config"
	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/providers"
	"github.com/krilinxito/studyhomie/internal/stats"
)

// backend returns the provider and config in use. Settings can swap them while the app runs;
// a session already running keeps the ones it started with.
func (s *Server) backend() (provider.Provider, config.Config, string) {
	s.pmu.RLock()
	defer s.pmu.RUnlock()
	return s.P, s.Cfg, s.Warning
}

// aiSettings is the part of the config the settings page edits.
type aiSettings struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	QuickModel string `json:"quickModel"`
	BaseURL    string `json:"baseURL"`
}

func (a aiSettings) apply(cfg config.Config) config.Config {
	cfg.Provider = strings.TrimSpace(a.Provider)
	cfg.Model = strings.TrimSpace(a.Model)
	cfg.QuickModel = strings.TrimSpace(a.QuickModel)
	cfg.BaseURL = strings.TrimSpace(a.BaseURL)
	return cfg
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	_, cfg, warning := s.backend()
	writeJSON(w, 200, map[string]any{
		"current":    aiSettings{Provider: cfg.Provider, Model: cfg.Model, QuickModel: cfg.QuickModel, BaseURL: cfg.BaseURL},
		"providers":  providers.Statuses(r.Context(), cfg),
		"warning":    warning,
		"configPath": s.ConfigPath,
		"study":      studySettings{PassScore: &cfg.PassScore, Timeout: &cfg.Timeout, DailyGoal: intPtr(stats.NormalizeGoal(cfg.DailyGoal)), MaxParallel: intPtr(maxParallel(cfg))},
		"sources":    sourceSettings{YouTubeKey: cfg.YouTubeKey, BraveKey: cfg.BraveKey},
	})
}

// sourceSettings holds the optional API keys for searching YouTube and Brave Search.
type sourceSettings struct {
	YouTubeKey string `json:"youtubeKey"`
	BraveKey   string `json:"braveKey"`
}

// saveSourceSettings writes the optional source API keys to studyhomie.toml.
func (s *Server) saveSourceSettings(w http.ResponseWriter, r *http.Request) {
	var body sourceSettings
	if !readJSON(w, r, &body) {
		return
	}
	s.pmu.Lock()
	cfg := s.Cfg
	cfg.YouTubeKey = strings.TrimSpace(body.YouTubeKey)
	cfg.BraveKey = strings.TrimSpace(body.BraveKey)
	if s.ConfigPath != "" {
		if err := config.Write(s.ConfigPath, cfg); err != nil {
			s.pmu.Unlock()
			fail(w, 500, "no pude guardar "+s.ConfigPath+": "+err.Error())
			return
		}
	}
	s.Cfg = cfg
	s.pmu.Unlock()
	writeJSON(w, 200, sourceSettings{YouTubeKey: cfg.YouTubeKey, BraveKey: cfg.BraveKey})
}

func intPtr(v int) *int { return &v }

// studySettings are how studying works: the mastery a roadmap topic needs to unlock the next one,
// how many minutes one AI call may take, and the daily XP goal. Any field can be sent alone.
type studySettings struct {
	PassScore *int `json:"passScore,omitempty"`
	Timeout   *int `json:"timeout,omitempty"`
	DailyGoal *int `json:"dailyGoal,omitempty"`
	// MaxParallel is how many flows (chat, practices) may call the AI at the same time.
	MaxParallel *int `json:"maxParallel,omitempty"`
}

// maxParallelCap bounds max_parallel: each flow may be a Claude CLI process of its own.
const maxParallelCap = 6

func maxParallel(cfg config.Config) int {
	if cfg.MaxParallel <= 0 {
		return 2
	}
	return min(cfg.MaxParallel, maxParallelCap)
}

// maxTimeout bounds the time limit of an AI call that can be set, in minutes.
const maxTimeout = 60

// saveStudySettings writes the study settings to studyhomie.toml. Sessions read them when they start.
func (s *Server) saveStudySettings(w http.ResponseWriter, r *http.Request) {
	var body studySettings
	if !readJSON(w, r, &body) {
		return
	}
	if body.MaxParallel != nil && (*body.MaxParallel < 1 || *body.MaxParallel > maxParallelCap) {
		fail(w, 400, "las llamadas a la vez van de 1 a 6")
		return
	}
	if (body.PassScore == nil && body.Timeout == nil && body.DailyGoal == nil && body.MaxParallel == nil) || (body.PassScore != nil && (*body.PassScore < 0 || *body.PassScore > 100)) {
		fail(w, 400, "la nota para avanzar va de 0 a 100")
		return
	}
	if body.Timeout != nil && (*body.Timeout < 0 || *body.Timeout > maxTimeout) {
		fail(w, 400, "el límite de tiempo va de 0 a 60 minutos")
		return
	}
	if body.DailyGoal != nil && (*body.DailyGoal < stats.MinDailyGoal || *body.DailyGoal > stats.MaxDailyGoal) {
		fail(w, 400, "la meta diaria va de 10 a 500 XP")
		return
	}
	s.pmu.Lock()
	cfg := s.Cfg
	if body.PassScore != nil {
		cfg.PassScore = *body.PassScore
	}
	if body.Timeout != nil {
		cfg.Timeout = *body.Timeout
	}
	if body.DailyGoal != nil {
		cfg.DailyGoal = *body.DailyGoal
	}
	if body.MaxParallel != nil {
		cfg.MaxParallel = *body.MaxParallel
	}
	if s.ConfigPath != "" {
		if err := config.Write(s.ConfigPath, cfg); err != nil {
			s.pmu.Unlock()
			fail(w, 500, "no pude guardar "+s.ConfigPath+": "+err.Error())
			return
		}
	}
	s.Cfg = cfg
	s.pmu.Unlock()
	writeJSON(w, 200, studySettings{PassScore: &cfg.PassScore, Timeout: &cfg.Timeout, DailyGoal: intPtr(stats.NormalizeGoal(cfg.DailyGoal)), MaxParallel: intPtr(maxParallel(cfg))})
}

// saveSettings switches the AI backend, and writes the choice to studyhomie.toml.
func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	var body aiSettings
	if !readJSON(w, r, &body) {
		return
	}
	if _, ok := providers.Find(body.Provider); !ok {
		fail(w, 400, "proveedor desconocido")
		return
	}
	_, cur, _ := s.backend()
	cfg := body.apply(cur)
	p, err := providers.New(cfg)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.ConfigPath != "" {
		if err := config.Write(s.ConfigPath, cfg); err != nil {
			fail(w, 500, "no pude guardar "+s.ConfigPath+": "+err.Error())
			return
		}
	}
	warning := providers.Check(cfg)
	s.pmu.Lock()
	s.P, s.Cfg, s.Warning = p, cfg, warning
	s.pmu.Unlock()
	writeJSON(w, 200, map[string]any{"ok": true, "provider": p.Name(), "warning": warning})
}

// listModels asks a backend which models it offers.
func (s *Server) listModels(w http.ResponseWriter, r *http.Request) {
	var body aiSettings
	if !readJSON(w, r, &body) {
		return
	}
	_, cur, _ := s.backend()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	models, err := providers.Models(ctx, body.apply(cur))
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if models == nil {
		models = []string{}
	}
	writeJSON(w, 200, map[string]any{"models": models})
}

// testSettings sends one tiny message with the given settings, without saving them.
func (s *Server) testSettings(w http.ResponseWriter, r *http.Request) {
	var body aiSettings
	if !readJSON(w, r, &body) {
		return
	}
	_, cur, _ := s.backend()
	cfg := body.apply(cur)
	if msg := providers.Check(cfg); msg != "" {
		fail(w, 400, msg)
		return
	}
	p, err := providers.New(cfg)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	drain(r)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	start := time.Now()
	resp, err := p.Complete(ctx, provider.Request{Prompt: "Responde solo con la palabra: listo", Model: cfg.Model}, nil)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		fail(w, status, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "reply": strings.TrimSpace(resp.Text), "ms": time.Since(start).Milliseconds()})
}
