package vault

import (
	"strings"
	"testing"
)

func TestActivitiesRoundTripAndObsidianEdits(t *testing.T) {
	items := []Activity{
		{
			Done:   false,
			Level:  ActivityEasy,
			Kind:   "Experimento",
			Title:  "Escucha tus armónicos",
			Prompt: "Graba una nota sostenida con tu celular y observa su espectro.",
		},
		{
			Done:       true,
			Level:      ActivityChallenge,
			Kind:       "Mini-proyecto",
			Title:      "Filtro pasa-bajos",
			Prompt:     "Aplica un filtro pasa-bajos a un audio corto y compara la onda.",
			Submission: "Usé scipy.signal.butter con corte en 800 Hz y el silbido desapareció.",
			Feedback:   "Excelente prueba; observa también cómo cambia el ataque transitorio.",
		},
	}

	md := FormatActivities(items)
	body := ReplaceSection("# Fourier\n\n## Guía\n\nTexto.\n", "Actividades", md)

	got := ParseActivities(body)
	if len(got) != 2 {
		t.Fatalf("got %d activities, want 2:\n%s", len(got), body)
	}
	if got[0].Done || got[0].Level != ActivityEasy || got[0].Kind != "Experimento" || got[0].Title != "Escucha tus armónicos" || got[0].XP != 15 {
		t.Fatalf("activity 0 mismatch: %+v", got[0])
	}
	if !got[1].Done || got[1].Level != ActivityChallenge || got[1].XP != 30 {
		t.Fatalf("activity 1 mismatch: %+v", got[1])
	}
	if !strings.Contains(got[1].Submission, "800 Hz") || !strings.Contains(got[1].Feedback, "transitorio") {
		t.Fatalf("submission/feedback lost: %+v", got[1])
	}

	// Plain hand-written Obsidian checklist under an English heading must also parse cleanly.
	manual := "# Sound\n\n## Activities\n\n- [ ] Record a clap in two different rooms and compare echo decay.\n- [x] **[Challenge · Analysis]** Compare room reverb times.\n"
	gotManual := ParseActivities(manual)
	if len(gotManual) != 2 {
		t.Fatalf("manual parse got %d, want 2", len(gotManual))
	}
	if gotManual[0].Level != ActivityMedium || gotManual[0].Done {
		t.Fatalf("manual 0 mismatch: %+v", gotManual[0])
	}
	if gotManual[1].Level != ActivityChallenge || !gotManual[1].Done || gotManual[1].Kind != "Analysis" {
		t.Fatalf("manual 1 mismatch: %+v", gotManual[1])
	}
}
