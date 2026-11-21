package web

import (
	"net/http"

	"github.com/krilinxito/studyhomie/internal/stats"
)

// stats returns the gamification snapshot (XP, streak, freezes, calendar, per-area progress)
// for the active vault.
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
	writeJSON(w, 200, stats.Compute(data, notes, concepts, cfg.DailyGoal, cfg.PassScore, s.Now()))
}
