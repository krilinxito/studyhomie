package session

import (
	"context"
	"strings"
	"testing"

	"github.com/krilinxito/studyhomie/internal/provider"
	"github.com/krilinxito/studyhomie/internal/vault"
)

func TestGenerateAndReviewActivities(t *testing.T) {
	f := &fake{replies: []provider.Response{
		reply("```json\n"+`{
  "activities": [
    {"level": "easy", "kind": "Experimento", "title": "Graba una nota", "prompt": "Graba una vocal sostenida con tu celular y observa su espectro."},
    {"level": "medium", "kind": "Análisis", "title": "Compara dos timbres", "prompt": "Compara el espectro de un silbido y de tu voz cantando la misma nota."},
    {"level": "challenge", "kind": "Mini-proyecto", "title": "Filtro en tu cuarto", "prompt": "Graba un aplauso en dos habitaciones y estima el tiempo de reverberación."}
  ]
}`+"\n```", 10),
		reply("Excelente observación: el silbido casi solo tiene la fundamental mientras tu voz muestra varios armónicos claros. Prueba también cambiar de vocal manteniendo la misma altura para ver cómo se mueven los formantes.", 10),
	}}

	s, _ := newSession(t, f, "")
	if err := s.V.Save(vault.Note{
		Slug: "acustica",
		Meta: vault.Meta{Title: "Acústica básica", Status: "prepared", Area: "musica"},
		Body: "# Acústica básica\n\n## Guía\n\nEl sonido viaja como ondas de presión y cada timbre tiene armónicos.\n",
	}); err != nil {
		t.Fatal(err)
	}

	items, err := s.GenerateActivities(context.Background(), "acustica")
	if err != nil {
		t.Fatalf("GenerateActivities: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d activities, want 3", len(items))
	}
	if items[0].Level != vault.ActivityEasy || items[2].Level != vault.ActivityChallenge {
		t.Fatalf("unexpected levels: %+v", items)
	}

	// Verify it persisted in the Markdown note under ## Actividades as a checklist.
	note, err := s.V.Load("acustica")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Body, "## Actividades") || !strings.Contains(note.Body, "- [ ] ") {
		t.Fatalf("expected Obsidian checklist in note:\n%s", note.Body)
	}

	// Review activity 1 with learner submission.
	got, all, err := s.ReviewActivity(context.Background(), "acustica", 1, "El silbido mostró un solo pico en 520 Hz y mi voz mostró 5 picos.")
	if err != nil {
		t.Fatalf("ReviewActivity: %v", err)
	}
	if !got.Done || !strings.Contains(got.Feedback, "formantes") || !strings.Contains(got.Submission, "520 Hz") {
		t.Fatalf("unexpected reviewed activity: %+v", got)
	}
	if len(all) != 3 || !all[1].Done {
		t.Fatalf("updated list mismatch: %+v", all)
	}
}
