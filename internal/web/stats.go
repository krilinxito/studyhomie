package web

import (
	"net/http"

	"github.com/krilinxito/studyhomie/internal/cards"
	"github.com/krilinxito/studyhomie/internal/stats"
)

// stats returns the gamification snapshot (XP, streak, freezes, calendar, per-area progress,
// and collectible flashcard summary) for the active vault.
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	v := s.vault()
	data, err := stats.Load(v.Root)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	notes, _ := v.List()
	concepts, _ := v.ListConcepts()
	_, cfg, _ := s.backend()
	now := s.Now()
	out := struct {
		stats.Summary
		Cards cards.CollectionSummary `json:"cards"`
	}{
		Summary: stats.Compute(data, notes, concepts, cfg.DailyGoal, cfg.PassScore, now),
		Cards:   cards.Summary(v.Root, now),
	}
	writeJSON(w, 200, out)
}

// tutorialSeen marks the first-run tutorial as completed for the active vault.
func (s *Server) tutorialSeen(w http.ResponseWriter, r *http.Request) {
	if err := stats.MarkTutorialSeen(s.vault().Root); err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
