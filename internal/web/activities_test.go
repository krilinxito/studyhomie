package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/krilinxito/studyhomie/internal/stats"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestActivitiesEndpointsAndXPDedup(t *testing.T) {
	s, ts := newTestServer(t, &scripted{replies: []string{
		"```json\n" + `{
  "activities": [
    {"level": "easy", "kind": "Experimento", "title": "Tono puro", "prompt": "Escucha 440 Hz y 880 Hz y nota la octava."},
    {"level": "challenge", "kind": "Mini-proyecto", "title": "Analiza un instrumento", "prompt": "Graba dos notas y compara sus picos."}
  ]
}` + "\n```",
		"Buen análisis de los picos espectrales; el segundo armónico suele dominar en cuerdas pulsadas cerca del puente.",
	}})
	now := time.Date(2026, 11, 20, 17, 0, 0, 0, time.Local)
	s.Now = func() time.Time { return now }

	if err := s.V.Save(vault.Note{
		Slug: "ondas",
		Meta: vault.Meta{Title: "Ondas sonoras", Status: "prepared", Area: "fisica"},
		Body: "# Ondas sonoras\n\n## Guía\n\nFrecuencia, amplitud y fase.\n",
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Generate activities
	res, body := do(t, ts, "POST", "/api/notes/ondas/activities", map[string]any{}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("generate status %d: %s", res.StatusCode, body)
	}
	var genOut struct {
		Activities []vault.Activity `json:"activities"`
	}
	if err := json.Unmarshal(body, &genOut); err != nil {
		t.Fatal(err)
	}
	if len(genOut.Activities) != 2 {
		t.Fatalf("got %d activities, want 2", len(genOut.Activities))
	}

	// 2. GET /api/notes/ondas includes parsed activities
	res, body = do(t, ts, "GET", "/api/notes/ondas", nil, nil)
	if res.StatusCode != 200 {
		t.Fatalf("get note status %d: %s", res.StatusCode, body)
	}
	var noteOut struct {
		Activities []vault.Activity `json:"activities"`
	}
	_ = json.Unmarshal(body, &noteOut)
	if len(noteOut.Activities) != 2 || noteOut.Activities[0].Title != "Tono puro" {
		t.Fatalf("GET note missing activities: %+v", noteOut.Activities)
	}

	// 3. Complete activity 0 (toggle without AI feedback) -> earns 15 XP
	res, body = do(t, ts, "POST", "/api/notes/ondas/activities/0", map[string]any{"done": true}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("toggle status %d: %s", res.StatusCode, body)
	}
	var updOut struct {
		Activity vault.Activity `json:"activity"`
		XPGained int            `json:"xpGained"`
	}
	_ = json.Unmarshal(body, &updOut)
	if !updOut.Activity.Done || updOut.XPGained != 15 {
		t.Fatalf("first completion: done=%v xp=%d, want true/15", updOut.Activity.Done, updOut.XPGained)
	}

	// 4. Uncheck and re-check activity 0 -> no duplicate XP
	do(t, ts, "POST", "/api/notes/ondas/activities/0", map[string]any{"done": false}, nil)
	res, body = do(t, ts, "POST", "/api/notes/ondas/activities/0", map[string]any{"done": true}, nil)
	updOut = struct {
		Activity vault.Activity `json:"activity"`
		XPGained int            `json:"xpGained"`
	}{}
	_ = json.Unmarshal(body, &updOut)
	if !updOut.Activity.Done || updOut.XPGained != 0 {
		t.Fatalf("re-check should not farm XP: done=%v xp=%d", updOut.Activity.Done, updOut.XPGained)
	}

	// 5. Complete activity 1 with submission + AI feedback -> earns 30 XP
	res, body = do(t, ts, "POST", "/api/notes/ondas/activities/1", map[string]any{
		"done":         true,
		"submission":   "Grabé guitarra en La y vi picos en 440, 880 y 1320 Hz.",
		"wantFeedback": true,
	}, nil)
	if res.StatusCode != 200 {
		t.Fatalf("feedback status %d: %s", res.StatusCode, body)
	}
	updOut = struct {
		Activity vault.Activity `json:"activity"`
		XPGained int            `json:"xpGained"`
	}{}
	_ = json.Unmarshal(body, &updOut)
	if !updOut.Activity.Done || updOut.XPGained != 30 || !strings.Contains(updOut.Activity.Feedback, "puente") {
		t.Fatalf("feedback completion mismatch: %+v xp=%d", updOut.Activity, updOut.XPGained)
	}

	// 6. Verify stats recorded both KindActivity events
	data, err := stats.Load(s.V.Root)
	if err != nil {
		t.Fatal(err)
	}
	actXP := 0
	for _, ev := range data.Events {
		if ev.Kind == stats.KindActivity {
			actXP += ev.XP
		}
	}
	if actXP != 45 {
		t.Fatalf("total activity XP = %d, want 45", actXP)
	}
}
