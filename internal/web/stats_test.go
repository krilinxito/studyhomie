package web

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestStatsEndpointAndReadDeduplication(t *testing.T) {
	s, ts := newTestServer(t, &scripted{})
	now := time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return now }

	if err := s.V.Save(vault.Note{
		Slug: "escalas",
		Meta: vault.Meta{Title: "Escalas", Status: "prepared", Area: "musica"},
		Body: "# Escalas\n\n## Guía\n\nContenido.\n",
	}); err != nil {
		t.Fatal(err)
	}

	// Reading a guide awards KindRead XP once per day.
	do(t, ts, "GET", "/api/notes/escalas", nil, nil)
	do(t, ts, "GET", "/api/notes/escalas", nil, nil)

	res, body := do(t, ts, "GET", "/api/stats", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("status = %d: %s", res.StatusCode, body)
	}
	var sum stats.Summary
	if err := json.Unmarshal(body, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.TodayXP != stats.XPFor(stats.KindRead, 0) || sum.TotalXP != stats.XPFor(stats.KindRead, 0) {
		t.Errorf("expected single read XP (%d), got today=%d total=%d", stats.XPFor(stats.KindRead, 0), sum.TodayXP, sum.TotalXP)
	}
	if sum.DailyGoal != stats.DefaultDailyGoal || len(sum.Areas) != 1 || sum.Areas[0].Area != "musica" {
		t.Errorf("summary = %+v", sum)
	}
	if len(sum.Calendar) != stats.CalendarDays {
		t.Errorf("calendar len = %d, want %d", len(sum.Calendar), stats.CalendarDays)
	}

	// Updating dailyGoal via /api/settings/study changes the goal and goalMet.
	res, body = do(t, ts, "POST", "/api/settings/study", map[string]any{"dailyGoal": 10}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("dailyGoal status = %d: %s", res.StatusCode, body)
	}
	_, body = do(t, ts, "GET", "/api/stats", nil, nil)
	json.Unmarshal(body, &sum)
	if sum.DailyGoal != 10 || !sum.GoalMet || sum.Streak != 1 {
		t.Errorf("after lowering goal to 10: goal=%d met=%v streak=%d", sum.DailyGoal, sum.GoalMet, sum.Streak)
	}

	// Reject out-of-range dailyGoal.
	if res, _ := do(t, ts, "POST", "/api/settings/study", map[string]any{"dailyGoal": 5}, nil); res.StatusCode != 400 {
		t.Errorf("dailyGoal=5 status = %d, want 400", res.StatusCode)
	}
}
